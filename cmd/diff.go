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

func runDiff(cmd *cobra.Command, args []string) error {
	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Parse --since flag if provided.
	var hasSince bool
	var cutoff time.Time
	if diffSince != "" {
		dur, err := parseDiffDuration(diffSince)
		if err != nil {
			return err
		}
		hasSince = true
		cutoff = time.Now().Add(-dur)
	}

	allRepos := manifest.FilterRepos(m.Repos(), Filters)

	// Build owner lookup for labels.
	ownerByRepo := make(map[string]manifest.OwnerGroup)
	for _, og := range m.Owners() {
		for _, entry := range og.Repos {
			ownerByRepo[entry.Repo] = og
		}
	}

	now := time.Now()
	var results []diffRepoResult
	total := 0

	for _, entry := range allRepos {
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

		daysAgo := int(math.Round(now.Sub(date).Hours() / 24))

		// Build display label.
		repoName := entry.Repo
		if idx := strings.LastIndex(entry.Repo, "/"); idx >= 0 {
			repoName = entry.Repo[idx+1:]
		}
		og := ownerByRepo[entry.Repo]
		var label string
		if entry.Category == "" {
			label = repoName
		} else {
			label = entry.Category + "/" + repoName
		}

		results = append(results, diffRepoResult{
			repo:    entry.Repo,
			owner:   og.Name,
			label:   label,
			sha:     sha,
			message: message,
			date:    date,
			daysAgo: daysAgo,
		})
	}

	shown := len(results)

	// JSON output path.
	if output.IsJSON() {
		type jsonRepo struct {
			Repo    string `json:"repo"`
			SHA     string `json:"sha"`
			Message string `json:"message"`
			Date    string `json:"date"`
			DaysAgo int    `json:"days_ago"`
		}

		jsonRepos := make([]jsonRepo, 0, shown)
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
				"shown": shown,
			},
			Repos: jsonRepos,
		})
	}

	// Human output path — group by owner.
	type ownerBlock struct {
		name    string
		results []diffRepoResult
	}
	var ownerBlocks []ownerBlock
	ownerIdx := make(map[string]int)

	for _, r := range results {
		if idx, ok := ownerIdx[r.owner]; ok {
			ownerBlocks[idx].results = append(ownerBlocks[idx].results, r)
		} else {
			ownerIdx[r.owner] = len(ownerBlocks)
			ownerBlocks = append(ownerBlocks, ownerBlock{
				name:    r.owner,
				results: []diffRepoResult{r},
			})
		}
	}

	// Compute label column width.
	labelWidth := 0
	for _, ob := range ownerBlocks {
		for _, r := range ob.results {
			if len(r.label) > labelWidth {
				labelWidth = len(r.label)
			}
		}
	}

	for i, ob := range ownerBlocks {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(ob.name)
		for _, r := range ob.results {
			label := r.label
			padding := strings.Repeat(" ", labelWidth-len(label))
			fmt.Printf("  %s%s   %s %s (%d days ago)\n",
				label, padding,
				r.sha,
				r.message,
				r.daysAgo,
			)
		}
	}

	if len(ownerBlocks) > 0 {
		fmt.Println()
	}
	fmt.Printf("-- %d %s shown --\n", shown, diffPluralRepos(shown))

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
