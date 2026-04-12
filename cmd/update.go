package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/runner"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
	"github.com/spf13/cobra"
)

// updateRepoResult holds the outcome of running update for a single repo.
type updateRepoResult struct {
	entry   manifest.RepoEntry
	skipped bool
	skipMsg string
	results []updateCommandResult
}

// updateCommandResult holds the outcome of a single command within a repo.
type updateCommandResult struct {
	command string
	failed  bool
	errMsg  string
}

var updateDryRun bool

var updateCmd = &cobra.Command{
	Use:   "update [repo]",
	Short: "Run update commands defined in the manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.SetNoColor(NoColor)

		m, err := manifest.Load(ManifestPath)
		if err != nil {
			if output.IsJSON() {
				output.PrintErrorAndExit("update", err)
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}

		allRepos := m.Repos()

		// Determine target repos based on optional filter argument.
		var targets []manifest.RepoEntry

		if len(args) == 1 {
			// Positional argument takes precedence over --filter; warn if both provided.
			if len(Filters) > 0 {
				fmt.Fprintf(os.Stderr, "warning: positional repo argument takes precedence over --filter; --filter ignored\n")
			}
			filter := args[0]
			found := false
			for _, r := range allRepos {
				if r.Repo == filter {
					found = true
					if len(r.Update) == 0 {
						if output.IsJSON() {
							output.PrintAndExit(output.SuccessResult{
								Command:  "update",
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
						fmt.Printf("no update commands configured for %s\n", filter)
						return nil
					}
					targets = append(targets, r)
					break
				}
			}
			if !found {
				notFoundErr := output.NewHintError(
					fmt.Errorf("repo %q not found in manifest", filter),
					"check repo name, available: rp list --json",
				)
				if output.IsJSON() {
					output.PrintErrorAndExit("update", notFoundErr)
				}
				fmt.Fprintf(os.Stderr, "%s\n", output.FormatHumanError(notFoundErr))
				os.Exit(2)
			}
		} else {
			// No positional arg: collect all repos that have update defined, then apply --filter.
			var withUpdate []manifest.RepoEntry
			for _, r := range allRepos {
				if len(r.Update) > 0 {
					withUpdate = append(withUpdate, r)
				}
			}
			targets = manifest.FilterRepos(withUpdate, Filters)
		}

		if len(targets) == 0 {
			if output.IsJSON() {
				output.PrintAndExit(output.SuccessResult{
					Command:  "update",
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
			fmt.Println("no repos with update commands defined")
			return nil
		}

		// --dry-run: list commands that would run without executing them.
		if updateDryRun {
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

			jsonRepos := make([]jsonRepoEntry, 0, len(targets))
			dryRepos := 0
			dryCommands := 0
			drySkipped := 0

			for _, target := range targets {
				if _, err := os.Stat(target.LocalPath); os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "warning: %s not found on disk, skipping\n", target.LocalPath)
					drySkipped++
					if output.IsJSON() {
						jsonRepos = append(jsonRepos, jsonRepoEntry{
							Repo:   target.Repo,
							Status: "skipped",
							Reason: "not_on_disk",
						})
					}
					continue
				}

				dryRepos++
				if output.IsJSON() {
					cmds := make([]jsonCommandEntry, 0, len(target.Update))
					for _, command := range target.Update {
						dryCommands++
						cmds = append(cmds, jsonCommandEntry{Command: command, Status: "would_run"})
					}
					jsonRepos = append(jsonRepos, jsonRepoEntry{
						Repo:     target.Repo,
						Status:   "ok",
						Commands: cmds,
					})
				}
			}

			if output.IsJSON() {
				output.PrintAndExit(output.SuccessResult{
					Command:  "update",
					ExitCode: 0,
					DryRun:   true,
					Summary: map[string]int{
						"repos":    dryRepos,
						"commands": dryCommands,
						"skipped":  drySkipped,
					},
					Repos: jsonRepos,
				})
			}

			// Human dry-run output: group by owner, same as normal output.
			for _, ownerGroup := range m.Owners() {
				ownerPrinted := false
				for _, entry := range ownerGroup.Repos {
					// Only print targets.
					inTargets := false
					for _, t := range targets {
						if t.Repo == entry.Repo {
							inTargets = true
							break
						}
					}
					if !inTargets {
						continue
					}

					if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
						// Already printed warning above; skip here.
						continue
					}

					if !ownerPrinted {
						fmt.Println(ownerGroup.Name)
						ownerPrinted = true
					}

					label := repoLabel(entry)
					paddedLabel := ui.PadRight(label, 24)
					for _, command := range entry.Update {
						dryCommands++
						fmt.Printf("  %s would run: %s\n", paddedLabel, command)
					}
				}
			}

			fmt.Println()
			fmt.Printf("-- Summary --\n%s, %s\n",
				ui.Plural(dryRepos, "repo"),
				ui.Plural(dryCommands, "command"),
			)
			return nil
		}

		succeeded := 0
		failed := 0
		skipped := 0
		totalCommands := 0
		anyFailed := false

		// JSON path: preserve ordered results (no live log).
		if output.IsJSON() {
			opts := worker.PoolOptions{Verb: "updating"}
			results := worker.PoolWithProgress(targets, Concurrency, opts, func(entry manifest.RepoEntry) (updateRepoResult, error) {
				result := updateRepoResult{entry: entry}
				if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
					result.skipped = true
					result.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
					return result, nil
				}
				for _, command := range entry.Update {
					err := runner.RunCommands(entry.LocalPath, []string{command})
					cr := updateCommandResult{command: command}
					if err != nil {
						cr.failed = true
						cr.errMsg = err.Error()
						result.results = append(result.results, cr)
						break
					}
					result.results = append(result.results, cr)
				}
				return result, nil
			})

			resultMap := make(map[string]updateRepoResult, len(results))
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
				Command:  "update",
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

		// Human path: stream per-repo lines as each worker finishes.
		fmt.Printf("Updating %s (concurrency: %d)...\n\n",
			ui.Plural(len(targets), "repo"), Concurrency)

		_ = worker.PoolWithLiveLog(
			targets,
			Concurrency,
			func(entry manifest.RepoEntry) (updateRepoResult, error) {
				result := updateRepoResult{entry: entry}
				if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
					result.skipped = true
					result.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
					return result, nil
				}
				for _, command := range entry.Update {
					err := runner.RunCommands(entry.LocalPath, []string{command})
					cr := updateCommandResult{command: command}
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
			func(n, total int, entry manifest.RepoEntry, res updateRepoResult, _ error) {
				label := ui.PadRight(repoLabel(entry), 24)
				if res.skipped {
					skipped++
					fmt.Printf("[%d/%d] -- skipped    %s (not on disk)\n", n, total, label)
					return
				}
				repoFailed := false
				var failedCmd updateCommandResult
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
					fmt.Printf("[%d/%d] %s updated    %s (%s)\n",
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
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "preview update commands without executing them")
}
