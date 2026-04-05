package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/ui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of every repo in the manifest",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
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

	// labelWidth is the column width for the label field (for alignment).
	// We compute the maximum label length across all repos.
	const minLabelWidth = 24
	labelWidth := minLabelWidth
	for _, owner := range m.Owners() {
		for _, entry := range owner.Repos {
			if l := len(repoLabel(entry)); l > labelWidth {
				labelWidth = l
			}
		}
	}

	type repoResult struct {
		label      string
		symbol     string
		details    string
		notCloned  bool
		attention  bool
	}

	type ownerResult struct {
		name  string
		repos []repoResult
	}

	var ownerResults []ownerResult

	countOK := 0
	countAttention := 0
	countNotCloned := 0

	for _, owner := range m.Owners() {
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
			} else {
				s, err := git.Status(entry.LocalPath)
				if err != nil {
					// Treat git errors as needing attention.
					result.symbol = ui.SymbolWarn()
					result.details = fmt.Sprintf("error: %v", err)
					result.attention = true
					countAttention++
				} else if needsAttention(s) {
					result.symbol = ui.SymbolWarn()
					result.details = statusDetails(s)
					result.attention = true
					countAttention++
				} else {
					result.symbol = ui.SymbolOK()
					result.details = statusDetails(s)
					countOK++
				}
			}

			repos = append(repos, result)
		}

		ownerResults = append(ownerResults, ownerResult{name: owner.Name, repos: repos})
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
