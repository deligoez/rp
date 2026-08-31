package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/spf13/cobra"
)

var listMissing bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all repos in manifest",
	RunE:  runList,
}

func init() {
	listCmd.Flags().BoolVar(&listMissing, "missing", false, "show only repos not cloned locally")
	rootCmd.AddCommand(listCmd)
}

// listRepoLine holds display data for a single repo row.
type listRepoLine struct {
	name   string
	path   string
	exists bool
}

// listCategoryBlock holds a category (or archive) group of repos.
type listCategoryBlock struct {
	name  string // empty string = flat bucket (no sub-header printed)
	repos []listRepoLine
}

// listOwnerBlock holds all display data for one owner.
type listOwnerBlock struct {
	name       string
	isFlat     bool
	categories []listCategoryBlock
}

func runList(cmd *cobra.Command, args []string) error {
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return manifestError("list", err)
	}

	filteredRepos := manifest.FilterRepos(m.Repos(), Filters)
	blocks, totalRepos, totalMissing := buildListBlocks(m, filteredRepos)

	if output.IsJSON() {
		printListJSON(filteredRepos, totalRepos, totalMissing)
	}

	printListHuman(blocks, totalRepos, totalMissing)
	return nil
}

// buildListBlocks groups the filtered repos into owner and category blocks in
// manifest order, and reports how many repos were visible and how many of
// those are missing from disk.
func buildListBlocks(m *manifest.Manifest, filteredRepos []manifest.RepoEntry) (blocks []listOwnerBlock, totalRepos, totalMissing int) {
	filteredSet := make(map[string]bool, len(filteredRepos))
	for _, r := range filteredRepos {
		filteredSet[r.Repo] = true
	}

	for _, owner := range m.Owners() {
		catOrder := []string{}
		catMap := map[string][]listRepoLine{}

		for _, entry := range owner.Repos {
			if !filteredSet[entry.Repo] {
				continue
			}

			exists := listDirExists(entry.LocalPath)
			totalRepos++
			if !exists {
				totalMissing++
			}

			cat := entry.Category
			if _, seen := catMap[cat]; !seen {
				catOrder = append(catOrder, cat)
			}
			catMap[cat] = append(catMap[cat], listRepoLine{
				name:   listRepoBaseName(entry.Repo),
				path:   listTildeCollapse(entry.LocalPath),
				exists: exists,
			})
		}

		if len(catOrder) == 0 {
			continue
		}

		catBlocks := make([]listCategoryBlock, 0, len(catOrder))
		for _, cat := range catOrder {
			catBlocks = append(catBlocks, listCategoryBlock{name: cat, repos: catMap[cat]})
		}

		blocks = append(blocks, listOwnerBlock{
			name:       owner.Name,
			isFlat:     owner.IsFlat,
			categories: catBlocks,
		})
	}

	return blocks, totalRepos, totalMissing
}

// printListJSON writes the list result and exits.
func printListJSON(filteredRepos []manifest.RepoEntry, totalRepos, totalMissing int) {
	type jsonRepo struct {
		Repo      string `json:"repo"`
		Owner     string `json:"owner"`
		Category  string `json:"category"`
		LocalPath string `json:"local_path"`
		Exists    bool   `json:"exists"`
	}

	jsonRepos := make([]jsonRepo, 0, len(filteredRepos))
	for _, entry := range filteredRepos {
		exists := listDirExists(entry.LocalPath)
		if listMissing && exists {
			continue
		}
		jsonRepos = append(jsonRepos, jsonRepo{
			Repo:      entry.Repo,
			Owner:     entry.Owner,
			Category:  entry.Category,
			LocalPath: entry.LocalPath,
			Exists:    exists,
		})
	}

	exitCode := 0
	if totalMissing > 0 {
		exitCode = 1
	}

	output.PrintAndExit(output.SuccessResult{
		Command:  "list",
		ExitCode: exitCode,
		Summary: map[string]int{
			"total":   totalRepos,
			"missing": totalMissing,
		},
		Repos: jsonRepos,
	})
}

// listColumnWidths measures the widest name and path among the rows that will
// actually be printed, so every row aligns.
func listColumnWidths(blocks []listOwnerBlock) (nameWidth, pathWidth int) {
	for _, ob := range blocks {
		for _, cb := range ob.categories {
			for _, rl := range cb.repos {
				if listMissing && rl.exists {
					continue
				}
				if len(rl.name) > nameWidth {
					nameWidth = len(rl.name)
				}
				if len(rl.path) > pathWidth {
					pathWidth = len(rl.path)
				}
			}
		}
	}
	return nameWidth, pathWidth
}

// printListHuman renders the owner blocks and the summary, then exits 1 when
// any repo is missing.
func printListHuman(blocks []listOwnerBlock, totalRepos, totalMissing int) {
	nameWidth, pathWidth := listColumnWidths(blocks)

	for _, ob := range blocks {
		if listMissing && !listOwnerHasMissing(ob) {
			continue
		}

		fmt.Println(ob.name)
		for _, cb := range ob.categories {
			if listMissing && !listCategoryHasMissing(cb) {
				continue
			}
			printListCategory(ob, cb, nameWidth, pathWidth)
		}
		fmt.Println()
	}

	fmt.Printf("-- %s total, %d missing --\n", ui.Plural(totalRepos, "repo"), totalMissing)

	if totalMissing > 0 {
		os.Exit(1)
	}
}

// printListCategory renders one category: a flat owner prints its repos
// directly, a categorized one prints a sub-header first.
func printListCategory(ob listOwnerBlock, cb listCategoryBlock, nameWidth, pathWidth int) {
	indent := 2
	if !ob.isFlat {
		fmt.Printf("  %s\n", cb.name)
		indent = 4
	}
	for _, rl := range cb.repos {
		if listMissing && rl.exists {
			continue
		}
		fmt.Println(listFormatRepoLine(rl, indent, nameWidth, pathWidth))
	}
}

// listFormatRepoLine renders one repo row with indent and aligned columns.
func listFormatRepoLine(rl listRepoLine, indent, nameWidth, pathWidth int) string {
	prefix := strings.Repeat(" ", indent)
	paddedName := ui.PadRight(rl.name, nameWidth)
	paddedPath := ui.PadRight(rl.path, pathWidth)

	var status string
	if rl.exists {
		status = ui.SymbolOK()
	} else {
		status = ui.SymbolError() + " missing"
	}

	return fmt.Sprintf("%s%s   %s   %s", prefix, paddedName, paddedPath, status)
}

// listRepoBaseName extracts the repo name from "github_owner/repo_name".
func listRepoBaseName(repo string) string {
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		return repo[idx+1:]
	}
	return repo
}

// listDirExists returns true if path exists and is a directory.
func listDirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// listTildeCollapse replaces the home directory prefix with ~.
func listTildeCollapse(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func listOwnerHasMissing(ob listOwnerBlock) bool {
	for _, cb := range ob.categories {
		if listCategoryHasMissing(cb) {
			return true
		}
	}
	return false
}

func listCategoryHasMissing(cb listCategoryBlock) bool {
	for _, rl := range cb.repos {
		if !rl.exists {
			return true
		}
	}
	return false
}
