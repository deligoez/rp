package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
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
	bsCloned        bootstrapStatus = iota
	bsAlreadyExists
	bsFailed
	bsWouldClone
	bsWouldSkip
)

// bootstrapRepoJSON is the per-repo JSON representation for bootstrap output.
type bootstrapRepoJSON struct {
	Repo      string `json:"repo"`
	Action    string `json:"action"` // "cloned", "already_exists", "failed", "would_clone", "would_skip"
	LocalPath string `json:"local_path"`
	Error     string `json:"error,omitempty"`
}

// bootstrapSummaryJSON is the summary JSON representation for bootstrap output.
type bootstrapSummaryJSON struct {
	Cloned         int `json:"cloned"`
	AlreadyExisted int `json:"already_existed"`
	Failed         int `json:"failed"`
	Total          int `json:"total"`
}

// repoLabel returns the display label for a repo entry following the spec convention:
//   - archive entries: "archive/{repo_name}"
//   - flat owners:     "{repo_name}"
//   - categorized:     "{category}/{repo_name}"
// repoLabel returns the display label for a repo entry:
//   - flat owners:     "{repo_name}"
//   - categorized:     "{category}/{repo_name}"
func repoLabel(e manifest.RepoEntry) string {
	if e.Category == "" {
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

		repos := manifest.FilterRepos(m.Repos(), Filters)

		if bootstrapDryRun {
			if output.IsJSON() {
				return runBootstrapDryRunJSON(repos)
			}
			return runBootstrapDryRun(m, repos)
		}

		if output.IsJSON() {
			return runBootstrapJSON(repos)
		}

		fmt.Printf("Bootstrapping %s (concurrency: %d)...\n\n",
			ui.Plural(len(repos), "repo"), Concurrency)

		var cloned, existed, failed int

		// 2. Run clones in parallel via worker pool, streaming per-repo lines.
		_ = worker.PoolWithLiveLog(
			repos,
			Concurrency,
			func(entry manifest.RepoEntry) (bootstrapResult, error) {
				return processBootstrapEntry(entry), nil
			},
			func(n, total int, entry manifest.RepoEntry, res bootstrapResult, _ error) {
				label := ui.PadRight(repoLabel(entry), 24)
				switch res.Status {
				case bsCloned:
					cloned++
					fmt.Printf("[%d/%d] %s cloned     %s\n", n, total, ui.SymbolOK(), label)
				case bsAlreadyExists:
					existed++
					fmt.Printf("[%d/%d] -- exists     %s\n", n, total, label)
				case bsFailed:
					failed++
					fmt.Printf("[%d/%d] %s FAILED     %s: %s\n", n, total, ui.SymbolError(), label, res.ErrMsg)
				}
			},
		)

		// 3. Summary.
		fmt.Println()
		fmt.Println(ui.SummaryLine(fmt.Sprintf("%s, %s, %s",
			fmt.Sprintf("%d cloned", cloned),
			pluralExisted(existed),
			fmt.Sprintf("%d failed", failed),
		)))

		if failed > 0 {
			os.Exit(2)
		}
		return nil
	},
}

// runBootstrapDryRun prints what would be cloned without performing any operations.
func runBootstrapDryRun(m *manifest.Manifest, filteredRepos []manifest.RepoEntry) error {
	// Build a set of filtered repo paths for quick lookup.
	included := make(map[string]bool, len(filteredRepos))
	for _, r := range filteredRepos {
		included[r.LocalPath] = true
	}

	for _, ownerGroup := range manifest.FilterOwners(m.Owners(), Filters) {
		fmt.Println(ownerGroup.Name)
		for _, entry := range ownerGroup.Repos {
			if !included[entry.LocalPath] {
				continue
			}
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

// runBootstrapJSON runs bootstrap with JSON output.
func runBootstrapJSON(repos []manifest.RepoEntry) error {
	results := worker.PoolWithProgress(
		repos,
		Concurrency,
		worker.PoolOptions{Verb: "cloning"},
		func(entry manifest.RepoEntry) (bootstrapResult, error) {
			return processBootstrapEntry(entry), nil
		},
	)

	var cloned, existed, failed int
	repoList := make([]bootstrapRepoJSON, 0, len(results))
	for _, r := range results {
		res := r.Value
		rj := bootstrapRepoJSON{
			Repo:      res.Entry.Repo,
			LocalPath: res.Entry.LocalPath,
			Error:     res.ErrMsg,
		}
		switch res.Status {
		case bsCloned:
			cloned++
			rj.Action = "cloned"
		case bsAlreadyExists:
			existed++
			rj.Action = "already_exists"
		case bsFailed:
			failed++
			rj.Action = "failed"
		}
		repoList = append(repoList, rj)
	}

	exitCode := 0
	if failed > 0 {
		exitCode = 2
	}

	result := output.SuccessResult{
		Command:  "bootstrap",
		ExitCode: exitCode,
		DryRun:   false,
		Summary: bootstrapSummaryJSON{
			Cloned:         cloned,
			AlreadyExisted: existed,
			Failed:         failed,
			Total:          len(results),
		},
		Repos: repoList,
	}
	output.PrintAndExit(result)
	return nil
}

// runBootstrapDryRunJSON runs bootstrap dry-run with JSON output.
func runBootstrapDryRunJSON(repos []manifest.RepoEntry) error {
	var wouldClone, wouldSkip int
	repoList := make([]bootstrapRepoJSON, 0, len(repos))
	for _, entry := range repos {
		rj := bootstrapRepoJSON{
			Repo:      entry.Repo,
			LocalPath: entry.LocalPath,
		}
		info, err := os.Stat(entry.LocalPath)
		if err == nil && info.IsDir() && git.IsRepo(entry.LocalPath) {
			wouldSkip++
			rj.Action = "would_skip"
		} else {
			wouldClone++
			rj.Action = "would_clone"
		}
		repoList = append(repoList, rj)
	}

	result := output.SuccessResult{
		Command:  "bootstrap",
		ExitCode: 0,
		DryRun:   true,
		Summary: bootstrapSummaryJSON{
			Cloned:         wouldClone,
			AlreadyExisted: wouldSkip,
			Failed:         0,
			Total:          len(repos),
		},
		Repos: repoList,
	}
	output.PrintAndExit(result)
	return nil
}

// processBootstrapDryRun checks what would happen without cloning.
func processBootstrapDryRun(entry manifest.RepoEntry) bootstrapResult {
	info, err := os.Stat(entry.LocalPath)
	if err == nil && info.IsDir() && git.IsRepo(entry.LocalPath) {
		return bootstrapResult{Entry: entry, Status: bsWouldSkip}
	}
	return bootstrapResult{Entry: entry, Status: bsWouldClone}
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
