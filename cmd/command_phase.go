package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/runner"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
)

// repoCommandResult holds the outcome of running a repo's manifest commands.
// install and update differ only in which command list they read, so they
// share one result shape.
type repoCommandResult struct {
	entry   manifest.RepoEntry
	skipped bool
	skipMsg string
	results []commandOutcome
}

// commandOutcome holds the outcome of a single command within a repo.
type commandOutcome struct {
	command string
	failed  bool
	errMsg  string
}

// runRepoCommands executes one repo's commands in its directory, stopping at
// the first failure. A repo missing from disk is reported as skipped rather
// than failed: bootstrap may simply not have cloned it.
func runRepoCommands(entry manifest.RepoEntry, commands []string) repoCommandResult {
	result := repoCommandResult{entry: entry}

	if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
		result.skipped = true
		result.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
		return result
	}

	for _, command := range commands {
		outcome := commandOutcome{command: command}
		if err := runner.RunCommands(entry.LocalPath, []string{command}); err != nil {
			outcome.failed = true
			outcome.errMsg = err.Error()
			result.results = append(result.results, outcome)
			break
		}
		result.results = append(result.results, outcome)
	}

	return result
}

// commandCmdSpec describes one of the two manifest-command commands. rp
// install and rp update run the same pipeline over a different command list,
// so the pipeline lives here once and each command supplies only its wording.
type commandCmdSpec struct {
	name       string // "install" / "update"
	verb       string // progress verb, e.g. "installing"
	heading    string // human header, e.g. "Installing"
	pastTense  string // live-log word, padded so the columns line up
	dryRun     *bool
	commandsOf func(manifest.RepoEntry) []string
}

// runCommandCmd is the shared body of rp install and rp update.
func runCommandCmd(spec commandCmdSpec, args []string) error {
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit(spec.name, err)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	targets, ok := commandTargets(spec, m, args)
	if !ok {
		return nil
	}

	if *spec.dryRun {
		return printCommandDryRun(spec, m, targets)
	}
	if output.IsJSON() {
		printCommandJSON(spec, targets)
		return nil
	}
	return printCommandHuman(spec, targets)
}

// commandTargets resolves which repos to act on. A positional repo argument
// wins over --filter. ok is false when the command has already printed its
// "nothing to do" result and should stop.
func commandTargets(spec commandCmdSpec, m *manifest.Manifest, args []string) ([]manifest.RepoEntry, bool) {
	allRepos := m.Repos()

	var targets []manifest.RepoEntry
	if len(args) == 1 {
		var ok bool
		if targets, ok = commandTargetFromArg(spec, allRepos, args[0]); !ok {
			return nil, false
		}
	} else {
		targets = commandTargetsFromFilter(spec, allRepos)
	}

	if len(targets) == 0 {
		reportNoCommandWork(spec, fmt.Sprintf("no repos with %s commands defined\n", spec.name))
		return nil, false
	}

	return targets, true
}

// commandTargetFromArg resolves the positional repo argument, which takes
// precedence over --filter. An unknown repo exits 2; a known repo with no
// commands is reported as nothing to do (ok=false).
func commandTargetFromArg(spec commandCmdSpec, allRepos []manifest.RepoEntry, name string) ([]manifest.RepoEntry, bool) {
	if len(Filters) > 0 {
		fmt.Fprintf(os.Stderr, "warning: positional repo argument takes precedence over --filter; --filter ignored\n")
	}

	for _, r := range allRepos {
		if r.Repo != name {
			continue
		}
		if len(spec.commandsOf(r)) == 0 {
			reportNoCommandWork(spec, fmt.Sprintf("no %s commands configured for %s\n", spec.name, name))
			return nil, false
		}
		return []manifest.RepoEntry{r}, true
	}

	notFoundErr := output.NewHintError(
		fmt.Errorf("repo %q not found in manifest", name),
		"check repo name, available: rp list --json",
	)
	if output.IsJSON() {
		output.PrintErrorAndExit(spec.name, notFoundErr)
	}
	fmt.Fprintf(os.Stderr, "%s\n", output.FormatHumanError(notFoundErr))
	os.Exit(2)
	return nil, false
}

// commandTargetsFromFilter selects every repo that defines commands, narrowed
// by --filter.
func commandTargetsFromFilter(spec commandCmdSpec, allRepos []manifest.RepoEntry) []manifest.RepoEntry {
	var withCommands []manifest.RepoEntry
	for _, r := range allRepos {
		if len(spec.commandsOf(r)) > 0 {
			withCommands = append(withCommands, r)
		}
	}
	return manifest.FilterRepos(withCommands, Filters)
}

