package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
