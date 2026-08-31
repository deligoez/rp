package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
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
		return runUpJSON(repos)
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
			if !ownerPrinted {
				fmt.Println(ownerGroup.Name)
				ownerPrinted = true
			}
			printUpRepoResult(&res, ui.PadRight(repoLabel(entry), 24), counts)
		}
	}
}

// printUpRepoResult prints one repo's command outcomes and folds them into the
// phase counts. A repo counts once: failed if any command failed, succeeded if
// it ran at least one and none failed.
func printUpRepoResult(res *repoCommandResult, paddedLabel string, counts *upCommandCounts) {
	if res.skipped {
		fmt.Fprintf(os.Stderr, "  %s\n", res.skipMsg)
		return
	}

	repoFailed := false
	for _, cr := range res.results {
		if cr.failed {
			fmt.Printf("  %s FAILED: %s (%s)\n", paddedLabel, cr.command, cr.errMsg)
			repoFailed = true
			continue
		}
		fmt.Printf("  %s %s %s\n", paddedLabel, ui.SymbolOK(), cr.command)
	}

	switch {
	case repoFailed:
		counts.failed++
	case len(res.results) > 0:
		counts.succeeded++
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

// upExitTally accumulates what up's exit code depends on. Each phase reports
// its own numbers so the code is derived from them directly, in both output
// modes, rather than reconstructed from the assembled result.
//
// Only sync contributes skipped: a repo sync passed over still needs a look,
// while an install skipped because the repo is not on disk is not an error.
type upExitTally struct {
	failed  int
	skipped int
}

func (t *upExitTally) add(o upExitTally) {
	t.failed += o.failed
	t.skipped += o.skipped
}

// code is 2 when any phase failed, 1 when sync skipped a repo, else 0.
func (t upExitTally) code() int {
	switch {
	case t.failed > 0:
		return 2
	case t.skipped > 0:
		return 1
	default:
		return 0
	}
}

// upHumanExitCode is the highest exit code across the four phases.
func upHumanExitCode(boot upBootstrapOutcome, sync upSyncCounts, inst, upd upCommandCounts) int {
	return upExitTally{
		failed:  boot.failed + sync.failed + inst.failed + upd.failed,
		skipped: sync.skipped,
	}.code()
}

// ── JSON mode ────────────────────────────────────────────────────────────────

func runUpJSON(repos []manifest.RepoEntry) error {
	result := output.UpResult{
		Command: "up",
		DryRun:  upDryRun,
	}

	var tally upExitTally

	clonedSet, failedSet, bootTally := upJSONBootstrap(&result, repos)
	tally.add(bootTally)
	tally.add(upJSONSync(&result, repos, clonedSet))

	install, installTally := upJSONCommandPhase(upCommandPhase{
		verb:       "installing",
		enabled:    !upNoInstall,
		commandsOf: func(r manifest.RepoEntry) []string { return r.Install },
		isTarget:   func(r manifest.RepoEntry) bool { return clonedSet[r.Repo] },
	}, repos)
	result.Install = install
	tally.add(installTally)

	update, updateTally := upJSONCommandPhase(upCommandPhase{
		verb:       "updating",
		enabled:    !upNoUpdate,
		commandsOf: func(r manifest.RepoEntry) []string { return r.Update },
		isTarget:   func(r manifest.RepoEntry) bool { return !clonedSet[r.Repo] && !failedSet[r.Repo] },
	}, repos)
	result.Update = update
	tally.add(updateTally)

	// ── Compute exit code ────────────────────────────────────────────────────
	if !upDryRun {
		result.ExitCode = tally.code()
	}

	output.PrintAndExit(result)
	return nil
}

// upJSONBootstrap fills result.Bootstrap and reports which repos were cloned
// and which failed, for the phases that follow.
func upJSONBootstrap(result *output.UpResult, repos []manifest.RepoEntry) (clonedSet, failedSet map[string]bool, tally upExitTally) {
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

	clonedSet = make(map[string]bool)
	failedSet = make(map[string]bool)
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
		tally.failed = nFailed
	}

	return clonedSet, failedSet, tally
}

// upJSONSync fills result.Sync.
func upJSONSync(result *output.UpResult, repos []manifest.RepoEntry, clonedSet map[string]bool) (tally upExitTally) {
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
		tally.failed = summary.Failed
		tally.skipped = summary.Skipped
	}

	return tally
}

