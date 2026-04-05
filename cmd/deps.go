package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/deps"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
	"github.com/spf13/cobra"
)

// depsRepoResult holds the outcome of running deps for a single repo.
type depsRepoResult struct {
	entry    manifest.RepoEntry
	skipped  bool
	skipMsg  string
	results  []depsCommandResult
}

// depsCommandResult holds the outcome of a single command within a repo.
type depsCommandResult struct {
	command string
	failed  bool
	errMsg  string
}

var depsCmd = &cobra.Command{
	Use:   "deps [repo]",
	Short: "Run dependency install commands defined in the manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.SetNoColor(NoColor)

		m, err := manifest.Load(ManifestPath)
		if err != nil {
			if output.IsJSON() {
				output.PrintErrorAndExit("deps", err)
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
					if len(r.Deps) == 0 {
						if output.IsJSON() {
							output.PrintAndExit(output.SuccessResult{
								Command:  "deps",
								ExitCode: 0,
								Summary: map[string]int{
									"succeeded": 0,
									"failed":    0,
									"skipped":   0,
									"total":     0,
								},
								Repos: []interface{}{},
							})
						}
						fmt.Printf("no deps configured for %s\n", filter)
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
					output.PrintErrorAndExit("deps", notFoundErr)
				}
				fmt.Fprintf(os.Stderr, "%s\n", output.FormatHumanError(notFoundErr))
				os.Exit(2)
			}
		} else {
			// No positional arg: collect all repos that have deps defined, then apply --filter.
			var withDeps []manifest.RepoEntry
			for _, r := range allRepos {
				if len(r.Deps) > 0 {
					withDeps = append(withDeps, r)
				}
			}
			targets = manifest.FilterRepos(withDeps, Filters)
		}

		if len(targets) == 0 {
			if output.IsJSON() {
				output.PrintAndExit(output.SuccessResult{
					Command:  "deps",
					ExitCode: 0,
					Summary: map[string]int{
						"succeeded": 0,
						"failed":    0,
						"skipped":   0,
						"total":     0,
					},
					Repos: []interface{}{},
				})
			}
			fmt.Println("no repos with deps defined")
			return nil
		}

		// Run deps for each target repo via worker pool.
		opts := worker.PoolOptions{Verb: "installing"}
		results := worker.PoolWithProgress(targets, Concurrency, opts, func(entry manifest.RepoEntry) (depsRepoResult, error) {
			result := depsRepoResult{entry: entry}

			// Check if repo exists on disk.
			if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
				result.skipped = true
				result.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
				return result, nil
			}

			// Run each dep command sequentially, stopping on first failure.
			for _, command := range entry.Deps {
				err := deps.RunDeps(entry.LocalPath, []string{command})
				cr := depsCommandResult{command: command}
				if err != nil {
					cr.failed = true
					cr.errMsg = err.Error()
					result.results = append(result.results, cr)
					// Stop on first failure per spec.
					break
				}
				result.results = append(result.results, cr)
			}

			return result, nil
		})

		// Build a map from repo -> result for ordered display.
		resultMap := make(map[string]depsRepoResult, len(results))
		for _, r := range results {
			resultMap[r.Value.entry.Repo] = r.Value
		}

		succeeded := 0
		failed := 0
		skipped := 0
		anyFailed := false

		// JSON output path.
		if output.IsJSON() {
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
			if anyFailed {
				exitCode = 2
			}

			output.PrintAndExit(output.SuccessResult{
				Command:  "deps",
				ExitCode: exitCode,
				Summary: map[string]int{
					"succeeded": succeeded,
					"failed":    failed,
					"skipped":   skipped,
					"total":     len(targets),
				},
				Repos: jsonRepos,
			})
		}

		// Human output path.
		for _, ownerGroup := range m.Owners() {
			// Print repos in this owner that are in our targets, preserving manifest order.
			ownerPrinted := false

			for _, entry := range ownerGroup.Repos {
				res, ok := resultMap[entry.Repo]
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
					skipped++
					continue
				}

				// Print a line per command result.
				repoFailed := false
				for _, cr := range res.results {
					if cr.failed {
						fmt.Printf("  %s FAILED: %s (%s)\n",
							paddedLabel,
							cr.command,
							cr.errMsg,
						)
						repoFailed = true
						anyFailed = true
					} else {
						fmt.Printf("  %s %s %s\n",
							paddedLabel,
							ui.SymbolOK(),
							cr.command,
						)
					}
				}

				if repoFailed {
					failed++
				} else if len(res.results) > 0 {
					succeeded++
				}
			}
		}

		// Summary line.
		fmt.Println()
		fmt.Println(ui.SummaryLine(
			fmt.Sprintf("%s succeeded, %d failed",
				ui.Plural(succeeded, "repo"),
				failed,
			),
		))

		if anyFailed {
			os.Exit(2)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(depsCmd)
}
