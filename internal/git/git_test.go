package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/deligoez/rp/internal/git"
)

// Test #1: IsRepo returns true for a directory that has been git-initialised.
func TestIsRepo_GitDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	if !git.IsRepo(dir) {
		t.Errorf("IsRepo(%q) = false, want true", dir)
	}
}

// Test #2: IsRepo returns false for a plain directory with no .git folder.
func TestIsRepo_NonGitDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if git.IsRepo(dir) {
		t.Errorf("IsRepo(%q) = true, want false", dir)
	}
}

// Test #5: Clone creates the target directory and it is a valid git repository.
func TestClone(t *testing.T) {
	t.Parallel()

	// Create a local bare repo to clone from.
	bareDir := t.TempDir()
	if err := exec.Command("git", "init", "--bare", bareDir).Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	// Destination for the clone.
	cloneDir := filepath.Join(t.TempDir(), "cloned")

	if err := git.Clone(bareDir, cloneDir); err != nil {
		t.Fatalf("Clone(%q, %q) returned error: %v", bareDir, cloneDir, err)
	}

	// Target directory must exist.
	if _, err := os.Stat(cloneDir); os.IsNotExist(err) {
		t.Fatalf("clone target directory %q does not exist", cloneDir)
	}

	// Target directory must be a git repo.
	if !git.IsRepo(cloneDir) {
		t.Errorf("IsRepo(%q) = false after Clone, want true", cloneDir)
	}
}
