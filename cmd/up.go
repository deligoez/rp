package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/runner"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
)

var (
	upDryRun    bool
	upNoInstall bool
	upNoUpdate  bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bootstrap, sync, install, and update repos in one pass",
	RunE:  runUp,
}

func init() {
	upCmd.Flags().BoolVar(&upDryRun, "dry-run", false, "preview all phases without making changes")
	upCmd.Flags().BoolVar(&upNoInstall, "no-install", false, "skip install phase")
	upCmd.Flags().BoolVar(&upNoUpdate, "no-update", false, "skip update phase")
	rootCmd.AddCommand(upCmd)
}

func runUp(cmd *cobra.Command, args []string) error {
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("up", fmt.Errorf("loading manifest: %w", err))
		}
		return fmt.Errorf("loading manifest: %w", err)
	}

	repos := manifest.FilterRepos(m.Repos(), Filters)

	if output.IsJSON() {
		return runUpJSON(m, repos)
	}
	return runUpHuman(m, repos)
}

// ── Human mode ──────────────────────────────────────────────────────────────

func runUpHuman(m *manifest.Manifest, repos []manifest.RepoEntry) error {
	owners := manifest.FilterOwners(m.Owners(), Filters)

	boot := upHumanBootstrap(repos, owners)
	sync := upHumanSync(repos, owners, boot.clonedSet)
	inst := upHumanInstall(repos, owners, boot.clonedSet)
	upd := upHumanUpdate(repos, owners, boot.clonedSet, boot.failedSet)

	printUpHumanSummary(boot, sync, inst, upd)

	if upDryRun {
		os.Exit(0)
	}
	if code := upHumanExitCode(boot, sync, inst, upd); code != 0 {
		os.Exit(code)
	}
	return nil
}

// upBootstrapOutcome is what the later phases need from bootstrap: which
// repos it cloned, which it failed on, and the counts for the summary.
type upBootstrapOutcome struct {
	clonedSet map[string]bool
	failedSet map[string]bool
	cloned    int
	existed   int
	failed    int
}

// upSyncCounts holds the sync phase's summary numbers.
type upSyncCounts struct {
	pulled   int
	upToDate int
	skipped  int
	failed   int
}

// upCommandCounts holds one command phase's summary numbers. Under --dry-run
// the dry counts are reported instead of succeeded/failed.
type upCommandCounts struct {
	ran         bool
	succeeded   int
	failed      int
	dryRepos    int
	dryCommands int
}

// upHumanBootstrap clones what is missing and prints the bootstrap block.
func upHumanBootstrap(repos []manifest.RepoEntry, owners []manifest.OwnerGroup) upBootstrapOutcome {
	// ── Phase 1: Bootstrap ───────────────────────────────────────────────────
	fmt.Println("== Bootstrap ==")

	bootstrapResults := worker.PoolWithProgress(
		repos,
		Concurrency,
		worker.PoolOptions{Verb: "cloning"},
		func(entry manifest.RepoEntry) (bootstrapResult, error) {
			if upDryRun {
				return processBootstrapDryRun(entry), nil
			}
			return processBootstrapEntry(entry), nil
		},
	)

	// Build lookup map and track cloned/failed sets.
	bootstrapMap := make(map[string]bootstrapResult, len(bootstrapResults))
	clonedSet := make(map[string]bool)
	failedSet := make(map[string]bool)
	for _, r := range bootstrapResults {
		bootstrapMap[r.Value.Entry.LocalPath] = r.Value
		switch r.Value.Status {
		case bsCloned, bsWouldClone:
			clonedSet[r.Value.Entry.Repo] = true
		case bsFailed:
			failedSet[r.Value.Entry.Repo] = true
		}
	}

	var bsClonedCount, bsExistedCount, bsFailedCount int
	for _, ownerGroup := range owners {
		fmt.Println(ownerGroup.Name)
		for _, entry := range ownerGroup.Repos {
			res, ok := bootstrapMap[entry.LocalPath]
			if !ok {
				continue
			}
			label := repoLabel(entry)
			switch res.Status {
			case bsCloned:
				bsClonedCount++
				fmt.Printf("  %s  %s\n", ui.PadRight(label, 24), ui.SymbolOK()+" cloned")
			case bsAlreadyExists:
				bsExistedCount++
				fmt.Printf("  %s  already exists\n", ui.PadRight(label, 24))
			case bsFailed:
				bsFailedCount++
				fmt.Printf("  %s  %s\n", ui.PadRight(label, 24), ui.SymbolError()+" FAILED: "+res.ErrMsg)
			case bsWouldClone:
				bsClonedCount++
				fmt.Printf("  %s  would clone %s\n", ui.PadRight(label, 24), entry.CloneURL)
			case bsWouldSkip:
				bsExistedCount++
				fmt.Printf("  %s  already exists — would skip\n", ui.PadRight(label, 24))
			}
		}
	}
	fmt.Println()

	return upBootstrapOutcome{
		clonedSet: clonedSet,
		failedSet: failedSet,
		cloned:    bsClonedCount,
		existed:   bsExistedCount,
		failed:    bsFailedCount,
	}
}

