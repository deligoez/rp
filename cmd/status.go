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
		fmt.Fprintf(&sb, " ~%d dirty", s.DirtyFiles)
	}
	if s.HasUpstream {
		if s.Ahead > 0 {
			fmt.Fprintf(&sb, " +%d ahead", s.Ahead)
		}
		if s.Behind > 0 {
			fmt.Fprintf(&sb, " -%d behind", s.Behind)
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

// statusRepoLine is one repo's human-mode row plus the classification the
// summary counts are derived from.
type statusRepoLine struct {
	label     string
	symbol    string
	details   string
	notCloned bool
	attention bool
}

// statusOwnerLines groups one owner's rows.
type statusOwnerLines struct {
	name  string
	repos []statusRepoLine
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Apply no-color setting before any output.
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return manifestError("status", err)
	}

	// Apply --filter flag: narrow down to matching owners/repos.
	filteredOwners := manifest.FilterOwners(m.Owners(), Filters)

	ownerLines, jsonRepos := collectStatus(filteredOwners)

	if output.IsJSON() {
		printStatusJSON(jsonRepos)
		return nil
	}

	printStatusHuman(ownerLines, jsonRepos, statusLabelWidth(filteredOwners))
	return nil
}

// statusLabelWidth is the column width for the label field, wide enough for
// the longest label and never below the minimum.
func statusLabelWidth(owners []manifest.OwnerGroup) int {
	const minLabelWidth = 24
	width := minLabelWidth
	for _, owner := range owners {
		for _, entry := range owner.Repos {
			if l := len(repoLabel(entry)); l > width {
				width = l
			}
		}
	}
	return width
}

// collectStatus inspects every repo once and returns both output modes' data.
// The two results are in the same order, repo for repo, so the human path can
// pair a row with its JSON entry by index.
func collectStatus(owners []manifest.OwnerGroup) ([]statusOwnerLines, []statusRepoJSON) {
	var ownerLines []statusOwnerLines
	var jsonRepos []statusRepoJSON

	for _, owner := range owners {
		repos := make([]statusRepoLine, 0, len(owner.Repos))
		for _, entry := range owner.Repos {
			line, jr := evalStatusRepo(entry)
			repos = append(repos, line)
			jsonRepos = append(jsonRepos, jr)
		}
		ownerLines = append(ownerLines, statusOwnerLines{name: owner.Name, repos: repos})
	}

	return ownerLines, jsonRepos
}

// evalStatusRepo classifies one repo for both output modes.
func evalStatusRepo(entry manifest.RepoEntry) (statusRepoLine, statusRepoJSON) {
	line := statusRepoLine{label: repoLabel(entry)}

	if !git.IsRepo(entry.LocalPath) {
		line.symbol = ui.SymbolError()
		line.details = "not cloned"
		line.notCloned = true
		return line, statusRepoJSON{
			Repo:      entry.Repo,
			Owner:     entry.Owner,
			Category:  entry.Category,
			LocalPath: entry.LocalPath,
			Cloned:    false,
		}
	}

	s, err := git.Status(entry.LocalPath)
	if err != nil {
		// Treat git errors as needing attention. The JSON entry still carries
		// the git fields, because cloned=true requires them.
		line.symbol = ui.SymbolWarn()
		line.details = fmt.Sprintf("error: %v", err)
		line.attention = true

		zero := 0
		f := false
		return line, statusRepoJSON{
			Repo:        entry.Repo,
			Owner:       entry.Owner,
			Category:    entry.Category,
			LocalPath:   entry.LocalPath,
			Cloned:      true,
			Branch:      "unknown",
			Clean:       &f,
			DirtyFiles:  &zero,
			Ahead:       &zero,
			Behind:      &zero,
			HasUpstream: &f,
		}
	}

	line.details = statusDetails(s)
	if needsAttention(s) {
		line.symbol = ui.SymbolWarn()
		line.attention = true
	} else {
		line.symbol = ui.SymbolOK()
	}
	return line, makeStatusRepoJSON(entry, s)
}

// statusPassesFilters reports whether a repo survives --dirty / --behind /
// --ahead. The flags are ANDed; with none set every repo passes.
func statusPassesFilters(r statusRepoJSON) bool {
	if statusDirty && (r.Clean == nil || *r.Clean) {
		return false
	}
	if statusBehind && (r.Behind == nil || *r.Behind == 0) {
		return false
	}
	if statusAhead && (r.Ahead == nil || *r.Ahead == 0) {
		return false
	}
	return true
}

// countStatusJSON derives the summary counts from JSON entries.
func countStatusJSON(repos []statusRepoJSON) (ok, attention, notCloned int) {
	for _, r := range repos {
		switch {
		case !r.Cloned:
			notCloned++
		case r.Clean != nil && *r.Clean &&
			(r.Ahead == nil || *r.Ahead == 0) &&
			(r.Behind == nil || *r.Behind == 0):
			ok++
		default:
			attention++
		}
	}
	return ok, attention, notCloned
}

// printStatusJSON writes the status result and exits.
func printStatusJSON(jsonRepos []statusRepoJSON) {
	filtered := make([]statusRepoJSON, 0, len(jsonRepos))
	for _, r := range jsonRepos {
		if statusPassesFilters(r) {
			filtered = append(filtered, r)
		}
	}

	okCount, attentionCount, notClonedCount := countStatusJSON(filtered)

	exitCode := 0
	if attentionCount > 0 || notClonedCount > 0 {
		exitCode = 1
	}

	output.PrintAndExit(output.SuccessResult{
		Command:  "status",
		ExitCode: exitCode,
		Summary: map[string]int{
			"ok":         okCount,
			"attention":  attentionCount,
			"not_cloned": notClonedCount,
			"total":      len(filtered),
		},
		Repos: filtered,
	})
}

// filterStatusLines drops rows whose JSON entry fails the post-filters.
// jsonRepos is in the same order as the rows, so they pair by index.
func filterStatusLines(ownerLines []statusOwnerLines, jsonRepos []statusRepoJSON) []statusOwnerLines {
	var kept []statusOwnerLines
	idx := 0
	for _, ol := range ownerLines {
		var repos []statusRepoLine
		for _, r := range ol.repos {
			jr := jsonRepos[idx]
			idx++
			if statusPassesFilters(jr) {
				repos = append(repos, r)
			}
		}
		if len(repos) > 0 {
			kept = append(kept, statusOwnerLines{name: ol.name, repos: repos})
		}
	}
	return kept
}

// printStatusHuman renders the owner blocks and the summary, then exits 1 when
// any repo needs attention or is not cloned.
func printStatusHuman(ownerLines []statusOwnerLines, jsonRepos []statusRepoJSON, labelWidth int) {
	if statusDirty || statusBehind || statusAhead {
		ownerLines = filterStatusLines(ownerLines, jsonRepos)
	}

	var countOK, countAttention, countNotCloned int
	for _, ol := range ownerLines {
		for _, r := range ol.repos {
			switch {
			case r.notCloned:
				countNotCloned++
			case r.attention:
				countAttention++
			default:
				countOK++
			}
		}
	}

	for i, owner := range ownerLines {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(owner.name)
		for _, r := range owner.repos {
			fmt.Printf("  %s  %s %s\n", ui.PadRight(r.label, labelWidth), r.symbol, r.details)
		}
	}

	fmt.Println()
	fmt.Println(ui.SummaryLine(
		fmt.Sprintf("%d OK, %d need attention, %d not cloned", countOK, countAttention, countNotCloned),
	))

	if countAttention > 0 || countNotCloned > 0 {
		os.Exit(1)
	}
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