// reportNoCommandWork reports a run with nothing to do: JSON prints the empty
// envelope and exits, the human path prints msg and returns to the caller.
func reportNoCommandWork(spec commandCmdSpec, msg string) {
	if output.IsJSON() {
		output.PrintAndExit(output.SuccessResult{
			Command:  spec.name,
			ExitCode: 0,
			Summary: map[string]int{
				"succeeded": 0,
				"failed":    0,
				"skipped":   0,
				"total":     0,
				"commands":  0,
			},
			Repos: []interface{}{},
		})
	}
	fmt.Print(msg)
}

// printCommandDryRun lists the commands each target would run, then exits.
// dryRunTarget is one target and whether it is on disk. Kept as one ordered
// slice so the JSON repos array preserves the target order, missing repos
// included, exactly as a single pass would produce it.
type dryRunTarget struct {
	entry   manifest.RepoEntry
	missing bool
}

func printCommandDryRun(spec commandCmdSpec, m *manifest.Manifest, targets []manifest.RepoEntry) error {
	scanned := commandDryRunTargets(targets)

	if output.IsJSON() {
		printCommandDryRunJSON(spec, scanned)
	}

	printCommandDryRunHuman(spec, m, scanned)
	return nil
}

// commandDryRunTargets marks which targets are on disk, warning once on stderr
// for each one that is not. Both output modes need the same split, and the
// warning belongs to the scan rather than to either renderer.
func commandDryRunTargets(targets []manifest.RepoEntry) []dryRunTarget {
	scanned := make([]dryRunTarget, 0, len(targets))
	for _, target := range targets {
		_, err := os.Stat(target.LocalPath)
		missing := os.IsNotExist(err)
		if missing {
			fmt.Fprintf(os.Stderr, "warning: %s not found on disk, skipping\n", target.LocalPath)
		}
		scanned = append(scanned, dryRunTarget{entry: target, missing: missing})
	}
	return scanned
}

// printCommandDryRunJSON writes the dry-run result and exits.
func printCommandDryRunJSON(spec commandCmdSpec, scanned []dryRunTarget) {
	type jsonCommandEntry struct {
		Command string `json:"command"`
		Status  string `json:"status"`
	}
	type jsonRepoEntry struct {
		Repo     string             `json:"repo"`
		Status   string             `json:"status"`
		Reason   string             `json:"reason,omitempty"`
		Commands []jsonCommandEntry `json:"commands,omitempty"`
	}

	jsonRepos := make([]jsonRepoEntry, 0, len(scanned))
	repos, commands, skipped := 0, 0, 0

	for _, target := range scanned {
		if target.missing {
			skipped++
			jsonRepos = append(jsonRepos, jsonRepoEntry{
				Repo:   target.entry.Repo,
				Status: "skipped",
				Reason: "not_on_disk",
			})
			continue
		}

		repos++
		list := spec.commandsOf(target.entry)
		cmds := make([]jsonCommandEntry, 0, len(list))
		for _, command := range list {
			commands++
			cmds = append(cmds, jsonCommandEntry{Command: command, Status: "would_run"})
		}
		jsonRepos = append(jsonRepos, jsonRepoEntry{
			Repo:     target.entry.Repo,
			Status:   "ok",
			Commands: cmds,
		})
	}

	output.PrintAndExit(output.SuccessResult{
		Command:  spec.name,
		ExitCode: 0,
		DryRun:   true,
		Summary: map[string]int{
			"repos":    repos,
			"commands": commands,
			"skipped":  skipped,
		},
		Repos: jsonRepos,
	})
}

// printCommandDryRunHuman lists what each on-disk target would run, grouped by
// owner in manifest order.
func printCommandDryRunHuman(spec commandCmdSpec, m *manifest.Manifest, scanned []dryRunTarget) {
	inTargets := make(map[string]bool, len(scanned))
	present := 0
	for _, t := range scanned {
		if t.missing {
			continue
		}
		inTargets[t.entry.Repo] = true
		present++
	}

	commands := 0
	for _, ownerGroup := range m.Owners() {
		ownerPrinted := false
		for _, entry := range ownerGroup.Repos {
			if !inTargets[entry.Repo] {
				continue
			}
			if !ownerPrinted {
				fmt.Println(ownerGroup.Name)
				ownerPrinted = true
			}
			paddedLabel := ui.PadRight(repoLabel(entry), 24)
			for _, command := range spec.commandsOf(entry) {
				commands++
				fmt.Printf("  %s would run: %s\n", paddedLabel, command)
			}
		}
	}

	fmt.Println()
	fmt.Printf("-- Summary --\n%s, %s\n",
		ui.Plural(present, "repo"),
		ui.Plural(commands, "command"),
	)
}

