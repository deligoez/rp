package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/output"
	"github.com/deligoez/rp/internal/ui"
	"github.com/deligoez/rp/internal/worker"
)

// syncAction classifies the outcome of a single repo sync.
type syncAction int

const (
	syncActionPulled     syncAction = iota
	syncActionUpToDate
	syncActionCloned
	syncActionSkipped
	syncActionFailed
	// dry-run variants
	syncActionWouldPull
	syncActionWouldSkip
	syncActionWouldClone
)

// syncSkipReason classifies why a repo was skipped.
type syncSkipReason int

const (
	syncSkipNone     syncSkipReason = iota
	syncSkipDirty
	syncSkipUnpushed
	syncSkipDiverged
	syncSkipNoUpstream
	syncSkipNotARepo
)

// syncResult holds the outcome of processing a single repo during sync.
type syncResult struct {
	label      string
	status     string // human-readable display string
	exitCode   int    // 0, 1, or 2

	// Structured fields for JSON output — populated by processSyncRepo.
	repo       string         // "owner/name"
	action     syncAction
	newCommits int            // action=pulled
	skipReason syncSkipReason // action=skipped
	dirtyFiles int            // reason=dirty
	ahead      int            // reason=unpushed
	branch     string         // reason=unpushed
	errMsg     string         // action=failed
}

