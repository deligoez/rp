package git_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deligoez/rp/internal/git"
)

// initRepoWithCommit creates a temp directory, initialises a git repo,
// configures a test identity, and makes an initial commit.
func initRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "init")
	return dir
}

// run executes an arbitrary command in dir and fails the test on error.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v failed: %s\n%s", args, err, out)
	}
}

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

// Test #6: LastCommitDate returns a time within the last minute for a freshly
// created commit.
func TestLastCommitDate(t *testing.T) {
	t.Parallel()

	dir := initRepoWithCommit(t)

	got, err := git.LastCommitDate(dir)
	if err != nil {
		t.Fatalf("LastCommitDate(%q) returned error: %v", dir, err)
	}

	now := time.Now()
	if got.After(now) {
		t.Errorf("LastCommitDate = %v, which is in the future (now = %v)", got, now)
	}
	if now.Sub(got) > time.Minute {
		t.Errorf("LastCommitDate = %v, which is more than 1 minute before now (%v)", got, now)
	}
}

// addCommitToBare creates a temporary clone of the bare repo, makes a commit,
// and pushes it back so the bare repo advances by one commit.
func addCommitToBare(t *testing.T, bareDir string) {
	t.Helper()
	tmpClone := filepath.Join(t.TempDir(), "pusher")
	run(t, t.TempDir(), "git", "clone", bareDir, tmpClone)
	run(t, tmpClone, "git", "config", "user.email", "test@test.com")
	run(t, tmpClone, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(tmpClone, "extra.txt"), []byte("extra"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(t, tmpClone, "git", "add", ".")
	run(t, tmpClone, "git", "commit", "-m", "remote commit")
	run(t, tmpClone, "git", "push")
}

// newBareRepoWithCommit initialises a bare repo that already contains an
// initial commit (via a temporary working clone).
func newBareRepoWithCommit(t *testing.T) string {
	t.Helper()
	bareDir := t.TempDir()
	run(t, bareDir, "git", "init", "--bare", bareDir)

	// Seed it with an initial commit via a temporary clone.
	seedClone := filepath.Join(t.TempDir(), "seed")
	run(t, t.TempDir(), "git", "clone", bareDir, seedClone)
	run(t, seedClone, "git", "config", "user.email", "test@test.com")
	run(t, seedClone, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(seedClone, "seed.txt"), []byte("seed"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(t, seedClone, "git", "add", ".")
	run(t, seedClone, "git", "commit", "-m", "seed commit")
	run(t, seedClone, "git", "push")
	return bareDir
}

// Test #8: Pull fast-forward success — one new commit arrives from upstream.
func TestPull_FFSuccess(t *testing.T) {
	t.Parallel()

	bareDir := newBareRepoWithCommit(t)

	// First clone: the one we will call Pull on.
	cloneDir := filepath.Join(t.TempDir(), "clone1")
	run(t, t.TempDir(), "git", "clone", bareDir, cloneDir)

	// Push a new commit to bare via a second clone.
	addCommitToBare(t, bareDir)

	result, err := git.Pull(cloneDir)
	if err != nil {
		t.Fatalf("Pull() returned unexpected error: %v", err)
	}
	if result.AlreadyUpToDate {
		t.Errorf("Pull() AlreadyUpToDate = true, want false")
	}
	if result.NewCommits != 1 {
		t.Errorf("Pull() NewCommits = %d, want 1", result.NewCommits)
	}
}

// Test #9: Pull already up to date — no new commits on upstream.
func TestPull_AlreadyUpToDate(t *testing.T) {
	t.Parallel()

	bareDir := newBareRepoWithCommit(t)

	cloneDir := filepath.Join(t.TempDir(), "clone2")
	run(t, t.TempDir(), "git", "clone", bareDir, cloneDir)

	result, err := git.Pull(cloneDir)
	if err != nil {
		t.Fatalf("Pull() returned unexpected error: %v", err)
	}
	if !result.AlreadyUpToDate {
		t.Errorf("Pull() AlreadyUpToDate = false, want true")
	}
	if result.NewCommits != 0 {
		t.Errorf("Pull() NewCommits = %d, want 0", result.NewCommits)
	}
}

// Test #10: Pull diverged — local and remote have both advanced independently.
func TestPull_Diverged(t *testing.T) {
	t.Parallel()

	bareDir := newBareRepoWithCommit(t)

	// Clone that will diverge.
	cloneDir := filepath.Join(t.TempDir(), "clone3")
	run(t, t.TempDir(), "git", "clone", bareDir, cloneDir)
	run(t, cloneDir, "git", "config", "user.email", "test@test.com")
	run(t, cloneDir, "git", "config", "user.name", "Test")

	// Push a remote commit so bare advances.
	addCommitToBare(t, bareDir)

	// Make a local commit so the clone also advances (diverged).
	if err := os.WriteFile(filepath.Join(cloneDir, "local.txt"), []byte("local"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(t, cloneDir, "git", "add", ".")
	run(t, cloneDir, "git", "commit", "-m", "local commit")

	result, err := git.Pull(cloneDir)
	if result != (git.PullResult{}) {
		t.Errorf("Pull() PullResult = %+v, want zero value on error", result)
	}
	if err == nil {
		t.Fatal("Pull() returned nil error, want ErrDiverged")
	}
	if !errors.Is(err, git.ErrDiverged) {
		t.Errorf("Pull() error = %v, want errors.Is(err, ErrDiverged)", err)
	}
}

// Test #3: Status on a clean repo returns Clean:true and a non-empty branch name.
func TestStatus_Clean(t *testing.T) {
	t.Parallel()

	dir := initRepoWithCommit(t)

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !s.Clean {
		t.Errorf("Clean = false, want true")
	}
	if s.DirtyFiles != 0 {
		t.Errorf("DirtyFiles = %d, want 0", s.DirtyFiles)
	}
	if s.Branch == "" {
		t.Errorf("Branch is empty, want a branch name")
	}
}

// Test #4: Status on a dirty repo (modified tracked file) returns Clean:false, DirtyFiles:1.
func TestStatus_Dirty(t *testing.T) {
	t.Parallel()

	dir := initRepoWithCommit(t)

	// Modify the tracked file to make the working tree dirty.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if s.Clean {
		t.Errorf("Clean = true, want false")
	}
	if s.DirtyFiles != 1 {
		t.Errorf("DirtyFiles = %d, want 1", s.DirtyFiles)
	}
}

// Test #7: Status with no remote configured reports HasUpstream:false, Ahead:0, Behind:0.
func TestStatus_NoUpstream(t *testing.T) {
	t.Parallel()

	dir := initRepoWithCommit(t)

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if s.HasUpstream {
		t.Errorf("HasUpstream = true, want false")
	}
	if s.Ahead != 0 {
		t.Errorf("Ahead = %d, want 0", s.Ahead)
	}
	if s.Behind != 0 {
		t.Errorf("Behind = %d, want 0", s.Behind)
	}
}

// Test #11: Status for a clone with one local commit not yet pushed reports Ahead:1, Behind:0.
func TestStatus_Ahead(t *testing.T) {
	t.Parallel()

	originDir := initRepoWithCommit(t)

	cloneDir := filepath.Join(t.TempDir(), "clone")
	run(t, t.TempDir(), "git", "clone", originDir, cloneDir)
	run(t, cloneDir, "git", "config", "user.email", "test@test.com")
	run(t, cloneDir, "git", "config", "user.name", "Test")

	// Add a local commit in the clone (not pushed).
	if err := os.WriteFile(filepath.Join(cloneDir, "extra.txt"), []byte("extra"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(t, cloneDir, "git", "add", ".")
	run(t, cloneDir, "git", "commit", "-m", "local commit")

	s, err := git.Status(cloneDir)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !s.HasUpstream {
		t.Errorf("HasUpstream = false, want true")
	}
	if s.Ahead != 1 {
		t.Errorf("Ahead = %d, want 1", s.Ahead)
	}
	if s.Behind != 0 {
		t.Errorf("Behind = %d, want 0", s.Behind)
	}
}

// Test #12: Status for a clone behind its upstream by one commit reports Ahead:0, Behind:1.
func TestStatus_Behind(t *testing.T) {
	t.Parallel()

	originDir := initRepoWithCommit(t)

	cloneDir := filepath.Join(t.TempDir(), "clone")
	run(t, t.TempDir(), "git", "clone", originDir, cloneDir)
	run(t, cloneDir, "git", "config", "user.email", "test@test.com")
	run(t, cloneDir, "git", "config", "user.name", "Test")

	// Add a new commit directly to origin after the clone was taken.
	if err := os.WriteFile(filepath.Join(originDir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(t, originDir, "git", "add", ".")
	run(t, originDir, "git", "commit", "-m", "upstream commit")

	// Fetch without merging so the clone is behind but not diverged.
	run(t, cloneDir, "git", "fetch", "origin")

	s, err := git.Status(cloneDir)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !s.HasUpstream {
		t.Errorf("HasUpstream = false, want true")
	}
	if s.Ahead != 0 {
		t.Errorf("Ahead = %d, want 0", s.Ahead)
	}
	if s.Behind != 1 {
		t.Errorf("Behind = %d, want 1", s.Behind)
	}
}

// Test #13: Status in detached HEAD state reports Branch as a short SHA (hex chars only).
func TestStatus_DetachedHEAD(t *testing.T) {
	t.Parallel()

	dir := initRepoWithCommit(t)

	// Detach HEAD at the current commit.
	run(t, dir, "git", "checkout", "--detach")

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if s.Branch == "" {
		t.Errorf("Branch is empty, want a short SHA")
	}
	for _, c := range s.Branch {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("Branch %q contains non-hex character %q; expected a short SHA", s.Branch, c)
			break
		}
	}
}

// --- QA Regression Tests ---

// QA-R7: Status with untracked file counts as dirty
func TestQA_StatusUntrackedDirty(t *testing.T) {
	dir := initRepoWithCommit(t)
	// Add untracked file (not git-added)
	os.WriteFile(filepath.Join(dir, "UNTRACKED.txt"), []byte("new"), 0644)

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if s.Clean {
		t.Error("expected Clean=false for repo with untracked file")
	}
	if s.DirtyFiles != 1 {
		t.Errorf("expected DirtyFiles=1, got %d", s.DirtyFiles)
	}
}

// QA-R8: Status with staged-only change counts as dirty
func TestQA_StatusStagedDirty(t *testing.T) {
	dir := initRepoWithCommit(t)
	// Modify and stage (but don't commit)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("modified"), 0644)
	run(t, dir, "git", "add", "f.txt")

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if s.Clean {
		t.Error("expected Clean=false for repo with staged changes")
	}
	if s.DirtyFiles != 1 {
		t.Errorf("expected DirtyFiles=1, got %d", s.DirtyFiles)
	}
}

// QA-R9: Status on feature branch shows branch name
func TestQA_StatusFeatureBranch(t *testing.T) {
	dir := initRepoWithCommit(t)
	run(t, dir, "git", "checkout", "-b", "feature/test-branch")

	s, err := git.Status(dir)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if s.Branch != "feature/test-branch" {
		t.Errorf("expected branch=feature/test-branch, got %s", s.Branch)
	}
}

// QA-R10: Pull on empty repo (no commits) should return error
func TestQA_PullEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")

	_, err := git.Pull(dir)
	if err == nil {
		t.Error("expected error pulling from empty repo (no commits)")
	}
}