// printCommandJSON runs every target's commands and prints the ordered JSON
// result, then exits.
func printCommandJSON(spec commandCmdSpec, targets []manifest.RepoEntry) {
	succeeded := 0
	failed := 0
	skipped := 0
	totalCommands := 0
	anyFailed := false
	opts := worker.PoolOptions{Verb: spec.verb}
	results := worker.PoolWithProgress(targets, Concurrency, opts, func(entry manifest.RepoEntry) (repoCommandResult, error) {
		return runRepoCommands(entry, spec.commandsOf(entry)), nil
	})

	resultMap := make(map[string]repoCommandResult, len(results))
	for _, r := range results {
		resultMap[r.Value.entry.Repo] = r.Value
	}

	type jsonCommandEntry struct {
		Command string `json:"command"`
		Status  string `json:"status"`
		Error   string `json:"error,omitempty"`
	}
	type jsonRepoEntry struct {
		Repo     string             `json:"repo"`
		Status   string             `json:"status"`
		Reason   string             `json:"reason,omitempty"`
		Commands []jsonCommandEntry `json:"commands,omitempty"`
	}

	jsonRepos := make([]jsonRepoEntry, 0, len(targets))

	for _, target := range targets {
		res, ok := resultMap[target.Repo]
		if !ok {
			continue
		}

		if res.skipped {
			skipped++
			jsonRepos = append(jsonRepos, jsonRepoEntry{
				Repo:   target.Repo,
				Status: "skipped",
				Reason: "not_on_disk",
			})
			continue
		}

		repoFailed := false
		var cmds []jsonCommandEntry
		for _, cr := range res.results {
			entry := jsonCommandEntry{Command: cr.command}
			totalCommands++
			if cr.failed {
				entry.Status = "failed"
				entry.Error = cr.errMsg
				repoFailed = true
			} else {
				entry.Status = "ok"
			}
			cmds = append(cmds, entry)
		}

		repoStatus := "ok"
		if repoFailed {
			repoStatus = "failed"
			failed++
			anyFailed = true
		} else {
			succeeded++
		}

		jsonRepos = append(jsonRepos, jsonRepoEntry{
			Repo:     target.Repo,
			Status:   repoStatus,
			Commands: cmds,
		})
	}

	exitCode := 0
	if skipped > 0 {
		exitCode = 1
	}
	if anyFailed {
		exitCode = 2
	}

	output.PrintAndExit(output.SuccessResult{
		Command:  spec.name,
		ExitCode: exitCode,
		Summary: map[string]int{
			"succeeded": succeeded,
			"failed":    failed,
			"skipped":   skipped,
			"total":     len(targets),
			"commands":  totalCommands,
		},
		Repos: jsonRepos,
	})
}

// printCommandHuman runs every target's commands, streaming one line per repo
// as each worker finishes, then prints the summary.
func printCommandHuman(spec commandCmdSpec, targets []manifest.RepoEntry) error {
	succeeded := 0
	failed := 0
	skipped := 0
	totalCommands := 0
	anyFailed := false
	// Human path: stream per-repo lines as each worker finishes.
	fmt.Printf(spec.heading+" %s (concurrency: %d)...\n\n",
		ui.Plural(len(targets), "repo"), Concurrency)

	_ = worker.PoolWithLiveLog(
		targets,
		Concurrency,
		func(entry manifest.RepoEntry) (repoCommandResult, error) {
			return runRepoCommands(entry, spec.commandsOf(entry)), nil
		},
		func(n, total int, entry manifest.RepoEntry, res repoCommandResult, _ error) {
			label := ui.PadRight(repoLabel(entry), 24)
			if res.skipped {
				skipped++
				fmt.Printf("[%d/%d] -- skipped    %s (not on disk)\n", n, total, label)
				return
			}
			repoFailed := false
			var failedCmd commandOutcome
			for _, cr := range res.results {
				totalCommands++
				if cr.failed {
					repoFailed = true
					failedCmd = cr
				}
			}
			if repoFailed {
				failed++
				anyFailed = true
				fmt.Printf("[%d/%d] %s FAILED     %s: %s (%s)\n",
					n, total, ui.SymbolError(), label, failedCmd.command, failedCmd.errMsg)
				return
			}
			if len(res.results) > 0 {
				succeeded++
				fmt.Printf("[%d/%d] %s "+spec.pastTense+"%s (%s)\n",
					n, total, ui.SymbolOK(), label, ui.Plural(len(res.results), "command"))
			}
		},
	)

	// Summary line.
	fmt.Println()
	fmt.Println(ui.SummaryLine(
		fmt.Sprintf("%s, %s succeeded, %d skipped, %d failed",
			ui.Plural(len(targets), "repo"),
			ui.Plural(totalCommands, "command"),
			skipped,
			failed,
		),
	))

	exitCode := 0
	if skipped > 0 {
		exitCode = 1
	}
	if anyFailed {
		exitCode = 2
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}

	return nil
}
