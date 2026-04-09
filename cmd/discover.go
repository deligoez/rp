package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/spf13/cobra"
)

var (
	discoverForks    bool
	discoverArchived bool
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Find GitHub repos not tracked in the manifest",
	RunE:  runDiscover,
}

func init() {
	discoverCmd.Flags().BoolVar(&discoverForks, "forks", false, "include forked repos in results")
	discoverCmd.Flags().BoolVar(&discoverArchived, "archived", false, "include archived repos in results")
	rootCmd.AddCommand(discoverCmd)
}

// ghRepo represents a GitHub repository returned by gh CLI.
type ghRepo struct {
	NameWithOwner string `json:"nameWithOwner"`
	IsFork        bool   `json:"isFork"`
	IsArchived    bool   `json:"isArchived"`
}

// ghAuthCheck verifies gh CLI is installed and authenticated.
func ghAuthCheck() error {
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		// Distinguish between gh not found and not authenticated.
		if _, lookErr := exec.LookPath("gh"); lookErr != nil {
			return output.NewHintError(
				fmt.Errorf("gh CLI not found"),
				"install gh from https://cli.github.com and run 'gh auth login'",
			)
		}
		return output.NewHintError(
			fmt.Errorf("gh is not authenticated"),
			"run 'gh auth login' to authenticate",
		)
	}
	return nil
}

// ghCurrentUser returns the authenticated user's login.
func ghCurrentUser() (string, error) {
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ghListOrgs returns the authenticated user's org memberships (paginated).
func ghListOrgs() ([]string, error) {
	out, err := exec.Command("gh", "api", "user/orgs", "--paginate", "--jq", ".[].login").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list orgs: %w", err)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// ghListRepos lists repos for a given owner via gh CLI.
func ghListRepos(owner string) ([]ghRepo, error) {
	out, err := exec.Command("gh", "repo", "list", owner,
		"--json", "nameWithOwner,isFork,isArchived",
		"--limit", "1000").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list repos for %s: %w", owner, err)
	}
	var repos []ghRepo
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, fmt.Errorf("failed to parse gh output for %s: %s", owner, string(out))
	}
	return repos, nil
}

// filterUntracked returns remote repos not present in the manifest.
// It deduplicates, applies fork/archived filters, and subtracts manifest repos.
func filterUntracked(remote []ghRepo, manifestRepos []string, forks, archived bool) []ghRepo {
	// 1. Deduplicate by lowercase nameWithOwner.
	seen := make(map[string]bool)
	var deduped []ghRepo
	for _, r := range remote {
		key := strings.ToLower(r.NameWithOwner)
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, r)
	}

	// 2. Apply fork/archived filters (AND logic).
	var filtered []ghRepo
	for _, r := range deduped {
		if !forks && r.IsFork {
			continue
		}
		if !archived && r.IsArchived {
			continue
		}
		filtered = append(filtered, r)
	}

	// 3. Build manifest set (lowercased).
	manifestSet := make(map[string]bool, len(manifestRepos))
	for _, repo := range manifestRepos {
		manifestSet[strings.ToLower(repo)] = true
	}

	// 4. Subtract manifest repos.
	var result []ghRepo
	for _, r := range filtered {
		if !manifestSet[strings.ToLower(r.NameWithOwner)] {
			result = append(result, r)
		}
	}

	return result
}

// matchesDiscoverFilter checks if a nameWithOwner matches any of the filter patterns.
// Supports owner/ prefix and exact owner/name match, both case-insensitive.
func matchesDiscoverFilter(nameWithOwner string, filters []string) bool {
	lower := strings.ToLower(nameWithOwner)
	for _, f := range filters {
		fl := strings.ToLower(f)
		if strings.HasSuffix(fl, "/") {
			// Owner prefix match.
			if strings.HasPrefix(lower, fl) {
				return true
			}
		} else {
			// Exact match.
			if lower == fl {
				return true
			}
		}
	}
	return false
}