// upJSONCmdEntry is one command's outcome in the up JSON result.
type upJSONCmdEntry struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// upJSONRepoEntry is one repo's outcome in a command phase.
type upJSONRepoEntry struct {
	Repo     string           `json:"repo"`
	Status   string           `json:"status"`
	Reason   string           `json:"reason,omitempty"`
	Commands []upJSONCmdEntry `json:"commands,omitempty"`
}

// emptyUpCommandResult is the sub-result for a phase with nothing to do.
func emptyUpCommandResult() *output.SubResult {
	return &output.SubResult{
		Summary: map[string]int{"succeeded": 0, "skipped": 0, "failed": 0, "total": 0, "commands": 0},
		Repos:   []interface{}{},
	}
}

// upJSONCommandPhase runs one command phase and returns its sub-result. A
// phase turned off with --no-install / --no-update returns nil, which
// serializes as null.
func upJSONCommandPhase(phase upCommandPhase, repos []manifest.RepoEntry) (*output.SubResult, upExitTally) {
	if !phase.enabled {
		return nil, upExitTally{}
	}

	var targets []manifest.RepoEntry
	for _, r := range repos {
		if len(phase.commandsOf(r)) > 0 && phase.isTarget(r) {
			targets = append(targets, r)
		}
	}
	if len(targets) == 0 {
		return emptyUpCommandResult(), upExitTally{}
	}

	if upDryRun {
		return upJSONCommandDryRun(phase, targets), upExitTally{}
	}
	return upJSONCommandRun(phase, targets)
}

// upJSONCommandDryRun reports what each target would run.
func upJSONCommandDryRun(phase upCommandPhase, targets []manifest.RepoEntry) *output.SubResult {
	var dryRepos, dryCommands, drySkipped int
	entries := make([]upJSONRepoEntry, 0, len(targets))

	for _, target := range targets {
		if _, err := os.Stat(target.LocalPath); os.IsNotExist(err) {
			drySkipped++
			entries = append(entries, upJSONRepoEntry{
				Repo:   target.Repo,
				Status: "skipped",
				Reason: "not_on_disk",
			})
			continue
		}
		commands := phase.commandsOf(target)
		cmds := make([]upJSONCmdEntry, 0, len(commands))
		for _, command := range commands {
			dryCommands++
			cmds = append(cmds, upJSONCmdEntry{Command: command, Status: "would_run"})
		}
		entries = append(entries, upJSONRepoEntry{
			Repo:     target.Repo,
			Status:   "ok",
			Commands: cmds,
		})
		dryRepos++
	}

	return &output.SubResult{
		Summary: map[string]int{
			"repos":    dryRepos,
			"commands": dryCommands,
			"skipped":  drySkipped,
		},
		Repos: entries,
	}
}

// upJSONCommandRun executes each target's commands and reports the outcome in
// manifest order.
func upJSONCommandRun(phase upCommandPhase, targets []manifest.RepoEntry) (*output.SubResult, upExitTally) {
	workerResults := worker.PoolWithProgress(
		targets,
		Concurrency,
		worker.PoolOptions{Verb: phase.verb},
		func(entry manifest.RepoEntry) (repoCommandResult, error) {
			return runRepoCommands(entry, phase.commandsOf(entry)), nil
		},
	)

	resultMap := make(map[string]repoCommandResult, len(workerResults))
	for _, r := range workerResults {
		resultMap[r.Value.entry.Repo] = r.Value
	}

	var succeeded, failed, skipped, commandCount int
	entries := make([]upJSONRepoEntry, 0, len(targets))

	for _, target := range targets {
		res, ok := resultMap[target.Repo]
		if !ok {
			continue
		}
		if res.skipped {
			skipped++
			entries = append(entries, upJSONRepoEntry{
				Repo:   target.Repo,
				Status: "skipped",
				Reason: "not_on_disk",
			})
			continue
		}

		repoFailed := false
		var cmds []upJSONCmdEntry
		for _, cr := range res.results {
			commandCount++
			e := upJSONCmdEntry{Command: cr.command}
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
			failed++
		} else {
			succeeded++
		}
		entries = append(entries, upJSONRepoEntry{
			Repo:     target.Repo,
			Status:   repoStatus,
			Commands: cmds,
		})
	}

	return &output.SubResult{
		Summary: map[string]int{
			"succeeded": succeeded,
			"failed":    failed,
			"skipped":   skipped,
			"total":     len(targets),
			"commands":  commandCount,
		},
		Repos: entries,
	}, upExitTally{failed: failed}
}
