package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/spf13/cobra"
)

var (
	statusDirty  bool
	statusBehind bool
	statusAhead  bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of every repo in the manifest",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)

	statusCmd.Flags().BoolVar(&statusDirty, "dirty", false, "show only dirty repos")
	statusCmd.Flags().BoolVar(&statusBehind, "behind", false, "show only repos behind remote")
	statusCmd.Flags().BoolVar(&statusAhead, "ahead", false, "show only repos with unpushed commits")
}

// statusRepoJSON is the per-repo object emitted in JSON mode.
type statusRepoJSON struct {
	Repo        string `json:"repo"`
	Owner       string `json:"owner"`
	Category    string `json:"category"`
	LocalPath   string `json:"local_path"`
	Cloned      bool   `json:"cloned"`
	Branch      string `json:"branch,omitempty"`
	Clean       *bool  `json:"clean,omitempty"`
	DirtyFiles  *int   `json:"dirty_files,omitempty"`
	Ahead       *int   `json:"ahead,omitempty"`
	Behind      *int   `json:"behind,omitempty"`
	HasUpstream *bool  `json:"has_upstream,omitempty"`
}

// statusDetails builds the detail string for a repo status line.
// Format examples (spec 3.4):
//   - Clean, up-to-date: "main"
//   - Dirty only: "main ~3 dirty"
//   - Ahead only: "main +2 ahead"
//   - Behind only: "main -3 behind"
//   - Both ahead and behind: "main +1 ahead -3 behind"
//   - Dirty + ahead: "main ~3 dirty +1 ahead"
func statusDetails(s git.RepoStatus) string {
	var sb strings.Builder
	sb.WriteString(s.Branch)

	if !s.Clean {
		sb.WriteString(fmt.Sprintf(" ~%d dirty", s.DirtyFiles))
	}
	if s.HasUpstream {
		if s.Ahead > 0 {
			sb.WriteString(fmt.Sprintf(" +%d ahead", s.Ahead))
		}
		if s.Behind > 0 {
			sb.WriteString(fmt.Sprintf(" -%d behind", s.Behind))
		}
	}

	return sb.String()
}