// upHumanSync pulls what is clean and prints the sync block. Repos bootstrap
// just cloned are already current and are reported as such.
func upHumanSync(repos []manifest.RepoEntry, owners []manifest.OwnerGroup, clonedSet map[string]bool) upSyncCounts {
	// ── Phase 2: Sync ────────────────────────────────────────────────────────
	fmt.Println("== Sync ==")

	syncResults := worker.PoolWithProgress(
		repos,
		Concurrency,
		worker.PoolOptions{Verb: "syncing"},
		func(entry manifest.RepoEntry) (syncResult, error) {
			// Repos just cloned in phase 1 are treated as up-to-date.
			if clonedSet[entry.Repo] {
				label := repoLabel(entry)
				return syncResult{
					label:    label,
					status:   ui.SymbolOK() + " up to date",
					exitCode: 0,
					repo:     entry.Repo,
					action:   syncActionUpToDate,
				}, nil
			}
			label := repoLabel(entry)
			return processSyncRepo(entry, label, upDryRun), nil
		},
	)

	syncMap := make(map[string]syncResult, len(syncResults))
	for _, r := range syncResults {
		syncMap[r.Value.repo] = r.Value
	}

	var syPulled, syUpToDate, sySkipped, syFailed int
	for _, ownerGroup := range owners {
		fmt.Println(ownerGroup.Name)
		for _, entry := range ownerGroup.Repos {
			res, ok := syncMap[entry.Repo]
			if !ok {
				continue
			}
			fmt.Printf("  %-30s %s\n", res.label, res.status)
			switch res.action {
			case syncActionPulled:
				syPulled++
			case syncActionUpToDate, syncActionCloned:
				syUpToDate++
			case syncActionSkipped:
				sySkipped++
			case syncActionFailed:
				syFailed++
			}
		}
		fmt.Println()
	}

	return upSyncCounts{pulled: syPulled, upToDate: syUpToDate, skipped: sySkipped, failed: syFailed}
}

// upCommandPhase describes one of up's two command phases. Install and update
// differ only in which repos they target, which command list they run, and
// whether a dry run first checks the repo is on disk: install targets are
// repos bootstrap would clone, so under --dry-run they are not there yet.
type upCommandPhase struct {
	name            string
	verb            string
	enabled         bool
	commandsOf      func(manifest.RepoEntry) []string
	isTarget        func(manifest.RepoEntry) bool
	dryRunNeedsDisk bool
}

// upHumanInstall runs install commands for the repos bootstrap just cloned.
func upHumanInstall(repos []manifest.RepoEntry, owners []manifest.OwnerGroup, clonedSet map[string]bool) upCommandCounts {
	return runUpHumanCommandPhase(upCommandPhase{
		name:       "Install",
		verb:       "installing",
		enabled:    !upNoInstall,
		commandsOf: func(r manifest.RepoEntry) []string { return r.Install },
		isTarget:   func(r manifest.RepoEntry) bool { return clonedSet[r.Repo] },
	}, repos, owners)
}

// upHumanUpdate runs update commands for the repos that already existed.
func upHumanUpdate(repos []manifest.RepoEntry, owners []manifest.OwnerGroup, clonedSet, failedSet map[string]bool) upCommandCounts {
	return runUpHumanCommandPhase(upCommandPhase{
		name:            "Update",
		verb:            "updating",
		enabled:         !upNoUpdate,
		commandsOf:      func(r manifest.RepoEntry) []string { return r.Update },
		isTarget:        func(r manifest.RepoEntry) bool { return !clonedSet[r.Repo] && !failedSet[r.Repo] },
		dryRunNeedsDisk: true,
	}, repos, owners)
}