func runDiscover(cmd *cobra.Command, args []string) error {
	// Step 1: Verify gh CLI.
	if err := ghAuthCheck(); err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("discover", err)
		}
		return err
	}

	// Step 2: Load manifest.
	m, err := manifest.Load(ManifestPath)
	if err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("discover", err)
		}
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Collect manifest repo names.
	manifestRepos := m.Repos()
	manifestNames := make([]string, len(manifestRepos))
	for i, r := range manifestRepos {
		manifestNames[i] = r.Repo
	}

	// Steps 3-4: Fetch user login and orgs.
	currentUser, err := ghCurrentUser()
	if err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("discover", err)
		}
		return err
	}

	orgs, err := ghListOrgs()
	if err != nil {
		if output.IsJSON() {
			output.PrintErrorAndExit("discover", err)
		}
		return err
	}

	// Build owner list: personal first, then orgs alphabetically.
	sort.Strings(orgs)
	owners := append([]string{currentUser}, orgs...)

	// Step 5: Scan all owners sequentially.
	var allRemote []ghRepo
	for i, owner := range owners {
		if !output.IsJSON() {
			fmt.Fprintf(os.Stderr, "[%d/%d] scanning %s...\n", i+1, len(owners), owner)
		}
		repos, err := ghListRepos(owner)
		if err != nil {
			if output.IsJSON() {
				output.PrintErrorAndExit("discover", err)
			}
			return err
		}
		allRemote = append(allRemote, repos...)
	}

	totalRemote := countUniqueRepos(allRemote)

	// Steps 6-8: Filter untracked.
	untracked := filterUntracked(allRemote, manifestNames, discoverForks, discoverArchived)

	// Step 9: Apply --filter.
	if len(Filters) > 0 {
		var filtered []ghRepo
		for _, r := range untracked {
			if matchesDiscoverFilter(r.NameWithOwner, Filters) {
				filtered = append(filtered, r)
			}
		}
		untracked = filtered
	}

	// Determine exit code.
	exitCode := 0
	if len(untracked) > 0 {
		exitCode = 1
	}

	// JSON output path.
	if output.IsJSON() {
		type jsonRepo struct {
			Repo     string `json:"repo"`
			Owner    string `json:"owner"`
			Fork     bool   `json:"fork"`
			Archived bool   `json:"archived"`
		}

		jsonRepos := make([]jsonRepo, 0, len(untracked))
		for _, r := range untracked {
			owner := ""
			if idx := strings.Index(r.NameWithOwner, "/"); idx >= 0 {
				owner = r.NameWithOwner[:idx]
			}
			jsonRepos = append(jsonRepos, jsonRepo{
				Repo:     r.NameWithOwner,
				Owner:    owner,
				Fork:     r.IsFork,
				Archived: r.IsArchived,
			})
		}

		output.PrintAndExit(output.SuccessResult{
			Command:  "discover",
			ExitCode: exitCode,
			Summary: map[string]int{
				"untracked":      len(untracked),
				"owners_scanned": len(owners),
				"total_remote":   totalRemote,
				"total_manifest": len(manifestNames),
			},
			Repos: jsonRepos,
		})
	}

	// Human output path — group by owner.
	type ownerBlock struct {
		name  string
		repos []ghRepo
	}
	var blocks []ownerBlock
	ownerIdx := make(map[string]int)

	for _, r := range untracked {
		owner := ""
		if idx := strings.Index(r.NameWithOwner, "/"); idx >= 0 {
			owner = r.NameWithOwner[:idx]
		}
		if idx, ok := ownerIdx[owner]; ok {
			blocks[idx].repos = append(blocks[idx].repos, r)
		} else {
			ownerIdx[owner] = len(blocks)
			blocks = append(blocks, ownerBlock{name: owner, repos: []ghRepo{r}})
		}
	}

	for i, b := range blocks {
		if i > 0 {
			fmt.Println()
		}
		label := b.name
		if strings.EqualFold(b.name, currentUser) {
			label += " (personal)"
		}
		fmt.Println(label)
		for _, r := range b.repos {
			fmt.Printf("  %s\n", r.NameWithOwner)
		}
	}

	if len(blocks) > 0 {
		fmt.Println()
	}

	if len(untracked) == 0 {
		fmt.Println("-- all repos tracked --")
	} else {
		fmt.Printf("-- %d untracked %s across %d %s --\n",
			len(untracked), pluralRepos(len(untracked)),
			len(blocks), pluralOwners(len(blocks)))
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}

	return nil
}

// countUniqueRepos counts unique repos by case-insensitive nameWithOwner (after dedup).
func countUniqueRepos(repos []ghRepo) int {
	seen := make(map[string]bool)
	for _, r := range repos {
		seen[strings.ToLower(r.NameWithOwner)] = true
	}
	return len(seen)
}

func pluralRepos(n int) string {
	if n == 1 {
		return "repo"
	}
	return "repos"
}

func pluralOwners(n int) string {
	if n == 1 {
		return "owner"
	}
	return "owners"
}