// syncSummaryJSON is the JSON summary for the sync command.
type syncSummaryJSON struct {
	Pulled   int `json:"pulled"`
	UpToDate int `json:"up_to_date"`
	Cloned   int `json:"cloned"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
	Total    int `json:"total"`
}

// syncRepoJSON is the per-repo JSON entry for the sync command.
type syncRepoJSON struct {
	Repo       string `json:"repo"`
	Action     string `json:"action"`
	NewCommits int    `json:"new_commits,omitempty"`
	Reason     string `json:"reason,omitempty"`
	DirtyFiles int    `json:"dirty_files,omitempty"`
	Ahead      int    `json:"ahead,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Error      string `json:"error,omitempty"`
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
		if output.IsJSON() {
			output.PrintErrorAndExit("sync", fmt.Errorf("loading manifest: %w", err))
		}
		return fmt.Errorf("loading manifest: %w", err)
	}

	repos := m.Repos()

	// Apply --filter flag.
	if len(Filters) > 0 {
		repos = manifest.FilterRepos(repos, Filters)
	}

	opts := worker.PoolOptions{Verb: "syncing"}
	results := worker.PoolWithProgress(repos, Concurrency, opts, func(entry manifest.RepoEntry) (syncResult, error) {
		label := repoLabel(entry)
		return processSyncRepo(entry, label, syncDryRun), nil
	})

	// JSON output path.
	if output.IsJSON() {
		return buildAndPrintSyncJSON(results, repos, syncDryRun)
	}

	// Human output: print results grouped by owner in manifest order.
	// Use filtered owners so the pos counter stays aligned with results.
	// worker.PoolWithProgress preserves input order so results[i] corresponds to repos[i].
	owners := manifest.FilterOwners(m.Owners(), Filters)
	pos := 0
	for _, owner := range owners {
		fmt.Println(owner.Name)
		for range owner.Repos {
			if pos >= len(results) {
				break
			}
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

	fmt.Printf("-- Summary --\n%s synced, %d need attention\n",
		ui.Plural(len(repos), "repo"),
		needAttention,
	)

	if syncDryRun {
		os.Exit(0)
	}
	if highestExit != 0 {
		os.Exit(highestExit)
	}
	return nil
}

// buildAndPrintSyncJSON constructs the JSON result and calls output.PrintAndExit.
func buildAndPrintSyncJSON(results []worker.Result[syncResult], repos []manifest.RepoEntry, dryRun bool) error {
	summary := syncSummaryJSON{Total: len(repos)}
	repoList := make([]syncRepoJSON, 0, len(results))

	highestExit := 0

	for _, r := range results {
		v := r.Value
		if v.exitCode > highestExit {
			highestExit = v.exitCode
		}

		rj := syncRepoJSON{Repo: v.repo}

		switch v.action {
		case syncActionPulled:
			rj.Action = "pulled"
			rj.NewCommits = v.newCommits
			summary.Pulled++
		case syncActionUpToDate:
			rj.Action = "up_to_date"
			summary.UpToDate++
		case syncActionCloned:
			rj.Action = "cloned"
			summary.Cloned++
		case syncActionSkipped:
			rj.Action = "skipped"
			summary.Skipped++
			switch v.skipReason {
			case syncSkipDirty:
				rj.Reason = "dirty"
				rj.DirtyFiles = v.dirtyFiles
			case syncSkipUnpushed:
				rj.Reason = "unpushed"
				rj.Ahead = v.ahead
				rj.Branch = v.branch
			case syncSkipDiverged:
				rj.Reason = "diverged"
			case syncSkipNoUpstream:
				rj.Reason = "no_upstream"
			case syncSkipNotARepo:
				rj.Reason = "not_a_repo"
			}
		case syncActionFailed:
			rj.Action = "failed"
			rj.Error = v.errMsg
			summary.Failed++
		case syncActionWouldPull:
			rj.Action = "would_pull"
		case syncActionWouldSkip:
			rj.Action = "would_skip"
			switch v.skipReason {
			case syncSkipDirty:
				rj.Reason = "dirty"
				rj.DirtyFiles = v.dirtyFiles
			case syncSkipUnpushed:
				rj.Reason = "unpushed"
				rj.Ahead = v.ahead
				rj.Branch = v.branch
			case syncSkipDiverged:
				rj.Reason = "diverged"
			case syncSkipNoUpstream:
				rj.Reason = "no_upstream"
			case syncSkipNotARepo:
				rj.Reason = "not_a_repo"
			}
		case syncActionWouldClone:
			rj.Action = "would_clone"
		}

		repoList = append(repoList, rj)
	}

	exitCode := 0
	if !dryRun {
		exitCode = highestExit
	}

	output.PrintAndExit(output.SuccessResult{
		Command:  "sync",
		ExitCode: exitCode,
		DryRun:   dryRun,
		Summary:  summary,
		Repos:    repoList,
	})
	return nil // unreachable; PrintAndExit calls os.Exit
}

// processSyncRepo evaluates a single repo entry and returns the appropriate syncResult.
// Evaluation order follows spec §3.3 (first match wins).
func processSyncRepo(entry manifest.RepoEntry, label string, dryRun bool) syncResult {
	path := entry.LocalPath
	repo := entry.Repo

	// 1. Not cloned → clone (create parent dirs first).
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if dryRun {
			return syncResult{
				label:    label,
				status:   "would clone",
				exitCode: 0,
				repo:     repo,
				action:   syncActionWouldClone,
			}
		}
		if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil {
			msg := "could not create parent dirs: " + mkErr.Error()
			return syncResult{
				label:    label,
				status:   ui.SymbolError() + " FAILED: " + msg,
				exitCode: 2,
				repo:     repo,
				action:   syncActionFailed,
				errMsg:   msg,
			}
		}
		if cloneErr := git.Clone(entry.CloneURL, path); cloneErr != nil {
			msg := "git clone: " + cloneErr.Error()
			return syncResult{
				label:    label,
				status:   ui.SymbolError() + " FAILED: " + msg,
				exitCode: 2,
				repo:     repo,
				action:   syncActionFailed,
				errMsg:   msg,
			}
		}
		return syncResult{
			label:    label,
			status:   ui.SymbolOK() + " cloned",
			exitCode: 0,
			repo:     repo,
			action:   syncActionCloned,
		}
	}

	// 2. Path exists but is NOT a git repo.
	if !git.IsRepo(path) {
		msg := "not a git repository"
		return syncResult{
			label:      label,
			status:     ui.SymbolError() + " " + msg,
			exitCode:   2,
			repo:       repo,
			action:     syncActionSkipped,
			skipReason: syncSkipNotARepo,
			errMsg:     msg,
		}
	}

	// Fetch repo status for dirty / unpushed checks.
	repoStatus, err := git.Status(path)
	if err != nil {
		msg := "git status failed: " + err.Error()
		return syncResult{
			label:    label,
			status:   ui.SymbolError() + " " + msg,
			exitCode: 2,
			repo:     repo,
			action:   syncActionFailed,
			errMsg:   msg,
		}
	}

	// 3. Dirty → skip, warn (dirty takes precedence over unpushed).
	if repoStatus.DirtyFiles > 0 {
		detail := "dirty - " + ui.Plural(repoStatus.DirtyFiles, "changed file")
		if dryRun {
			return syncResult{
				label:      label,
				status:     "would skip (" + detail + ")",
				exitCode:   0,
				repo:       repo,
				action:     syncActionWouldSkip,
				skipReason: syncSkipDirty,
				dirtyFiles: repoStatus.DirtyFiles,
			}
		}
		return syncResult{
			label:      label,
			status:     ui.SymbolWarn() + " " + detail,
			exitCode:   1,
			repo:       repo,
			action:     syncActionSkipped,
			skipReason: syncSkipDirty,
			dirtyFiles: repoStatus.DirtyFiles,
		}
	}

	// 4. Unpushed commits (only when HasUpstream && Ahead > 0) → skip, warn.
	if repoStatus.HasUpstream && repoStatus.Ahead > 0 {
		detail := ui.Plural(repoStatus.Ahead, "unpushed commit") + " (" + repoStatus.Branch + ")"
		if dryRun {
			return syncResult{
				label:      label,
				status:     "would skip (" + detail + ")",
				exitCode:   0,
				repo:       repo,
				action:     syncActionWouldSkip,
				skipReason: syncSkipUnpushed,
				ahead:      repoStatus.Ahead,
				branch:     repoStatus.Branch,
			}
		}
		return syncResult{
			label:      label,
			status:     ui.SymbolWarn() + " " + detail,
			exitCode:   1,
			repo:       repo,
			action:     syncActionSkipped,
			skipReason: syncSkipUnpushed,
			ahead:      repoStatus.Ahead,
			branch:     repoStatus.Branch,
		}
	}

	// 5. Clean → git pull --ff-only.
	if dryRun {
		return syncResult{
			label:    label,
			status:   "would pull",
			exitCode: 0,
			repo:     repo,
			action:   syncActionWouldPull,
		}
	}

	pullResult, pullErr := git.Pull(path)
	if pullErr != nil {
		switch {
		case errors.Is(pullErr, git.ErrDiverged):
			return syncResult{
				label:      label,
				status:     ui.SymbolWarn() + " diverged",
				exitCode:   1,
				repo:       repo,
				action:     syncActionSkipped,
				skipReason: syncSkipDiverged,
			}
		case errors.Is(pullErr, git.ErrNoUpstream):
			return syncResult{
				label:      label,
				status:     ui.SymbolWarn() + " no upstream",
				exitCode:   1,
				repo:       repo,
				action:     syncActionSkipped,
				skipReason: syncSkipNoUpstream,
			}
		default:
			msg := classifySyncError(pullErr)
			return syncResult{
				label:    label,
				status:   ui.SymbolWarn() + " " + msg,
				exitCode: 1,
				repo:     repo,
				action:   syncActionFailed,
				errMsg:   msg,
			}
		}
	}

	if pullResult.AlreadyUpToDate {
		return syncResult{
			label:    label,
			status:   ui.SymbolOK() + " up to date",
			exitCode: 0,
			repo:     repo,
			action:   syncActionUpToDate,
		}
	}

	return syncResult{
		label:      label,
		status:     ui.SymbolOK() + " pulled " + ui.Plural(pullResult.NewCommits, "commit"),
		exitCode:   0,
		repo:       repo,
		action:     syncActionPulled,
		newCommits: pullResult.NewCommits,
	}
}

// classifySyncError maps a git pull error to a clean single-line message.
func classifySyncError(err error) string {
	if err == nil {
		return "pull failed"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "not currently on a branch"):
		return "pull failed (detached HEAD)"
	case strings.Contains(s, "exit status 128"):
		return "pull failed (empty repo)"
	default:
		return "pull failed"
	}
}