// runUpHumanCommandPhase prints one command phase's block and reports its
// counts. A disabled phase prints nothing and reports ran=false.
func runUpHumanCommandPhase(phase upCommandPhase, repos []manifest.RepoEntry, owners []manifest.OwnerGroup) upCommandCounts {
	counts := upCommandCounts{ran: phase.enabled}
	if !phase.enabled {
		return counts
	}

	fmt.Printf("== %s ==\n", phase.name)

	var targets []manifest.RepoEntry
	for _, r := range repos {
		if len(phase.commandsOf(r)) > 0 && phase.isTarget(r) {
			targets = append(targets, r)
		}
	}
	if len(targets) == 0 {
		return counts
	}

	if upDryRun {
		printUpCommandDryRun(phase, targets, owners, &counts)
	} else {
		runUpCommandPhase(phase, targets, owners, &counts)
	}
	fmt.Println()

	return counts
}

// printUpCommandDryRun lists the commands each target would run.
func printUpCommandDryRun(phase upCommandPhase, targets []manifest.RepoEntry, owners []manifest.OwnerGroup, counts *upCommandCounts) {
	inTargets := make(map[string]bool, len(targets))
	for _, t := range targets {
		inTargets[t.Repo] = true
	}

	for _, ownerGroup := range owners {
		ownerPrinted := false
		for _, entry := range ownerGroup.Repos {
			if !inTargets[entry.Repo] {
				continue
			}
			if phase.dryRunNeedsDisk {
				if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "  warning: %s not found on disk, skipping\n", entry.LocalPath)
					continue
				}
			}
			if !ownerPrinted {
				fmt.Println(ownerGroup.Name)
				ownerPrinted = true
			}
			paddedLabel := ui.PadRight(repoLabel(entry), 24)
			for _, command := range phase.commandsOf(entry) {
				fmt.Printf("  %s would run: %s\n", paddedLabel, command)
				counts.dryCommands++
			}
			counts.dryRepos++
		}
	}
}

// runUpCommandPhase executes each target's commands and prints the results in
// manifest order.
func runUpCommandPhase(phase upCommandPhase, targets []manifest.RepoEntry, owners []manifest.OwnerGroup, counts *upCommandCounts) {
	results := worker.PoolWithProgress(
		targets,
		Concurrency,
		worker.PoolOptions{Verb: phase.verb},
		func(entry manifest.RepoEntry) (repoCommandResult, error) {
			return runRepoCommands(entry, phase.commandsOf(entry)), nil
		},
	)

	resultMap := make(map[string]repoCommandResult, len(results))
	for _, r := range results {
		resultMap[r.Value.entry.Repo] = r.Value
	}

	for _, ownerGroup := range owners {
		ownerPrinted := false
		for _, entry := range ownerGroup.Repos {
			res, ok := resultMap[entry.Repo]
			if !ok {
				continue
			}
			paddedLabel := ui.PadRight(repoLabel(entry), 24)
			if !ownerPrinted {
				fmt.Println(ownerGroup.Name)
				ownerPrinted = true
			}
			if res.skipped {
				fmt.Fprintf(os.Stderr, "  %s\n", res.skipMsg)
				continue
			}
			repoFailed := false
			for _, cr := range res.results {
				if cr.failed {
					fmt.Printf("  %s FAILED: %s (%s)\n", paddedLabel, cr.command, cr.errMsg)
					repoFailed = true
				} else {
					fmt.Printf("  %s %s %s\n", paddedLabel, ui.SymbolOK(), cr.command)
				}
			}
			if repoFailed {
				counts.failed++
			} else if len(res.results) > 0 {
				counts.succeeded++
			}
		}
	}
}