// needsAttention returns true when a repo status is anything other than clean
// and fully in sync with upstream.
func needsAttention(s git.RepoStatus) bool {
	if !s.Clean {
		return true
	}
	if s.HasUpstream && (s.Ahead > 0 || s.Behind > 0) {
		return true
	}
	return false
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Apply no-color setting before any output.
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Apply --filter flag: narrow down to matching owners/repos.
	filteredOwners := manifest.FilterOwners(m.Owners(), Filters)

	// labelWidth is the column width for the label field (for alignment).
	// We compute the maximum label length across all repos.
	const minLabelWidth = 24
	labelWidth := minLabelWidth
	for _, owner := range filteredOwners {
		for _, entry := range owner.Repos {
			if l := len(repoLabel(entry)); l > labelWidth {
				labelWidth = l
			}
		}
	}

	type repoResult struct {
		label     string
		symbol    string
		details   string
		notCloned bool
		attention bool
	}

	type ownerResult struct {
		name  string
		repos []repoResult
	}

	var ownerResults []ownerResult

	// For JSON path: collect per-repo JSON objects alongside human-mode data.
	var jsonRepos []statusRepoJSON

	countOK := 0
	countAttention := 0
	countNotCloned := 0

	for _, owner := range filteredOwners {
		var repos []repoResult

		for _, entry := range owner.Repos {
			label := repoLabel(entry)
			var result repoResult
			result.label = label

			if !git.IsRepo(entry.LocalPath) {
				result.symbol = ui.SymbolError()
				result.details = "not cloned"
				result.notCloned = true
				countNotCloned++

				jsonRepos = append(jsonRepos, statusRepoJSON{
					Repo:      entry.Repo,
					Owner:     entry.Owner,
					Category:  entry.Category,
					LocalPath: entry.LocalPath,
					Cloned:    false,
				})
			} else {
				s, err := git.Status(entry.LocalPath)
				if err != nil {
					// Treat git errors as needing attention.
					result.symbol = ui.SymbolWarn()
					result.details = fmt.Sprintf("error: %v", err)
					result.attention = true
					countAttention++

					// Emit a minimal JSON entry for error cases (cloned but status failed).
					cloned := true
					jsonRepos = append(jsonRepos, statusRepoJSON{
						Repo:      entry.Repo,
						Owner:     entry.Owner,
						Category:  entry.Category,
						LocalPath: entry.LocalPath,
						Cloned:    cloned,
					})
				} else if needsAttention(s) {
					result.symbol = ui.SymbolWarn()
					result.details = statusDetails(s)
					result.attention = true
					countAttention++

					jsonRepos = append(jsonRepos, makeStatusRepoJSON(entry, s))
				} else {
					result.symbol = ui.SymbolOK()
					result.details = statusDetails(s)
					countOK++

					jsonRepos = append(jsonRepos, makeStatusRepoJSON(entry, s))
				}
			}

			repos = append(repos, result)
		}

		ownerResults = append(ownerResults, ownerResult{name: owner.Name, repos: repos})
	}

	// JSON output path.
	if output.IsJSON() {
		// Apply --dirty / --behind / --ahead post-filters (AND logic).
		filtered := make([]statusRepoJSON, 0, len(jsonRepos))
		for _, r := range jsonRepos {
			if statusDirty && (r.Clean == nil || *r.Clean) {
				continue
			}
			if statusBehind && (r.Behind == nil || *r.Behind == 0) {
				continue
			}
			if statusAhead && (r.Ahead == nil || *r.Ahead == 0) {
				continue
			}
			filtered = append(filtered, r)
		}

		// Recompute summary counts from filtered set.
		okCount := 0
		attentionCount := 0
		notClonedCount := 0
		for _, r := range filtered {
			if !r.Cloned {
				notClonedCount++
			} else if r.Clean != nil && *r.Clean &&
				(r.Ahead == nil || *r.Ahead == 0) &&
				(r.Behind == nil || *r.Behind == 0) {
				okCount++
			} else {
				attentionCount++
			}
		}

		exitCode := 0
		if attentionCount > 0 || notClonedCount > 0 {
			exitCode = 1
		}

		result := output.SuccessResult{
			Command:  "status",
			ExitCode: exitCode,
			Summary: map[string]int{
				"ok":         okCount,
				"attention":  attentionCount,
				"not_cloned": notClonedCount,
				"total":      len(filtered),
			},
			Repos: filtered,
		}
		output.PrintAndExit(result)
		return nil
	}

	// Human output path (unchanged).

	// Apply --dirty / --behind / --ahead post-filters to human output.
	// Re-filter ownerResults to only include matching repos.
	if statusDirty || statusBehind || statusAhead {
		// We need to cross-reference ownerResults with jsonRepos for filtering.
		// Build a set of repo names that pass the post-filter.
		passing := make(map[string]bool, len(jsonRepos))
		for _, r := range jsonRepos {
			if statusDirty && (r.Clean == nil || *r.Clean) {
				continue
			}
			if statusBehind && (r.Behind == nil || *r.Behind == 0) {
				continue
			}
			if statusAhead && (r.Ahead == nil || *r.Ahead == 0) {
				continue
			}
			passing[r.Repo] = true
		}

		// Rebuild counts from scratch.
		countOK = 0
		countAttention = 0
		countNotCloned = 0

		// Rebuild ownerResults using jsonRepos (same order) as the source of truth.
		var filteredOwnerResults []ownerResult
		jsonIdx := 0
		for _, or_ := range ownerResults {
			var filteredRepos []repoResult
			for _, r := range or_.repos {
				jr := jsonRepos[jsonIdx]
				jsonIdx++
				if !passing[jr.Repo] {
					continue
				}
				filteredRepos = append(filteredRepos, r)
				// Update summary counts.
				if r.notCloned {
					countNotCloned++
				} else if r.attention {
					countAttention++
				} else {
					countOK++
				}
			}
			if len(filteredRepos) > 0 {
				filteredOwnerResults = append(filteredOwnerResults, ownerResult{name: or_.name, repos: filteredRepos})
			}
		}
		ownerResults = filteredOwnerResults
	}

	// Print output grouped by owner.
	for i, owner := range ownerResults {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(owner.name)
		for _, r := range owner.repos {
			paddedLabel := ui.PadRight(r.label, labelWidth)
			fmt.Printf("  %s  %s %s\n", paddedLabel, r.symbol, r.details)
		}
	}

	// Summary.
	fmt.Println()
	summaryParts := []string{
		fmt.Sprintf("%d OK, %d need attention, %d not cloned", countOK, countAttention, countNotCloned),
	}
	fmt.Println(ui.SummaryLine(summaryParts...))

	// Exit 1 if any repo needs attention or is not cloned.
	if countAttention > 0 || countNotCloned > 0 {
		os.Exit(1)
	}

	return nil
}

// makeStatusRepoJSON builds a statusRepoJSON for a cloned repo with full git status.
func makeStatusRepoJSON(entry manifest.RepoEntry, s git.RepoStatus) statusRepoJSON {
	clean := s.Clean
	dirtyFiles := s.DirtyFiles
	ahead := s.Ahead
	behind := s.Behind
	hasUpstream := s.HasUpstream

	r := statusRepoJSON{
		Repo:        entry.Repo,
		Owner:       entry.Owner,
		Category:    entry.Category,
		LocalPath:   entry.LocalPath,
		Cloned:      true,
		Branch:      s.Branch,
		Clean:       &clean,
		DirtyFiles:  &dirtyFiles,
		HasUpstream: &hasUpstream,
	}

	if s.HasUpstream {
		r.Ahead = &ahead
		r.Behind = &behind
	}

	return r
}
