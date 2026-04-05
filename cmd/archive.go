package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/spf13/cobra"
)

var archiveThreshold int

// archiveCandidate holds all data collected for a single archive candidate repo.
type archiveCandidate struct {
	entry    manifest.RepoEntry
	owner    string
	label    string
	lastDate time.Time
	daysAgo  int
}

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Report non-archive repos with stale last commits",
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := manifest.Load(ManifestPath)
		if err != nil {
			return fmt.Errorf("loading manifest: %w", err)
		}

		today := time.Now().Truncate(24 * time.Hour)

		// Apply --filter to the full repo list before scanning.
		allRepos := manifest.FilterRepos(m.Repos(), Filters)

		// Build a lookup from repo name -> owner group for label construction.
		ownerByRepo := make(map[string]manifest.OwnerGroup)
		for _, og := range m.Owners() {
			for _, entry := range og.Repos {
				ownerByRepo[entry.Repo] = og
			}
		}

		var candidates []archiveCandidate

		for _, entry := range allRepos {
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

				og := ownerByRepo[entry.Repo]
				var label string
				if og.IsFlat {
					label = repoName
				} else {
					label = entry.Category + "/" + repoName
				}

				candidates = append(candidates, archiveCandidate{
					entry:    entry,
					owner:    og.Name,
					label:    label,
					lastDate: lastDateTrunc,
					daysAgo:  daysAgo,
				})
			}
		}

		// JSON output path.
		if output.IsJSON() {
			type jsonRepo struct {
				Repo       string `json:"repo"`
				Owner      string `json:"owner"`
				Category   string `json:"category"`
				LastCommit string `json:"last_commit"`
				DaysAgo    int    `json:"days_ago"`
				Suggestion string `json:"suggestion"`
			}

			jsonRepos := make([]jsonRepo, 0, len(candidates))
			for _, c := range candidates {
				jsonRepos = append(jsonRepos, jsonRepo{
					Repo:       c.entry.Repo,
					Owner:      c.owner,
					Category:   c.entry.Category,
					LastCommit: c.lastDate.UTC().Format(time.RFC3339),
					DaysAgo:    c.daysAgo,
					Suggestion: fmt.Sprintf("move to %s/archive/ and update manifest", c.owner),
				})
			}

			output.PrintAndExit(output.SuccessResult{
				Command:  "archive",
				ExitCode: 0,
				Summary: map[string]int{
					"candidates":     len(candidates),
					"threshold_days": archiveThreshold,
				},
				Repos: jsonRepos,
			})
		}

		// Human output path.
		fmt.Printf("Archive candidates (last commit >= %d days ago):\n\n", archiveThreshold)

		// Group candidates by owner for display.
		type ownerResult struct {
			name       string
			candidates []archiveCandidate
		}
		var ownerResults []ownerResult
		ownerIdx := make(map[string]int)

		for _, c := range candidates {
			if idx, ok := ownerIdx[c.owner]; ok {
				ownerResults[idx].candidates = append(ownerResults[idx].candidates, c)
			} else {
				ownerIdx[c.owner] = len(ownerResults)
				ownerResults = append(ownerResults, ownerResult{
					name:       c.owner,
					candidates: []archiveCandidate{c},
				})
			}
		}

		for i, or_ := range ownerResults {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(or_.name)
			for _, c := range or_.candidates {
				fmt.Printf("  %-24s last commit: %s (%d days ago)\n",
					c.label,
					c.lastDate.Format("2006-01-02"),
					c.daysAgo,
				)
				fmt.Printf("    -> suggestion: move to %s/archive/ and update manifest\n", c.owner)
			}
		}

		fmt.Printf("\n%d %s could be archived\n", len(candidates), pluralRepos(len(candidates)))

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