// printUpHumanSummary prints the closing summary block.
func printUpHumanSummary(boot upBootstrapOutcome, sync upSyncCounts, inst, upd upCommandCounts) {
	bsClonedCount, bsExistedCount, bsFailedCount := boot.cloned, boot.existed, boot.failed
	syPulled, syUpToDate, sySkipped := sync.pulled, sync.upToDate, sync.skipped
	runInstallPhase, dryInstRepos, dryInstCommands := inst.ran, inst.dryRepos, inst.dryCommands
	instSucceeded, instFailed := inst.succeeded, inst.failed
	runUpdatePhase, dryUpdRepos, dryUpdCommands := upd.ran, upd.dryRepos, upd.dryCommands
	updSucceeded, updFailed := upd.succeeded, upd.failed
	// ── Summary ──────────────────────────────────────────────────────────────
	fmt.Println("-- Summary --")
	fmt.Printf("%s, %s, %s\n",
		fmt.Sprintf("%d cloned", bsClonedCount),
		pluralExisted(bsExistedCount),
		fmt.Sprintf("%d failed", bsFailedCount),
	)
	fmt.Printf("%s pulled, %d up to date, %d skipped\n", fmt.Sprintf("%d", syPulled), syUpToDate, sySkipped)

	if runInstallPhase {
		if upDryRun {
			fmt.Printf("install: %s, %s would run\n", ui.Plural(dryInstRepos, "repo"), ui.Plural(dryInstCommands, "command"))
		} else {
			fmt.Printf("install: %d succeeded, %d failed\n", instSucceeded, instFailed)
		}
	}

	if runUpdatePhase {
		if upDryRun {
			fmt.Printf("update: %s, %s would run\n", ui.Plural(dryUpdRepos, "repo"), ui.Plural(dryUpdCommands, "command"))
		} else {
			fmt.Printf("update: %d succeeded, %d failed\n", updSucceeded, updFailed)
		}
	}
}

// upHumanExitCode is the highest exit code across the four phases.
func upHumanExitCode(boot upBootstrapOutcome, sync upSyncCounts, inst, upd upCommandCounts) int {
	if boot.failed > 0 || sync.failed > 0 || inst.failed > 0 || upd.failed > 0 {
		return 2
	}
	if sync.skipped > 0 {
		return 1
	}
	return 0
}

// ── JSON mode ────────────────────────────────────────────────────────────────

