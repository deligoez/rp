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
	archive    *listCategoryBlock // nil if none
}

func runList(cmd *cobra.Command, args []string) error {
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Apply --filter to repo list.
	filteredRepos := manifest.FilterRepos(m.Repos(), Filters)

	// Build a set of filtered repo names for fast lookup.
	filteredSet := make(map[string]bool, len(filteredRepos))
	for _, r := range filteredRepos {
		filteredSet[r.Repo] = true
	}

	totalRepos := 0
	totalMissing := 0

	var blocks []listOwnerBlock

	for _, owner := range m.Owners() {
		catOrder := []string{}
		catMap := map[string][]listRepoLine{}
		var archiveLines []listRepoLine

		for _, entry := range owner.Repos {
			// Skip repos excluded by filter.
			if !filteredSet[entry.Repo] {
				continue
			}

			repoName := listRepoBaseName(entry.Repo)
			exists := listDirExists(entry.LocalPath)

			rl := listRepoLine{
				name:   repoName,
				path:   listTildeCollapse(entry.LocalPath),
				exists: exists,
			}

			totalRepos++
			if !exists {
				totalMissing++
			}

			if entry.IsArchive {
				archiveLines = append(archiveLines, rl)
			} else {
				cat := entry.Category
				if owner.IsFlat {
					// Flat owners: all non-archive repos share the empty-string bucket
					cat = ""
				}
				if _, seen := catMap[cat]; !seen {
					catOrder = append(catOrder, cat)
				}
				catMap[cat] = append(catMap[cat], rl)
			}
		}

		var catBlocks []listCategoryBlock
		for _, cat := range catOrder {
			catBlocks = append(catBlocks, listCategoryBlock{
				name:  cat,
				repos: catMap[cat],
			})
		}

		var archBlock *listCategoryBlock
		if len(archiveLines) > 0 {
			archBlock = &listCategoryBlock{name: "archive", repos: archiveLines}
		}

		blocks = append(blocks, listOwnerBlock{
			name:       owner.Name,
			isFlat:     owner.IsFlat,
			categories: catBlocks,
			archive:    archBlock,
		})
	}

	// JSON output path.
	if output.IsJSON() {
		type jsonRepo struct {
			Repo      string `json:"repo"`
			Owner     string `json:"owner"`
			Category  string `json:"category"`
			LocalPath string `json:"local_path"`
			Exists    bool   `json:"exists"`
			IsArchive bool   `json:"is_archive"`
			IsFlat    bool   `json:"is_flat"`
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
				IsArchive: entry.IsArchive,
				IsFlat:    entry.IsFlat,
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

	// Compute column widths across all visible lines for aligned output.
	nameWidth := 0
	pathWidth := 0

	for _, ob := range blocks {
		for _, cb := range ob.categories {
			for _, rl := range cb.repos {
				if !listMissing || !rl.exists {
					if len(rl.name) > nameWidth {
						nameWidth = len(rl.name)
					}
					if len(rl.path) > pathWidth {
						pathWidth = len(rl.path)
					}
				}
			}
		}
		if ob.archive != nil {
			for _, rl := range ob.archive.repos {
				if !listMissing || !rl.exists {
					if len(rl.name) > nameWidth {
						nameWidth = len(rl.name)
					}
					if len(rl.path) > pathWidth {
						pathWidth = len(rl.path)
					}
				}
			}
		}
	}

	// Print output.
	for _, ob := range blocks {
		if listMissing && !listOwnerHasMissing(ob) {
			continue
		}

		ownerHeader := ob.name
		if ob.isFlat {
			ownerHeader += " (flat)"
		}
		fmt.Println(ownerHeader)

		// Non-archive categories.
		for _, cb := range ob.categories {
			if listMissing && !listCategoryHasMissing(cb) {
				continue
			}

			if ob.isFlat {
				// Flat owner: repos at 2-space indent, no category sub-header.
				for _, rl := range cb.repos {
					if listMissing && rl.exists {
						continue
					}
					fmt.Println(listFormatRepoLine(rl, 2, nameWidth, pathWidth))
				}
			} else {
				// Categorized owner: category sub-header at 2-space indent.
				fmt.Printf("  %s\n", cb.name)
				for _, rl := range cb.repos {
					if listMissing && rl.exists {
						continue
					}
					fmt.Println(listFormatRepoLine(rl, 4, nameWidth, pathWidth))
				}
			}
		}

		// Archive sub-group (always at 2-space indent with its own "archive" header).
		if ob.archive != nil {
			if !listMissing || listCategoryHasMissing(*ob.archive) {
				fmt.Println("  archive")
				for _, rl := range ob.archive.repos {
					if listMissing && rl.exists {
						continue
					}
					fmt.Println(listFormatRepoLine(rl, 4, nameWidth, pathWidth))
				}
			}
		}

		fmt.Println()
	}

	// Summary.
	totalStr := ui.Plural(totalRepos, "repo")
	fmt.Printf("-- %s total, %d missing --\n", totalStr, totalMissing)

	if totalMissing > 0 {
		os.Exit(1)
	}

	return nil
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
	if ob.archive != nil && listCategoryHasMissing(*ob.archive) {
		return true
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
