package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/deligoez/rp/internal/deps"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
)

var (
	upDryRun bool
	upNoDeps bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bootstrap, sync, and install deps for all repos in one pass",
	RunE:  runUp,
}

func init() {
	upCmd.Flags().BoolVar(&upDryRun, "dry-run", false, "preview bootstrap and sync without making changes; skip deps")
	upCmd.Flags().BoolVar(&upNoDeps, "no-deps", false, "skip dependency installation phase")
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

	// ── Phase 1: Bootstrap ───────────────────────────────────────────────────
	fmt.Println("== Bootstrap ==")

	bootstrapResults := worker.PoolWithProgress(
		repos,
		Concurrency,
		worker.PoolOptions{Verb: "cloning"},
		func(entry manifest.RepoEntry) (bootstrapResult, error) {
			return processBootstrapEntry(entry), nil
		},
	)

	// Build lookup map and track cloned paths.
	bootstrapMap := make(map[string]bootstrapResult, len(bootstrapResults))
	clonedPaths := make(map[string]bool)
	for _, r := range bootstrapResults {
		bootstrapMap[r.Value.Entry.LocalPath] = r.Value
		if r.Value.Status == bsCloned {
			clonedPaths[r.Value.Entry.LocalPath] = true
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

	// ── Phase 2: Sync ────────────────────────────────────────────────────────
	fmt.Println("== Sync ==")

	syncResults := worker.PoolWithProgress(
		repos,
		Concurrency,
		worker.PoolOptions{Verb: "syncing"},
		func(entry manifest.RepoEntry) (syncResult, error) {
			// Repos just cloned in phase 1 are treated as up-to-date.
			if clonedPaths[entry.LocalPath] {
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

	// ── Phase 3: Deps ────────────────────────────────────────────────────────
	var depSucceeded, depFailed int

	runDepsPhase := !upDryRun && !upNoDeps

	if runDepsPhase {
		fmt.Println("== Deps ==")

		var depsTargets []manifest.RepoEntry
		for _, r := range repos {
			if len(r.Deps) > 0 {
				depsTargets = append(depsTargets, r)
			}
		}

		if len(depsTargets) > 0 {
			depsResults := worker.PoolWithProgress(
				depsTargets,
				Concurrency,
				worker.PoolOptions{Verb: "installing"},
				func(entry manifest.RepoEntry) (depsRepoResult, error) {
					result := depsRepoResult{entry: entry}
					if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
						result.skipped = true
						result.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
						return result, nil
					}
					for _, command := range entry.Deps {
						err := deps.RunDeps(entry.LocalPath, []string{command})
						cr := depsCommandResult{command: command}
						if err != nil {
							cr.failed = true
							cr.errMsg = err.Error()
							result.results = append(result.results, cr)
							break
						}
						result.results = append(result.results, cr)
					}
					return result, nil
				},
			)

			depsMap := make(map[string]depsRepoResult, len(depsResults))
			for _, r := range depsResults {
				depsMap[r.Value.entry.Repo] = r.Value
			}

			for _, ownerGroup := range owners {
				ownerPrinted := false
				for _, entry := range ownerGroup.Repos {
					res, ok := depsMap[entry.Repo]
					if !ok {
						continue
					}
					label := repoLabel(entry)
					paddedLabel := ui.PadRight(label, 24)
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
						depFailed++
					} else if len(res.results) > 0 {
						depSucceeded++
					}
				}
			}
			fmt.Println()
		}
	}

	// ── Summary ──────────────────────────────────────────────────────────────
	fmt.Println("-- Summary --")
	fmt.Printf("%s, %s, %s\n",
		fmt.Sprintf("%d cloned", bsClonedCount),
		pluralExisted(bsExistedCount),
		fmt.Sprintf("%d failed", bsFailedCount),
	)
	fmt.Printf("%s pulled, %d up to date, %d skipped\n", fmt.Sprintf("%d", syPulled), syUpToDate, sySkipped)

	if runDepsPhase {
		fmt.Printf("%s succeeded, %d failed\n", fmt.Sprintf("%d deps", depSucceeded), depFailed)
	}

	if upDryRun {
		os.Exit(0)
	}

	// Exit with highest code across phases.
	exitCode := 0
	if bsFailedCount > 0 {
		exitCode = 2
	}
	if syFailed > 0 && exitCode < 2 {
		exitCode = 2
	}
	if sySkipped > 0 && exitCode < 1 {
		exitCode = 1
	}
	if depFailed > 0 && exitCode < 2 {
		exitCode = 2
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
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

	clonedPaths := make(map[string]bool)
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
				clonedPaths[res.Entry.LocalPath] = true
			case bsAlreadyExists:
				nExisted++
				rj.Action = "already_exists"
			case bsFailed:
				nFailed++
				rj.Action = "failed"
			case bsWouldClone:
				nCloned++
				rj.Action = "would_clone"
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
			if clonedPaths[entry.LocalPath] {
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

	// ── Phase 3: Deps ────────────────────────────────────────────────────────
	runDepsPhase := !upDryRun && !upNoDeps

	if runDepsPhase {
		var depsTargets []manifest.RepoEntry
		for _, r := range repos {
			if len(r.Deps) > 0 {
				depsTargets = append(depsTargets, r)
			}
		}

		depsWorkerResults := worker.PoolWithProgress(
			depsTargets,
			Concurrency,
			worker.PoolOptions{Verb: "installing"},
			func(entry manifest.RepoEntry) (depsRepoResult, error) {
				res := depsRepoResult{entry: entry}
				if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
					res.skipped = true
					res.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
					return res, nil
				}
				for _, command := range entry.Deps {
					err := deps.RunDeps(entry.LocalPath, []string{command})
					cr := depsCommandResult{command: command}
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

		type jsonCmdEntry struct {
			Command string `json:"command"`
			Status  string `json:"status"`
			Error   string `json:"error,omitempty"`
		}
		type jsonRepoEntry struct {
			Repo     string        `json:"repo"`
			Status   string        `json:"status"`
			Reason   string        `json:"reason,omitempty"`
			Commands []jsonCmdEntry `json:"commands,omitempty"`
		}

		depsMap := make(map[string]depsRepoResult, len(depsWorkerResults))
		for _, r := range depsWorkerResults {
			depsMap[r.Value.entry.Repo] = r.Value
		}

		var depSucceeded, depFailed, depSkipped int
		depsRepos := make([]jsonRepoEntry, 0, len(depsTargets))

		for _, target := range depsTargets {
			res, ok := depsMap[target.Repo]
			if !ok {
				continue
			}
			if res.skipped {
				depSkipped++
				depsRepos = append(depsRepos, jsonRepoEntry{
					Repo:   target.Repo,
					Status: "skipped",
					Reason: "not_on_disk",
				})
				continue
			}
			repoFailed := false
			var cmds []jsonCmdEntry
			for _, cr := range res.results {
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
				depFailed++
			} else {
				depSucceeded++
			}
			depsRepos = append(depsRepos, jsonRepoEntry{
				Repo:     target.Repo,
				Status:   repoStatus,
				Commands: cmds,
			})
		}

		result.Deps = &output.SubResult{
			Summary: map[string]int{
				"succeeded": depSucceeded,
				"failed":    depFailed,
				"skipped":   depSkipped,
				"total":     len(depsTargets),
			},
			Repos: depsRepos,
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

	if r.Deps != nil {
		if s, ok := r.Deps.Summary.(map[string]int); ok {
			if s["failed"] > 0 && highest < 2 {
				highest = 2
			}
		}
	}

	return highest
}
