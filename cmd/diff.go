package cmd

import (
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/spf13/cobra"
)

var diffSince string

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show the most recent commit for each cloned repo",
	RunE:  runDiff,
}

func init() {
	diffCmd.Flags().StringVar(&diffSince, "since", "", "only show repos with commits newer than duration (e.g. 7d, 24h)")
	rootCmd.AddCommand(diffCmd)
}

// diffRepoResult holds the collected data for one repo.
type diffRepoResult struct {
	repo    string
	owner   string
	label   string
	sha     string
	message string
	date    time.Time
	daysAgo int
}

// parseDiffCutoff turns --since into an absolute cutoff. hasSince is false
// when the flag was not given, in which case cutoff is unused.
func parseDiffCutoff() (hasSince bool, cutoff time.Time, err error) {
	if diffSince == "" {
		return false, time.Time{}, nil
	}
	dur, err := parseDiffDuration(diffSince)
	if err != nil {
		return false, time.Time{}, err
	}
	return true, time.Now().Add(-dur), nil
}

// diffOwnerLookup maps each repo to its owner group, for building labels.
func diffOwnerLookup(m *manifest.Manifest) map[string]manifest.OwnerGroup {
	byRepo := make(map[string]manifest.OwnerGroup)
	for _, og := range m.Owners() {
		for _, entry := range og.Repos {
			byRepo[entry.Repo] = og
		}
	}
	return byRepo
}

// collectDiffResults reads each cloned repo's last commit. total counts every
// repo that had one; the returned slice is what survives --since.
func collectDiffResults(repos []manifest.RepoEntry, ownerByRepo map[string]manifest.OwnerGroup, hasSince bool, cutoff time.Time) (results []diffRepoResult, total int) {
	now := time.Now()

	for _, entry := range repos {
		if !git.IsRepo(entry.LocalPath) {
			continue
		}

		sha, message, date, ok := diffLastCommit(entry.LocalPath)
		if !ok {
			// Empty repo or git error — skip silently.
			continue
		}

		total++

		if hasSince && date.Before(cutoff) {
			continue
		}

		repoName := entry.Repo
		if idx := strings.LastIndex(entry.Repo, "/"); idx >= 0 {
			repoName = entry.Repo[idx+1:]
		}
		label := repoName
		if entry.Category != "" {
			label = entry.Category + "/" + repoName
		}

		results = append(results, diffRepoResult{
			repo:    entry.Repo,
			owner:   ownerByRepo[entry.Repo].Name,
			label:   label,
			sha:     sha,
			message: message,
			date:    date,
			daysAgo: int(math.Round(now.Sub(date).Hours() / 24)),
		})
	}

	return results, total
}

// printDiffJSON writes the diff result and exits.
func printDiffJSON(results []diffRepoResult, total int) {
	type jsonRepo struct {
		Repo    string `json:"repo"`
		SHA     string `json:"sha"`
		Message string `json:"message"`
		Date    string `json:"date"`
		DaysAgo int    `json:"days_ago"`
	}

	jsonRepos := make([]jsonRepo, 0, len(results))
	for _, r := range results {
		jsonRepos = append(jsonRepos, jsonRepo{
			Repo:    r.repo,
			SHA:     r.sha,
			Message: r.message,
			Date:    r.date.UTC().Format(time.RFC3339),
			DaysAgo: r.daysAgo,
		})
	}

	output.PrintAndExit(output.SuccessResult{
		Command:  "diff",
		ExitCode: 0,
		Summary: map[string]int{
			"total": total,
			"shown": len(results),
		},
		Repos: jsonRepos,
	})
}

// printDiffHuman prints the results grouped by owner, with aligned labels.
func printDiffHuman(results []diffRepoResult) {
	type ownerBlock struct {
		name    string
		results []diffRepoResult
	}
	var ownerBlocks []ownerBlock
	ownerIdx := make(map[string]int)
	labelWidth := 0

	for _, r := range results {
		if len(r.label) > labelWidth {
			labelWidth = len(r.label)
		}
		if idx, ok := ownerIdx[r.owner]; ok {
			ownerBlocks[idx].results = append(ownerBlocks[idx].results, r)
			continue
		}
		ownerIdx[r.owner] = len(ownerBlocks)
		ownerBlocks = append(ownerBlocks, ownerBlock{name: r.owner, results: []diffRepoResult{r}})
	}

	for i, ob := range ownerBlocks {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(ob.name)
		for _, r := range ob.results {
			padding := strings.Repeat(" ", labelWidth-len(r.label))
			fmt.Printf("  %s%s   %s %s (%d days ago)\n",
				r.label, padding, r.sha, r.message, r.daysAgo)
		}
	}

	if len(ownerBlocks) > 0 {
		fmt.Println()
	}
	fmt.Printf("-- %d %s shown --\n", len(results), diffPluralRepos(len(results)))
}

func runDiff(cmd *cobra.Command, args []string) error {
	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return manifestError("diff", err)
	}

	// Parse --since flag if provided.
	hasSince, cutoff, err := parseDiffCutoff()
	if err != nil {
		return err
	}

	allRepos := manifest.FilterRepos(m.Repos(), Filters)
	ownerByRepo := diffOwnerLookup(m)

	results, total := collectDiffResults(allRepos, ownerByRepo, hasSince, cutoff)

	if output.IsJSON() {
		printDiffJSON(results, total)
	}

	printDiffHuman(results)
	return nil
}

// sep is the ASCII Record Separator used as a delimiter in git log format.
// This avoids conflicts with | or other characters that may appear in commit messages.
const sep = "\x1e"

// diffLastCommit runs git log -1 with a safe delimiter and parses the result.
// Returns (sha, message, date, ok). ok is false if the repo has no commits or on error.
func diffLastCommit(path string) (sha, message string, date time.Time, ok bool) {
	format := "%h" + sep + "%s" + sep + "%cI"
	out, err := exec.Command("git", "-C", path, "log", "-1", "--format="+format).Output()
	if err != nil {
		return "", "", time.Time{}, false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", "", time.Time{}, false
	}
	parts := strings.SplitN(line, sep, 3)
	if len(parts) != 3 {
		return "", "", time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, parts[2])
	if err != nil {
		return "", "", time.Time{}, false
	}
	return parts[0], parts[1], t, true
}

// parseDiffDuration parses a duration string of the form "Nd" or "Nh".
func parseDiffDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q: use Nd or Nh", s)
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration %q: use Nd or Nh", s)
	}
	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration %q: use Nd or Nh", s)
	}
}

func diffPluralRepos(n int) string {
	if n == 1 {
		return "repo"
	}
	return "repos"
}
