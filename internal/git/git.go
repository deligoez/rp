package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrDiverged   = errors.New("branches have diverged")
	ErrNoUpstream = errors.New("no upstream tracking branch")
)

// PullResult holds the outcome of a git pull operation.
type PullResult struct {
	NewCommits      int
	AlreadyUpToDate bool
}

// RepoStatus describes the current state of a git repository.
type RepoStatus struct {
	Clean       bool
	DirtyFiles  int
	Branch      string
	Ahead       int
	Behind      int
	HasUpstream bool
}

// IsRepo reports whether path contains a .git directory.
func IsRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Clone runs "git clone <url> <path>".
func Clone(url, path string) error {
	cmd := exec.Command("git", "clone", url, path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Status returns the current status of the git repository at path.
func Status(path string) (RepoStatus, error) {
	var s RepoStatus

	// --- Dirty files ---
	out, err := runGit(path, "status", "--porcelain")
	if err != nil {
		return s, err
	}
	var dirtyCount int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			dirtyCount++
		}
	}
	s.DirtyFiles = dirtyCount
	s.Clean = dirtyCount == 0

	// --- Branch ---
	branchOut, err := runGit(path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		// Detached HEAD: fall back to short SHA
		shaOut, err2 := runGit(path, "rev-parse", "--short", "HEAD")
		if err2 != nil {
			return s, err2
		}
		s.Branch = strings.TrimSpace(string(shaOut))
	} else {
		s.Branch = strings.TrimSpace(string(branchOut))
	}

	// --- Ahead / Behind ---
	revOut, err := runGit(path, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		// No upstream configured.
		s.HasUpstream = false
		s.Ahead = 0
		s.Behind = 0
	} else {
		parts := strings.Fields(strings.TrimSpace(string(revOut)))
		if len(parts) == 2 {
			ahead, err1 := strconv.Atoi(parts[0])
			behind, err2 := strconv.Atoi(parts[1])
			if err1 == nil && err2 == nil {
				s.Ahead = ahead
				s.Behind = behind
				s.HasUpstream = true
			}
		}
	}

	return s, nil
}

// LastCommitDate returns the committer date of the most recent commit in the
// repository at path.
func LastCommitDate(path string) (time.Time, error) {
	out, err := exec.Command("git", "-C", path, "log", "-1", "--format=%cI").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("git log: %w", err)
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

// runGit executes a git command with -C path and returns the combined stdout.
func runGit(path string, args ...string) ([]byte, error) {
	fullArgs := append([]string{"-C", path}, args...)
	cmd := exec.Command("git", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// Pull performs a fast-forward-only pull on the repository at path.
func Pull(path string) (PullResult, error) {
	// 1. Record HEAD before pull.
	oldHeadBytes, err := runGit(path, "rev-parse", "HEAD")
	if err != nil {
		return PullResult{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	oldHead := strings.TrimSpace(string(oldHeadBytes))

	// 2. Run git pull --ff-only, capturing stderr for error classification.
	pullArgs := append([]string{"-C", path}, "pull", "--ff-only")
	pullCmd := exec.Command("git", pullArgs...)
	var pullStdout, pullStderr bytes.Buffer
	pullCmd.Stdout = &pullStdout
	pullCmd.Stderr = &pullStderr
	if err := pullCmd.Run(); err != nil {
		stderr := pullStderr.String()
		if strings.Contains(stderr, "diverged") || strings.Contains(strings.ToLower(stderr), "not possible to fast-forward") {
			return PullResult{}, ErrDiverged
		}
		if strings.Contains(stderr, "no tracking information") || strings.Contains(stderr, "no such ref") {
			return PullResult{}, ErrNoUpstream
		}
		return PullResult{}, fmt.Errorf("git pull --ff-only: %w\n%s", err, stderr)
	}

	// 3. Record HEAD after pull.
	newHeadBytes, err := runGit(path, "rev-parse", "HEAD")
	if err != nil {
		return PullResult{}, fmt.Errorf("git rev-parse HEAD (post-pull): %w", err)
	}
	newHead := strings.TrimSpace(string(newHeadBytes))

	// 4. Already up to date.
	if oldHead == newHead {
		return PullResult{AlreadyUpToDate: true}, nil
	}

	// 5. Count new commits.
	countBytes, err := runGit(path, "rev-list", oldHead+"..HEAD", "--count")
	if err != nil {
		return PullResult{}, fmt.Errorf("git rev-list count: %w", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countBytes)))
	if err != nil {
		return PullResult{}, fmt.Errorf("parsing commit count: %w", err)
	}

	return PullResult{NewCommits: count}, nil
}
