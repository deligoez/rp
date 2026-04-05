package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
)

// syncResult holds the outcome of processing a single repo during sync.
type syncResult struct {
	label    string
	owner    string
	status   string // e.g. "OK up to date", "!! dirty - 3 changed files"
	exitCode int    // 0, 1, or 2
}

var syncDryRun bool

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Pull all clean repos, skip dirty ones, report status",
	RunE:  runSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "show what would happen without pulling")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	ui.SetNoColor(NoColor)

	m, err := manifest.Load(ManifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	repos := m.Repos()

	opts := worker.PoolOptions{Verb: "syncing"}
	results := worker.PoolWithProgress(repos, Concurrency, opts, func(entry manifest.RepoEntry) (syncResult, error) {
		label := repoLabel(entry)
		return processSyncRepo(entry, label, syncDryRun), nil
	})

	// Print results grouped by owner in manifest order.
	// worker.PoolWithProgress preserves input order so results[i] corresponds to repos[i].
	owners := m.Owners()
	pos := 0
	for _, owner := range owners {
		fmt.Println(owner.Name)
		for range owner.Repos {
			r := results[pos].Value
			fmt.Printf("  %-30s %s\n", r.label, r.status)
			pos++
		}
		fmt.Println()
	}

	// Compute summary counts.
	highestExit := 0
	needAttention := 0
	for _, r := range results {
		if r.Value.exitCode > highestExit {
			highestExit = r.Value.exitCode
		}
		if r.Value.exitCode > 0 {
			needAttention++
		}
	}

	fmt.Printf("-- Summary --\n%s, %s\n",
		ui.Plural(len(repos), "repo")+" synced",
		ui.Plural(needAttention, "repo")+" need attention",
	)

	if syncDryRun {
		os.Exit(0)
	}
	if highestExit != 0 {
		os.Exit(highestExit)
	}
	return nil
}

// processSyncRepo evaluates a single repo entry and returns the appropriate syncResult.
// Evaluation order follows spec §3.3 (first match wins).
func processSyncRepo(entry manifest.RepoEntry, label string, dryRun bool) syncResult {
	path := entry.LocalPath
	owner := entry.Owner

	// 1. Not cloned → clone (create parent dirs first).
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if dryRun {
			return syncResult{label: label, owner: owner, status: "would clone", exitCode: 0}
		}
		if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil {
			return syncResult{
				label:    label,
				owner:    owner,
				status:   ui.SymbolError() + " FAILED: could not create parent dirs: " + mkErr.Error(),
				exitCode: 2,
			}
		}
		if cloneErr := git.Clone(entry.CloneURL, path); cloneErr != nil {
			return syncResult{
				label:    label,
				owner:    owner,
				status:   ui.SymbolError() + " FAILED: git clone: " + cloneErr.Error(),
				exitCode: 2,
			}
		}
		return syncResult{label: label, owner: owner, status: ui.SymbolOK() + " cloned", exitCode: 0}
	}

	// 2. Path exists but is NOT a git repo.
	if !git.IsRepo(path) {
		return syncResult{
			label:    label,
			owner:    owner,
			status:   ui.SymbolError() + " not a git repository",
			exitCode: 2,
		}
	}

	// Fetch repo status for dirty / unpushed checks.
	repoStatus, err := git.Status(path)
	if err != nil {
		return syncResult{
			label:    label,
			owner:    owner,
			status:   ui.SymbolError() + " git status failed: " + err.Error(),
			exitCode: 2,
		}
	}

	// 3. Dirty → skip, warn (dirty takes precedence over unpushed).
	if repoStatus.DirtyFiles > 0 {
		detail := "dirty - " + ui.Plural(repoStatus.DirtyFiles, "changed file")
		if dryRun {
			return syncResult{label: label, owner: owner, status: "would skip (" + detail + ")", exitCode: 0}
		}
		return syncResult{label: label, owner: owner, status: ui.SymbolWarn() + " " + detail, exitCode: 1}
	}

	// 4. Unpushed commits (only when HasUpstream && Ahead > 0) → skip, warn.
	if repoStatus.HasUpstream && repoStatus.Ahead > 0 {
		detail := ui.Plural(repoStatus.Ahead, "unpushed commit") + " (" + repoStatus.Branch + ")"
		if dryRun {
			return syncResult{label: label, owner: owner, status: "would skip (" + detail + ")", exitCode: 0}
		}
		return syncResult{label: label, owner: owner, status: ui.SymbolWarn() + " " + detail, exitCode: 1}
	}

	// 5. Clean → git pull --ff-only.
	if dryRun {
		return syncResult{label: label, owner: owner, status: "would pull", exitCode: 0}
	}

	pullResult, pullErr := git.Pull(path)
	if pullErr != nil {
		switch {
		case errors.Is(pullErr, git.ErrDiverged):
			return syncResult{label: label, owner: owner, status: ui.SymbolWarn() + " diverged", exitCode: 1}
		case errors.Is(pullErr, git.ErrNoUpstream):
			return syncResult{label: label, owner: owner, status: ui.SymbolWarn() + " no upstream", exitCode: 1}
		default:
			return syncResult{
				label:    label,
				owner:    owner,
				status:   ui.SymbolWarn() + " pull failed: " + pullErr.Error(),
				exitCode: 1,
			}
		}
	}

	if pullResult.AlreadyUpToDate {
		return syncResult{label: label, owner: owner, status: ui.SymbolOK() + " up to date", exitCode: 0}
	}

	return syncResult{
		label:    label,
		owner:    owner,
		status:   ui.SymbolOK() + " pulled " + ui.Plural(pullResult.NewCommits, "commit"),
		exitCode: 0,
	}
}
