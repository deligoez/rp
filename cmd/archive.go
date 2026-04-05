package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/spf13/cobra"
)

var archiveThreshold int

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Report non-archive repos with stale last commits",
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := manifest.Load(ManifestPath)
		if err != nil {
			return fmt.Errorf("loading manifest: %w", err)
		}

		today := time.Now().Truncate(24 * time.Hour)

		fmt.Printf("Archive candidates (last commit >= %d days ago):\n", archiveThreshold)

		type candidate struct {
			label    string
			lastDate time.Time
			daysAgo  int
		}

		type ownerResult struct {
			name       string
			candidates []candidate
		}

		var ownerResults []ownerResult
		totalCandidates := 0

		for _, owner := range m.Owners() {
			var candidates []candidate

			for _, entry := range owner.Repos {
				// Only scan non-archive repos.
				if entry.IsArchive {
					continue
				}

				// Skip repos that don't exist on disk (silently).
				if !git.IsRepo(entry.LocalPath) {
					continue
				}

				lastDate, err := git.LastCommitDate(entry.LocalPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not get last commit date for %s: %v\n", entry.Repo, err)
					continue
				}

				lastDateTrunc := lastDate.Truncate(24 * time.Hour)
				daysAgo := int(today.Sub(lastDateTrunc).Hours() / 24)

				if daysAgo >= archiveThreshold {
					// Build label per spec section 3.0.
					repoName := entry.Repo
					if idx := strings.LastIndex(entry.Repo, "/"); idx >= 0 {
						repoName = entry.Repo[idx+1:]
					}

					var label string
					if owner.IsFlat {
						label = repoName
					} else {
						label = entry.Category + "/" + repoName
					}

					candidates = append(candidates, candidate{
						label:    label,
						lastDate: lastDateTrunc,
						daysAgo:  daysAgo,
					})
				}
			}

			if len(candidates) > 0 {
				ownerResults = append(ownerResults, ownerResult{
					name:       owner.Name,
					candidates: candidates,
				})
				totalCandidates += len(candidates)
			}
		}

		for _, or_ := range ownerResults {
			fmt.Printf("\n%s\n", or_.name)
			for _, c := range or_.candidates {
				fmt.Printf("  %-24s last commit: %s (%d days ago)\n",
					c.label,
					c.lastDate.Format("2006-01-02"),
					c.daysAgo,
				)
				fmt.Printf("    -> suggestion: move to %s/archive/ and update manifest\n", or_.name)
			}
		}

		fmt.Printf("\n%d %s could be archived\n", totalCandidates, pluralRepos(totalCandidates))

		return nil
	},
}

func pluralRepos(n int) string {
	if n == 1 {
		return "repo"
	}
	return "repos"
}

func init() {
	archiveCmd.Flags().IntVar(&archiveThreshold, "threshold", 365, "days since last commit to consider stale")
	rootCmd.AddCommand(archiveCmd)
}