func runUpJSON(m *manifest.Manifest, repos []manifest.RepoEntry) error {
	result := output.UpResult{
		Command: "up",
		DryRun:  upDryRun,
	}

	// ── Phase 1: Bootstrap ───────────────────────────────────────────────────
	var bootstrapWorkerResults []worker.Result[bootstrapResult]

	if upDryRun {
		// Dry-run: evaluate without cloning.
		for _, entry := range repos {
			var st bootstrapStatus
			info, err := os.Stat(entry.LocalPath)
			if err == nil && info.IsDir() {
				st = bsWouldSkip
			} else {
				st = bsWouldClone
			}
			bootstrapWorkerResults = append(bootstrapWorkerResults, worker.Result[bootstrapResult]{
				Value: bootstrapResult{Entry: entry, Status: st},
			})
		}
	} else {
		bootstrapWorkerResults = worker.PoolWithProgress(
			repos,
			Concurrency,
			worker.PoolOptions{Verb: "cloning"},
			func(entry manifest.RepoEntry) (bootstrapResult, error) {
				return processBootstrapEntry(entry), nil
			},
		)
	}

	clonedSet := make(map[string]bool)
	failedSet := make(map[string]bool)
	{
		var nCloned, nExisted, nFailed int
		bsRepos := make([]bootstrapRepoJSON, 0, len(bootstrapWorkerResults))
		for _, r := range bootstrapWorkerResults {
			res := r.Value
			rj := bootstrapRepoJSON{
				Repo:      res.Entry.Repo,
				LocalPath: res.Entry.LocalPath,
				Error:     res.ErrMsg,
			}
			switch res.Status {
			case bsCloned:
				nCloned++
				rj.Action = "cloned"
				clonedSet[res.Entry.Repo] = true
			case bsAlreadyExists:
				nExisted++
				rj.Action = "already_exists"
			case bsFailed:
				nFailed++
				rj.Action = "failed"
				failedSet[res.Entry.Repo] = true
			case bsWouldClone:
				nCloned++
				rj.Action = "would_clone"
				clonedSet[res.Entry.Repo] = true
			case bsWouldSkip:
				nExisted++
				rj.Action = "would_skip"
			}
			bsRepos = append(bsRepos, rj)
		}

		result.Bootstrap = &output.SubResult{
			Summary: bootstrapSummaryJSON{
				Cloned:         nCloned,
				AlreadyExisted: nExisted,
				Failed:         nFailed,
				Total:          len(bootstrapWorkerResults),
			},
			Repos: bsRepos,
		}
	}

	// ── Phase 2: Sync ────────────────────────────────────────────────────────
	syncWorkerResults := worker.PoolWithProgress(
		repos,
		Concurrency,
		worker.PoolOptions{Verb: "syncing"},
		func(entry manifest.RepoEntry) (syncResult, error) {
			if clonedSet[entry.Repo] {
				return syncResult{
					label:    repoLabel(entry),
					status:   ui.SymbolOK() + " up to date",
					exitCode: 0,
					repo:     entry.Repo,
					action:   syncActionUpToDate,
				}, nil
			}
			label := repoLabel(entry)
			return processSyncRepo(entry, label, upDryRun), nil
		},
	)

	{
		summary := syncSummaryJSON{Total: len(syncWorkerResults)}
		syncRepos := make([]syncRepoJSON, 0, len(syncWorkerResults))
		for _, r := range syncWorkerResults {
			v := r.Value
			rj := syncRepoJSON{Repo: v.repo}
			switch v.action {
			case syncActionPulled:
				rj.Action = "pulled"
				rj.NewCommits = v.newCommits
				summary.Pulled++
			case syncActionUpToDate:
				rj.Action = "up_to_date"
				summary.UpToDate++
			case syncActionCloned:
				rj.Action = "cloned"
				summary.Cloned++
			case syncActionSkipped:
				rj.Action = "skipped"
				summary.Skipped++
				switch v.skipReason {
				case syncSkipDirty:
					rj.Reason = "dirty"
					rj.DirtyFiles = v.dirtyFiles
				case syncSkipUnpushed:
					rj.Reason = "unpushed"
					rj.Ahead = v.ahead
					rj.Branch = v.branch
				case syncSkipDiverged:
					rj.Reason = "diverged"
				case syncSkipNoUpstream:
					rj.Reason = "no_upstream"
				case syncSkipNotARepo:
					rj.Reason = "not_a_repo"
				}
			case syncActionFailed:
				rj.Action = "failed"
				rj.Error = v.errMsg
				summary.Failed++
			case syncActionWouldPull:
				rj.Action = "would_pull"
			case syncActionWouldSkip:
				rj.Action = "would_skip"
				switch v.skipReason {
				case syncSkipDirty:
					rj.Reason = "dirty"
					rj.DirtyFiles = v.dirtyFiles
				case syncSkipUnpushed:
					rj.Reason = "unpushed"
					rj.Ahead = v.ahead
					rj.Branch = v.branch
				case syncSkipDiverged:
					rj.Reason = "diverged"
				case syncSkipNoUpstream:
					rj.Reason = "no_upstream"
				case syncSkipNotARepo:
					rj.Reason = "not_a_repo"
				}
			case syncActionWouldClone:
				rj.Action = "would_clone"
			}
			syncRepos = append(syncRepos, rj)
		}
		result.Sync = &output.SubResult{
			Summary: summary,
			Repos:   syncRepos,
		}
	}

	// ── Phase 3: Install (cloned repos only) ─────────────────────────────────
	type jsonCmdEntry struct {
		Command string `json:"command"`
		Status  string `json:"status"`
		Error   string `json:"error,omitempty"`
	}
	type jsonRepoEntry struct {
		Repo     string         `json:"repo"`
		Status   string         `json:"status"`
		Reason   string         `json:"reason,omitempty"`
		Commands []jsonCmdEntry `json:"commands,omitempty"`
	}

	runInstallPhase := !upNoInstall

	if runInstallPhase {
		var installTargets []manifest.RepoEntry
		for _, r := range repos {
			if len(r.Install) > 0 && clonedSet[r.Repo] {
				installTargets = append(installTargets, r)
			}
		}

		if upDryRun {
			if len(installTargets) > 0 {
				var dryRepos, dryCommands, drySkipped int
				instRepos := make([]jsonRepoEntry, 0, len(installTargets))

				for _, target := range installTargets {
					if _, err := os.Stat(target.LocalPath); os.IsNotExist(err) {
						drySkipped++
						instRepos = append(instRepos, jsonRepoEntry{
							Repo:   target.Repo,
							Status: "skipped",
							Reason: "not_on_disk",
						})
						continue
					}
					cmds := make([]jsonCmdEntry, 0, len(target.Install))
					for _, command := range target.Install {
						dryCommands++
						cmds = append(cmds, jsonCmdEntry{Command: command, Status: "would_run"})
					}
					instRepos = append(instRepos, jsonRepoEntry{
						Repo:     target.Repo,
						Status:   "ok",
						Commands: cmds,
					})
					dryRepos++
				}

				result.Install = &output.SubResult{
					Summary: map[string]int{
						"repos":    dryRepos,
						"commands": dryCommands,
						"skipped":  drySkipped,
					},
					Repos: instRepos,
				}
			} else {
				result.Install = &output.SubResult{
					Summary: map[string]int{"succeeded": 0, "skipped": 0, "failed": 0, "total": 0, "commands": 0},
					Repos:   []interface{}{},
				}
			}
		} else if len(installTargets) > 0 {
			instWorkerResults := worker.PoolWithProgress(
				installTargets,
				Concurrency,
				worker.PoolOptions{Verb: "installing"},
				func(entry manifest.RepoEntry) (repoCommandResult, error) {
					res := repoCommandResult{entry: entry}
					if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
						res.skipped = true
						res.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
						return res, nil
					}
					for _, command := range entry.Install {
						err := runner.RunCommands(entry.LocalPath, []string{command})
						cr := commandOutcome{command: command}
						if err != nil {
							cr.failed = true
							cr.errMsg = err.Error()
							res.results = append(res.results, cr)
							break
						}
						res.results = append(res.results, cr)
					}
					return res, nil
				},
			)

			instMap := make(map[string]repoCommandResult, len(instWorkerResults))
			for _, r := range instWorkerResults {
				instMap[r.Value.entry.Repo] = r.Value
			}

			var instSucceeded, instFailed, instSkipped, instCommands int
			instRepos := make([]jsonRepoEntry, 0, len(installTargets))

			for _, target := range installTargets {
				res, ok := instMap[target.Repo]
				if !ok {
					continue
				}
				if res.skipped {
					instSkipped++
					instRepos = append(instRepos, jsonRepoEntry{
						Repo:   target.Repo,
						Status: "skipped",
						Reason: "not_on_disk",
					})
					continue
				}
				repoFailed := false
				var cmds []jsonCmdEntry
				for _, cr := range res.results {
					instCommands++
					e := jsonCmdEntry{Command: cr.command}
					if cr.failed {
						e.Status = "failed"
						e.Error = cr.errMsg
						repoFailed = true
					} else {
						e.Status = "ok"
					}
					cmds = append(cmds, e)
				}
				repoStatus := "ok"
				if repoFailed {
					repoStatus = "failed"
					instFailed++
				} else {
					instSucceeded++
				}
				instRepos = append(instRepos, jsonRepoEntry{
					Repo:     target.Repo,
					Status:   repoStatus,
					Commands: cmds,
				})
			}

			result.Install = &output.SubResult{
				Summary: map[string]int{
					"succeeded": instSucceeded,
					"failed":    instFailed,
					"skipped":   instSkipped,
					"total":     len(installTargets),
					"commands":  instCommands,
				},
				Repos: instRepos,
			}
		} else {
			result.Install = &output.SubResult{
				Summary: map[string]int{"succeeded": 0, "skipped": 0, "failed": 0, "total": 0, "commands": 0},
				Repos:   []interface{}{},
			}
		}
	}

	// ── Phase 4: Update (pre-existing repos only) ────────────────────────────
	runUpdatePhase := !upNoUpdate

	if runUpdatePhase {
		var updateTargets []manifest.RepoEntry
		for _, r := range repos {
			if len(r.Update) > 0 && !clonedSet[r.Repo] && !failedSet[r.Repo] {
				updateTargets = append(updateTargets, r)
			}
		}

		if upDryRun {
			if len(updateTargets) > 0 {
				var dryRepos, dryCommands, drySkipped int
				updRepos := make([]jsonRepoEntry, 0, len(updateTargets))

				for _, target := range updateTargets {
					if _, err := os.Stat(target.LocalPath); os.IsNotExist(err) {
						drySkipped++
						updRepos = append(updRepos, jsonRepoEntry{
							Repo:   target.Repo,
							Status: "skipped",
							Reason: "not_on_disk",
						})
						continue
					}
					cmds := make([]jsonCmdEntry, 0, len(target.Update))
					for _, command := range target.Update {
						dryCommands++
						cmds = append(cmds, jsonCmdEntry{Command: command, Status: "would_run"})
					}
					updRepos = append(updRepos, jsonRepoEntry{
						Repo:     target.Repo,
						Status:   "ok",
						Commands: cmds,
					})
					dryRepos++
				}

				result.Update = &output.SubResult{
					Summary: map[string]int{
						"repos":    dryRepos,
						"commands": dryCommands,
						"skipped":  drySkipped,
					},
					Repos: updRepos,
				}
			} else {
				result.Update = &output.SubResult{
					Summary: map[string]int{"succeeded": 0, "skipped": 0, "failed": 0, "total": 0, "commands": 0},
					Repos:   []interface{}{},
				}
			}
		} else if len(updateTargets) > 0 {
			updWorkerResults := worker.PoolWithProgress(
				updateTargets,
				Concurrency,
				worker.PoolOptions{Verb: "updating"},
				func(entry manifest.RepoEntry) (repoCommandResult, error) {
					res := repoCommandResult{entry: entry}
					if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
						res.skipped = true
						res.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
						return res, nil
					}
					for _, command := range entry.Update {
						err := runner.RunCommands(entry.LocalPath, []string{command})
						cr := commandOutcome{command: command}
						if err != nil {
							cr.failed = true
							cr.errMsg = err.Error()
							res.results = append(res.results, cr)
							break
						}
						res.results = append(res.results, cr)
					}
					return res, nil
				},
			)

			updMap := make(map[string]repoCommandResult, len(updWorkerResults))
			for _, r := range updWorkerResults {
				updMap[r.Value.entry.Repo] = r.Value
			}

			var updSucceeded, updFailed, updSkipped, updCommands int
			updRepos := make([]jsonRepoEntry, 0, len(updateTargets))

			for _, target := range updateTargets {
				res, ok := updMap[target.Repo]
				if !ok {
					continue
				}
				if res.skipped {
					updSkipped++
					updRepos = append(updRepos, jsonRepoEntry{
						Repo:   target.Repo,
						Status: "skipped",
						Reason: "not_on_disk",
					})
					continue
				}
				repoFailed := false
				var cmds []jsonCmdEntry
				for _, cr := range res.results {
					updCommands++
					e := jsonCmdEntry{Command: cr.command}
					if cr.failed {
						e.Status = "failed"
						e.Error = cr.errMsg
						repoFailed = true
					} else {
						e.Status = "ok"
					}
					cmds = append(cmds, e)
				}
				repoStatus := "ok"
				if repoFailed {
					repoStatus = "failed"
					updFailed++
				} else {
					updSucceeded++
				}
				updRepos = append(updRepos, jsonRepoEntry{
					Repo:     target.Repo,
					Status:   repoStatus,
					Commands: cmds,
				})
			}

			result.Update = &output.SubResult{
				Summary: map[string]int{
					"succeeded": updSucceeded,
					"failed":    updFailed,
					"skipped":   updSkipped,
					"total":     len(updateTargets),
					"commands":  updCommands,
				},
				Repos: updRepos,
			}
		} else {
			result.Update = &output.SubResult{
				Summary: map[string]int{"succeeded": 0, "skipped": 0, "failed": 0, "total": 0, "commands": 0},
				Repos:   []interface{}{},
			}
		}
	}

	// ── Compute exit code ────────────────────────────────────────────────────
	if upDryRun {
		result.ExitCode = 0
	} else {
		result.ExitCode = upExitCode(result)
	}

	output.PrintAndExit(result)
	return nil
}

// upExitCode returns the highest exit code across all phases of an UpResult.
func upExitCode(r output.UpResult) int {
	highest := 0

	if r.Bootstrap != nil {
		if s, ok := r.Bootstrap.Summary.(bootstrapSummaryJSON); ok {
			if s.Failed > 0 && highest < 2 {
				highest = 2
			}
		}
	}

	if r.Sync != nil {
		if s, ok := r.Sync.Summary.(syncSummaryJSON); ok {
			if s.Failed > 0 && highest < 2 {
				highest = 2
			}
			if s.Skipped > 0 && highest < 1 {
				highest = 1
			}
		}
	}

	if r.Install != nil {
		if s, ok := r.Install.Summary.(map[string]int); ok {
			if s["failed"] > 0 && highest < 2 {
				highest = 2
			}
		}
	}

	if r.Update != nil {
		if s, ok := r.Update.Summary.(map[string]int); ok {
			if s["failed"] > 0 && highest < 2 {
				highest = 2
			}
		}
	}

	return highest
}
