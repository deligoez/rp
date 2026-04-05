package deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test 1: Run single command successfully
func TestRunDeps_SingleCommand(t *testing.T) {
	dir := t.TempDir()
	err := RunDeps(dir, []string{"echo hello"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// Test 2: Run multiple commands in order
func TestRunDeps_MultipleCommands(t *testing.T) {
	dir := t.TempDir()
	err := RunDeps(dir, []string{"echo a", "echo b"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// Test 3: Empty command list is a no-op
func TestRunDeps_EmptyCommands(t *testing.T) {
	dir := t.TempDir()
	err := RunDeps(dir, []string{})
	if err != nil {
		t.Fatalf("expected no error for empty commands, got: %v", err)
	}
}

// Test 4: Failing command returns error containing the command name
func TestRunDeps_FailingCommand(t *testing.T) {
	dir := t.TempDir()
	err := RunDeps(dir, []string{"false"})
	if err == nil {
		t.Fatal("expected error from failing command, got nil")
	}
	if !strings.Contains(err.Error(), "false") {
		t.Errorf("expected error to contain command name %q, got: %v", "false", err)
	}
}

// Test 5: Commands run in the specified repo directory
func TestRunDeps_RunsInRepoDir(t *testing.T) {
	dir := t.TempDir()
	markerFile := filepath.Join(dir, "marker.txt")

	err := RunDeps(dir, []string{"pwd > marker.txt"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	content, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("expected marker file to be created, got: %v", err)
	}

	output := strings.TrimSpace(string(content))
	if !strings.Contains(output, dir) {
		t.Errorf("expected pwd output to contain %q, got %q", dir, output)
	}
}

// Test 6: Shell features work (pipes, env vars)
func TestRunDeps_ShellFeaturesWork(t *testing.T) {
	dir := t.TempDir()
	err := RunDeps(dir, []string{"echo $HOME | cat"})
	if err != nil {
		t.Fatalf("expected no error with shell features, got: %v", err)
	}
}

// Test 7: First failure stops execution — subsequent commands do not run
func TestRunDeps_StopsOnFirstFailure(t *testing.T) {
	dir := t.TempDir()
	shouldNotExist := filepath.Join(dir, "should_not_exist")

	err := RunDeps(dir, []string{"false", "touch should_not_exist"})
	if err == nil {
		t.Fatal("expected error from failing command, got nil")
	}

	if _, statErr := os.Stat(shouldNotExist); !os.IsNotExist(statErr) {
		t.Errorf("expected file %q to not exist, but it does", shouldNotExist)
	}
}
