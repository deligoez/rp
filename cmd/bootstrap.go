package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
	"github.com/spf13/cobra"
)

var bootstrapDryRun bool

// bootstrapResult holds the outcome of processing a single repo during bootstrap.
type bootstrapResult struct {
	Entry   manifest.RepoEntry
	Status  bootstrapStatus
	ErrMsg  string
}

type bootstrapStatus int

const (
	bsCloned      bootstrapStatus = iota
	bsAlreadyExists
	bsFailed
	bsWouldClone
	bsWouldSkip
)

// repoLabel returns the display label for a repo entry following the spec convention:
//   - archive entries: "archive/{repo_name}"
//   - flat owners:     "{repo_name}"
//   - categorized:     "{category}/{repo_name}"
func repoLabel(e manifest.RepoEntry) string {
	if e.IsArchive {
		return "archive/" + repoName(e.Repo)
	}
	if e.IsFlat {
		return repoName(e.Repo)
	}
	return e.Category + "/" + repoName(e.Repo)
}

// repoName extracts the repo name part from "owner/name".
func repoName(repo string) string {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return repo
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Clone every repo in the manifest that does not yet exist locally",
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.SetNoColor(NoColor)

		// 1. Load manifest.
		m, err := manifest.Load(ManifestPath)
		if err != nil {
			return fmt.Errorf("loading manifest: %w", err)
		}

		repos := m.Repos()

		if bootstrapDryRun {
			return runBootstrapDryRun(m)
		}

		fmt.Printf("Bootstrapping %s (concurrency: %d)...\n\n",
			ui.Plural(len(repos), "repo"), Concurrency)

		// 2. Run clones in parallel via worker pool.
		results := worker.PoolWithProgress(
			repos,
			Concurrency,
			worker.PoolOptions{Verb: "cloning"},
			func(entry manifest.RepoEntry) (bootstrapResult, error) {
				return processBootstrapEntry(entry), nil
			},
		)

		// Build a lookup from repo path (LocalPath) to result for ordered display.
		resultMap := make(map[string]bootstrapResult, len(results))
		for _, r := range results {
			resultMap[r.Value.Entry.LocalPath] = r.Value
		}

		// 3. Print output grouped by owner in manifest order.
		var cloned, existed, failed int
		for _, ownerGroup := range m.Owners() {
			fmt.Println(ownerGroup.Name)
			for _, entry := range ownerGroup.Repos {
				res := resultMap[entry.LocalPath]
				label := repoLabel(entry)
				switch res.Status {
				case bsCloned:
					cloned++
					fmt.Printf("  %s  %s\n", ui.PadRight(label, 24), ui.SymbolOK()+" cloned")
				case bsAlreadyExists:
					existed++
					fmt.Printf("  %s  %s\n", ui.PadRight(label, 24), ui.SymbolOK()+" already exists")
				case bsFailed:
					failed++
					fmt.Printf("  %s  %s\n", ui.PadRight(label, 24), ui.SymbolError()+" FAILED: "+res.ErrMsg)
				}
			}
		}

		// 4. Summary.
		fmt.Println()
		fmt.Println(ui.SummaryLine(fmt.Sprintf("%s, %s, %s",
			ui.Plural(cloned, "cloned"),
			pluralExisted(existed),
			ui.Plural(failed, "failed"),
		)))

		if failed > 0 {
			os.Exit(2)
		}
		return nil
	},
}

// runBootstrapDryRun prints what would be cloned without performing any operations.
func runBootstrapDryRun(m *manifest.Manifest) error {
	for _, ownerGroup := range m.Owners() {
		fmt.Println(ownerGroup.Name)
		for _, entry := range ownerGroup.Repos {
			label := repoLabel(entry)
			info, err := os.Stat(entry.LocalPath)
			var action string
			if err == nil {
				if info.IsDir() {
					if git.IsRepo(entry.LocalPath) {
						action = "already exists — would skip"
					} else {
						action = ui.SymbolError() + " ERROR: directory exists but is not a git repo"
					}
				} else {
					action = ui.SymbolError() + " ERROR: path exists and is not a directory"
				}
			} else if os.IsNotExist(err) {
				action = "would clone " + entry.CloneURL
			} else {
				action = ui.SymbolError() + " ERROR: " + err.Error()
			}
			fmt.Printf("  %s  %s\n", ui.PadRight(label, 24), action)
		}
	}
	// --dry-run always exits 0.
	return nil
}

// processBootstrapEntry determines what to do with a single repo entry and does it.
func processBootstrapEntry(entry manifest.RepoEntry) bootstrapResult {
	info, err := os.Stat(entry.LocalPath)
	if err == nil {
		// Path exists.
		if !info.IsDir() {
			return bootstrapResult{
				Entry:  entry,
				Status: bsFailed,
				ErrMsg: "path exists but is not a directory",
			}
		}
		if git.IsRepo(entry.LocalPath) {
			return bootstrapResult{Entry: entry, Status: bsAlreadyExists}
		}
		return bootstrapResult{
			Entry:  entry,
			Status: bsFailed,
			ErrMsg: "directory exists but is not a git repo",
		}
	}

	if !os.IsNotExist(err) {
		return bootstrapResult{
			Entry:  entry,
			Status: bsFailed,
			ErrMsg: err.Error(),
		}
	}

	// Path does not exist — create parent dirs and clone.
	parentDir := filepath.Dir(entry.LocalPath)
	if mkErr := os.MkdirAll(parentDir, 0755); mkErr != nil {
		return bootstrapResult{
			Entry:  entry,
			Status: bsFailed,
			ErrMsg: "could not create parent directory: " + mkErr.Error(),
		}
	}

	if cloneErr := git.Clone(entry.CloneURL, entry.LocalPath); cloneErr != nil {
		return bootstrapResult{
			Entry:  entry,
			Status: bsFailed,
			ErrMsg: cloneErr.Error(),
		}
	}

	return bootstrapResult{Entry: entry, Status: bsCloned}
}

// pluralExisted formats the "already existed" count correctly.
func pluralExisted(n int) string {
	if n == 1 {
		return "1 already existed"
	}
	return fmt.Sprintf("%d already existed", n)
}

func init() {
	bootstrapCmd.Flags().BoolVar(&bootstrapDryRun, "dry-run", false,
		"show what would be cloned without cloning")
	rootCmd.AddCommand(bootstrapCmd)
}
