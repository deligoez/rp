package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Binary build helpers
// ---------------------------------------------------------------------------

// testBinaryDir holds a temp directory that lives for the duration of the
// test binary process (cleaned up in TestMain). We store the rp binary here
// so it is not deleted when any individual test's TempDir is cleaned.
var testBinaryDir string

// TestMain builds the rp binary once before any tests run and removes it
// after all tests finish.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rp-json-test-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for binary: %v\n", err)
		os.Exit(1)
	}
	testBinaryDir = dir

	bin := filepath.Join(dir, "rp")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = ".." // project root
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s\n", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// binaryForTest returns the path to the pre-built rp binary.
func binaryForTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(testBinaryDir, "rp")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not found at %s: %v", bin, err)
	}
	return bin
}

// runRPJSON executes the binary with --json and the given extra args, parses
// the stdout as a JSON object, and returns the result.
// Exit-code errors from the subprocess are intentionally ignored; the JSON
// envelope carries its own exit_code field.
func runRPJSON(t *testing.T, binary, manifestPath string, args ...string) map[string]interface{} {
	t.Helper()
	all := append([]string{"--json", "--manifest", manifestPath}, args...)
	cmd := exec.Command(binary, all...)
	out, _ := cmd.Output() // ignore exit code; we inspect the JSON
	if len(out) == 0 {
		// Capture stderr for better diagnostics.
		cmd2 := exec.Command(binary, all...)
		stderr, _ := cmd2.CombinedOutput()
		t.Fatalf("empty output from binary\nstderr+stdout: %s", stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON from binary: %v\nraw output: %s", err, out)
	}
	return result
}

// ---------------------------------------------------------------------------
// Manifest / git repo helpers
// ---------------------------------------------------------------------------

// writeManifest creates a manifest YAML file in dir and returns its path.
func writeManifest(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	return path
}

// initGitRepo initialises a bare git repo with a single commit at repoDir and
// returns the path.
func initGitRepo(t *testing.T, repoDir string) string {
	t.Helper()
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", repoDir, err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test User")
	// Create an initial commit so LastCommitDate is available.
	readmePath := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	return repoDir
}

// makeDirtyGitRepo creates a git repo with one uncommitted change and returns
// its path.
func makeDirtyGitRepo(t *testing.T, repoDir string) string {
	t.Helper()
	initGitRepo(t, repoDir)
	// Write a second file without staging/committing it → dirty.
	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatalf("write dirty.txt: %v", err)
	}
	return repoDir
}

// assertKey returns result[key] and fails the test if the key is absent.
func assertKey(t *testing.T, result map[string]interface{}, key string) interface{} {
	t.Helper()
	v, ok := result[key]
	if !ok {
		t.Fatalf("expected key %q in JSON result, got keys: %v", key, mapKeys(result))
	}
	return v
}

// assertNoKey fails the test if result contains the given key.
func assertNoKey(t *testing.T, result map[string]interface{}, key string) {
	t.Helper()
	if _, ok := result[key]; ok {
		t.Fatalf("unexpected key %q in JSON result", key)
	}
}

// assertString asserts result[key] equals want.
func assertString(t *testing.T, result map[string]interface{}, key, want string) {
	t.Helper()
	v := assertKey(t, result, key)
	s, ok := v.(string)
	if !ok {
		t.Fatalf("key %q: expected string, got %T (%v)", key, v, v)
	}
	if s != want {
		t.Fatalf("key %q: want %q, got %q", key, want, s)
	}
}

// assertFloat asserts result[key] equals want (JSON numbers decode to float64).
func assertFloat(t *testing.T, result map[string]interface{}, key string, want float64) {
	t.Helper()
	v := assertKey(t, result, key)
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("key %q: expected number, got %T (%v)", key, v, v)
	}
	if f != want {
		t.Fatalf("key %q: want %v, got %v", key, want, f)
	}
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// processExitCode returns the process exit code for the given binary invocation.
func processExitCode(binary, manifestPath string, args ...string) int {
	all := append([]string{"--json", "--manifest", manifestPath}, args...)
	cmd := exec.Command(binary, all...)
	cmd.Run() //nolint:errcheck
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return -1
}

// ---------------------------------------------------------------------------
// Test 1: Status JSON — valid JSON, exit_code matches process exit code
// ---------------------------------------------------------------------------

func TestJSONStatusBasic(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create a real git repo under base/owner/projects/myrepo.
	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
`, base))

	result := runRPJSON(t, binary, manifest, "status")

	assertString(t, result, "command", "status")
	assertKey(t, result, "exit_code")
	assertKey(t, result, "summary")
	assertKey(t, result, "repos")

	// JSON exit_code must match the actual process exit code.
	jsonExitCode := int(result["exit_code"].(float64))
	procExitCode := processExitCode(binary, manifest, "status")
	if jsonExitCode != procExitCode {
		t.Errorf("exit_code in JSON (%d) does not match process exit code (%d)",
			jsonExitCode, procExitCode)
	}
}

// ---------------------------------------------------------------------------
// Test 2: Status JSON not cloned — missing repo has cloned:false, no
// branch/clean/dirty_files/ahead/behind/has_upstream fields
// ---------------------------------------------------------------------------

func TestJSONStatusNotCloned(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Don't create any directory; the repo is "not cloned".
	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
ghost:
  projects:
    - repo: ghost/missing
`, base))

	result := runRPJSON(t, binary, manifest, "status")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})

	// cloned must be false.
	cloned, ok := repo["cloned"].(bool)
	if !ok || cloned {
		t.Fatalf("expected cloned:false, got %v", repo["cloned"])
	}

	// These fields must be absent when cloned is false.
	for _, field := range []string{"branch", "clean", "dirty_files", "ahead", "behind", "has_upstream"} {
		if _, present := repo[field]; present {
			t.Errorf("field %q should be absent when cloned is false, but it is present", field)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 3: Bootstrap JSON — action field present per repo
// ---------------------------------------------------------------------------

func TestJSONBootstrapActionField(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// One real repo that already exists → "already_exists".
	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	// One repo whose URL is fake → clone will fail → "failed".
	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
    - repo: owner/nonexistent
`, base))

	result := runRPJSON(t, binary, manifest, "bootstrap")

	assertString(t, result, "command", "bootstrap")
	assertKey(t, result, "summary")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 2 {
		t.Fatalf("expected 2 repo entries, got %d", len(repos))
	}

	// Each repo must have an action field.
	for i, r := range repos {
		entry := r.(map[string]interface{})
		action, ok := entry["action"].(string)
		if !ok || action == "" {
			t.Errorf("repos[%d] missing or empty action field", i)
		}
		if entry["local_path"] == nil {
			t.Errorf("repos[%d] missing local_path field", i)
		}
	}

	// Verify the existing repo gets "already_exists".
	firstAction := repos[0].(map[string]interface{})["action"].(string)
	if firstAction != "already_exists" {
		t.Errorf("expected action=already_exists for existing repo, got %q", firstAction)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Sync JSON skipped — dirty repo has action:skipped, reason:dirty,
// dirty_files present
// ---------------------------------------------------------------------------

func TestJSONSyncSkippedDirty(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "dirty")
	makeDirtyGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/dirty
`, base))

	result := runRPJSON(t, binary, manifest, "sync")

	assertString(t, result, "command", "sync")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})
	assertString(t, repo, "action", "skipped")
	assertString(t, repo, "reason", "dirty")

	dirtyFiles, ok := repo["dirty_files"].(float64)
	if !ok {
		t.Fatalf("expected dirty_files field (number), got %T (%v)", repo["dirty_files"], repo["dirty_files"])
	}
	if dirtyFiles <= 0 {
		t.Errorf("expected dirty_files > 0, got %v", dirtyFiles)
	}
}

// ---------------------------------------------------------------------------
// Test 5: Install JSON skipped — missing repo has status:skipped, reason:not_on_disk
// ---------------------------------------------------------------------------

func TestJSONInstallSkippedNotOnDisk(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Repo is not on disk and has an install command defined.
	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/missing
      install:
        - echo hello
`, base))

	result := runRPJSON(t, binary, manifest, "install")

	assertString(t, result, "command", "install")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})
	assertString(t, repo, "status", "skipped")
	assertString(t, repo, "reason", "not_on_disk")
}

// ---------------------------------------------------------------------------
// Test 6: Archive JSON date — last_commit is RFC 3339
// ---------------------------------------------------------------------------


// ---------------------------------------------------------------------------
// Test 7: Error JSON — bad manifest path → error and hint fields present,
// no summary/repos
// ---------------------------------------------------------------------------

func TestJSONErrorBadManifest(t *testing.T) {
	binary := binaryForTest(t)

	result := runRPJSON(t, binary, "/nonexistent/path/manifest.yaml", "status")

	assertKey(t, result, "error")
	assertKey(t, result, "hint")

	assertNoKey(t, result, "summary")
	assertNoKey(t, result, "repos")

	// exit_code must be present and non-zero (typically 2).
	ec := assertKey(t, result, "exit_code").(float64)
	if ec == 0 {
		t.Errorf("expected non-zero exit_code for error result, got 0")
	}
}

// ---------------------------------------------------------------------------
// Test 8: Compact JSON — status --json --compact → no repos field
// ---------------------------------------------------------------------------

func TestJSONCompactNoRepos(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
`, base))

	// Pass --compact in addition to --json (already injected by runRPJSON).
	all := append([]string{"--json", "--compact", "--manifest", manifest}, "status")
	cmd := exec.Command(binary, all...)
	out, _ := cmd.Output()

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}

	assertNoKey(t, result, "repos")
	assertKey(t, result, "summary")
	assertKey(t, result, "command")
	assertKey(t, result, "exit_code")
}

// ---------------------------------------------------------------------------
// Test 9: Dry-run JSON — bootstrap --json --dry-run → dry_run:true,
// action is "would_clone" or "would_skip"
// ---------------------------------------------------------------------------

func TestJSONBootstrapDryRun(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// One existing repo (would_skip) and one missing (would_clone).
	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
    - repo: owner/missing
`, base))

	result := runRPJSON(t, binary, manifest, "bootstrap", "--dry-run")

	assertString(t, result, "command", "bootstrap")

	// dry_run must be true.
	dryRun, ok := result["dry_run"].(bool)
	if !ok || !dryRun {
		t.Fatalf("expected dry_run:true, got %v", result["dry_run"])
	}

	// exit_code must be 0 for dry-run.
	assertFloat(t, result, "exit_code", 0)

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 2 {
		t.Fatalf("expected 2 repo entries, got %d", len(repos))
	}

	validDryRunActions := map[string]bool{
		"would_clone": true,
		"would_skip":  true,
	}

	for i, r := range repos {
		entry := r.(map[string]interface{})
		action, ok := entry["action"].(string)
		if !ok || !validDryRunActions[action] {
			t.Errorf("repos[%d] expected would_clone or would_skip, got %q", i, action)
		}
	}

	// Verify the existing repo is "would_skip" and missing is "would_clone".
	actions := make([]string, len(repos))
	for i, r := range repos {
		actions[i] = r.(map[string]interface{})["action"].(string)
	}

	hasWouldSkip := false
	hasWouldClone := false
	for _, a := range actions {
		if a == "would_skip" {
			hasWouldSkip = true
		}
		if a == "would_clone" {
			hasWouldClone = true
		}
	}
	if !hasWouldSkip {
		t.Errorf("expected at least one would_skip action, actions: %v", actions)
	}
	if !hasWouldClone {
		t.Errorf("expected at least one would_clone action, actions: %v", actions)
	}

	_ = strings.Join(actions, ",") // suppress unused import lint
}

// ---------------------------------------------------------------------------
// Up Command Tests (spec 9.4)
// ---------------------------------------------------------------------------

// upManifest creates a test manifest with:
//   - one repo already cloned (already_exists in bootstrap, up_to_date in sync)
//   - one repo not cloned with a fake URL (fails in bootstrap)
//
// Returns the manifest path and the base directory.
func upManifest(t *testing.T) (manifestPath, base string) {
	t.Helper()
	base = t.TempDir()

	// Create one real repo that already exists on disk.
	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	manifestPath = writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
    - repo: owner/missing
`, base))
	return manifestPath, base
}

// runUpJSON runs `rp up --json` (plus extra args) and returns the parsed JSON.
func runUpJSON(t *testing.T, binary, manifestPath string, extraArgs ...string) map[string]interface{} {
	t.Helper()
	args := append([]string{"--json", "--manifest", manifestPath, "up"}, extraArgs...)
	cmd := exec.Command(binary, args...)
	out, _ := cmd.Output()
	if len(out) == 0 {
		cmd2 := exec.Command(binary, args...)
		combined, _ := cmd2.CombinedOutput()
		t.Fatalf("empty output from binary\ncombined: %s", combined)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON from binary: %v\nraw: %s", err, out)
	}
	return result
}

// assertSubResult checks that the given key in result is a JSON object
// containing both "summary" and "repos" sub-keys.
func assertSubResult(t *testing.T, result map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	raw := assertKey(t, result, key)
	sub, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("key %q: expected object, got %T", key, raw)
	}
	assertKey(t, sub, "summary")
	return sub
}

// ---------------------------------------------------------------------------
// Test Up-1: Up all phases — JSON has bootstrap, sync, install, and update sections
// ---------------------------------------------------------------------------

func TestUpAllPhases(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	// Include a repo with an update command so the update phase has work to do.
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
      update:
        - echo hello
    - repo: owner/missing
`, base))

	result := runUpJSON(t, binary, manifestPath)

	assertString(t, result, "command", "up")
	assertKey(t, result, "exit_code")

	// bootstrap, sync, and update sections must be present.
	assertSubResult(t, result, "bootstrap")
	assertSubResult(t, result, "sync")
	assertSubResult(t, result, "update")
}

// ---------------------------------------------------------------------------
// Test Up-2: Up dry-run — bootstrap + sync present; install/update nil when no repos
// have install/update defined; exit_code 0
// ---------------------------------------------------------------------------

func TestUpDryRun(t *testing.T) {
	binary := binaryForTest(t)
	manifestPath, _ := upManifest(t)

	result := runUpJSON(t, binary, manifestPath, "--dry-run")

	assertString(t, result, "command", "up")

	// dry_run must be true.
	dryRun, ok := result["dry_run"].(bool)
	if !ok || !dryRun {
		t.Fatalf("expected dry_run:true, got %v", result["dry_run"])
	}

	// exit_code must be 0 in dry-run.
	assertFloat(t, result, "exit_code", 0)

	// bootstrap and sync must be present.
	assertSubResult(t, result, "bootstrap")
	assertSubResult(t, result, "sync")
}

// ---------------------------------------------------------------------------
// Test Up-2b: Up dry-run with install/update — preview present with would_run
// commands when repos have install/update defined
// ---------------------------------------------------------------------------

func TestUpDryRunWithInstallUpdate(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create one repo on disk so it shows as would_skip (existing) and eligible for update.
	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
      update:
        - echo hello
    - repo: owner/missing
      install:
        - echo world
`, base))

	result := runUpJSON(t, binary, manifestPath, "--dry-run")

	assertString(t, result, "command", "up")

	dryRun, ok := result["dry_run"].(bool)
	if !ok || !dryRun {
		t.Fatalf("expected dry_run:true, got %v", result["dry_run"])
	}

	assertFloat(t, result, "exit_code", 0)
	assertSubResult(t, result, "bootstrap")
	assertSubResult(t, result, "sync")

	// install preview must be present (owner/missing would be cloned).
	installSub := assertSubResult(t, result, "install")

	// Summary should use dry-run schema: repos/commands/skipped.
	summary, ok := installSub["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("install.summary: expected object, got %T", installSub["summary"])
	}
	if _, ok := summary["repos"]; !ok {
		t.Error("install.summary missing 'repos' key")
	}
	if _, ok := summary["commands"]; !ok {
		t.Error("install.summary missing 'commands' key")
	}

	// update preview must be present (owner/existing is pre-existing).
	updateSub := assertSubResult(t, result, "update")
	updRepos, ok := updateSub["repos"].([]interface{})
	if !ok || len(updRepos) == 0 {
		t.Fatalf("update.repos: expected non-empty array, got %v", updateSub["repos"])
	}
	foundWouldRun := false
	for _, r := range updRepos {
		repo, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		cmds, ok := repo["commands"].([]interface{})
		if !ok {
			continue
		}
		for _, c := range cmds {
			cmd, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd["status"] == "would_run" {
				foundWouldRun = true
			}
		}
	}
	if !foundWouldRun {
		t.Error("expected at least one command with status=would_run in update preview")
	}
}

// ---------------------------------------------------------------------------
// Test Up-3: Up --no-install --no-update — install/update sections omitted
// ---------------------------------------------------------------------------

func TestUpNoInstallNoUpdate(t *testing.T) {
	binary := binaryForTest(t)
	manifestPath, _ := upManifest(t)

	result := runUpJSON(t, binary, manifestPath, "--no-install", "--no-update")

	assertString(t, result, "command", "up")
	assertSubResult(t, result, "bootstrap")
	assertSubResult(t, result, "sync")
}

// ---------------------------------------------------------------------------
// Test Up-4: Up JSON structure — top-level has command:"up",
// bootstrap/sync/update each has summary and repos
// ---------------------------------------------------------------------------

func TestUpJSONStructure(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
      update:
        - echo hello
`, base))

	result := runUpJSON(t, binary, manifestPath)

	assertString(t, result, "command", "up")
	assertKey(t, result, "exit_code")

	for _, phase := range []string{"bootstrap", "sync", "update"} {
		sub := assertSubResult(t, result, phase)
		// repos must be present (not omitted) at the full (non-compact) level.
		assertKey(t, sub, "repos")
	}
}

// ---------------------------------------------------------------------------
// Test Up-5: Up exit code — repo with bad URL fails bootstrap → exit_code 2
// ---------------------------------------------------------------------------

func TestUpExitCodeFailure(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Only a missing repo — bootstrap will fail to clone it.
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/willnotclone
`, base))

	result := runUpJSON(t, binary, manifestPath)

	assertString(t, result, "command", "up")

	// The failed clone must produce exit_code 2 (highest wins).
	exitCode, ok := result["exit_code"].(float64)
	if !ok {
		t.Fatalf("exit_code missing or not a number: %v", result["exit_code"])
	}
	if exitCode != 2 {
		t.Errorf("expected exit_code 2 for bootstrap failure, got %v", exitCode)
	}

	// Process exit code must also be 2.
	args := []string{"--json", "--manifest", manifestPath, "up"}
	cmd := exec.Command(binary, args...)
	cmd.Run() //nolint:errcheck
	if cmd.ProcessState != nil {
		proc := cmd.ProcessState.ExitCode()
		if proc != 2 {
			t.Errorf("expected process exit code 2, got %d", proc)
		}
	}
}

// ---------------------------------------------------------------------------
// Test Up-6: Up phase continuation — dirty repo causes sync skip (exit 1)
// but update still runs
// ---------------------------------------------------------------------------

func TestUpPhaseContinuation(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create a dirty repo that has an update command — sync will skip it, update should still run.
	dirtyDir := filepath.Join(base, "owner", "projects", "dirty")
	makeDirtyGitRepo(t, dirtyDir)

	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/dirty
      update:
        - echo hello
`, base))

	result := runUpJSON(t, binary, manifestPath)

	assertString(t, result, "command", "up")

	// sync must report the dirty repo as skipped.
	syncSub := assertSubResult(t, result, "sync")
	syncRepos, ok := syncSub["repos"].([]interface{})
	if !ok || len(syncRepos) == 0 {
		t.Fatalf("expected sync.repos to be a non-empty array, got %v", syncSub["repos"])
	}
	firstSyncRepo := syncRepos[0].(map[string]interface{})
	if firstSyncRepo["action"] != "skipped" {
		t.Errorf("expected sync action=skipped for dirty repo, got %v", firstSyncRepo["action"])
	}

	// update phase must still be present (phase continuation).
	assertSubResult(t, result, "update")

	// exit_code must be at least 1 (skipped in sync).
	exitCode, ok := result["exit_code"].(float64)
	if !ok || exitCode < 1 {
		t.Errorf("expected exit_code >= 1 due to sync skip, got %v", result["exit_code"])
	}
}

// ---------------------------------------------------------------------------
// Hint Tests (spec 9.5)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Test Hint-1: Missing manifest — error message contains hint text
// ---------------------------------------------------------------------------

func TestHintMissingManifest(t *testing.T) {
	binary := binaryForTest(t)

	// runRPJSON already handles this — but we want to verify hint text is non-empty.
	result := runRPJSON(t, binary, "/nonexistent/path/manifest.yaml", "status")

	hint, ok := result["hint"].(string)
	if !ok || hint == "" {
		t.Fatalf("expected non-empty hint field, got %v", result["hint"])
	}

	// The hint should suggest how to create a manifest.
	if !strings.Contains(strings.ToLower(hint), "manifest") {
		t.Errorf("expected hint to reference 'manifest', got: %q", hint)
	}
}

// ---------------------------------------------------------------------------
// Test Hint-2: JSON error with hint — both "error" and "hint" fields present
// ---------------------------------------------------------------------------

func TestHintJSONErrorHasHintField(t *testing.T) {
	binary := binaryForTest(t)

	result := runRPJSON(t, binary, "/nonexistent/path/manifest.yaml", "status")

	// error field must be present and non-empty.
	errVal, ok := result["error"].(string)
	if !ok || errVal == "" {
		t.Fatalf("expected non-empty error field, got %v", result["error"])
	}

	// hint field must be present and non-empty.
	hintVal, ok := result["hint"].(string)
	if !ok || hintVal == "" {
		t.Fatalf("expected non-empty hint field, got %v", result["hint"])
	}

	// summary and repos must be absent in an error response.
	assertNoKey(t, result, "summary")
	assertNoKey(t, result, "repos")

	// exit_code must be non-zero.
	ec := assertKey(t, result, "exit_code").(float64)
	if ec == 0 {
		t.Errorf("expected non-zero exit_code for error response, got 0")
	}
}

// ---------------------------------------------------------------------------
// Test Hint-3: Human error with hint — stderr contains "error:" and "hint:" lines
// ---------------------------------------------------------------------------

func TestHintHumanModeStderr(t *testing.T) {
	binary := binaryForTest(t)

	// Run WITHOUT --json so we get human-readable output on stderr.
	cmd := exec.Command(binary, "--manifest", "/nonexistent/path/manifest.yaml", "status")
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Run() //nolint:errcheck

	stderr := stderrBuf.String()

	if !strings.Contains(stderr, "error:") {
		t.Errorf("expected stderr to contain 'error:', got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "hint:") {
		t.Errorf("expected stderr to contain 'hint:', got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// 9.2 Filter Tests #6 and #7
// ---------------------------------------------------------------------------

func TestFilterWithJSON(t *testing.T) {
	dir := t.TempDir()

	// Create two repos under different owners.
	repo1 := initGitRepo(t, filepath.Join(dir, "repos", "repo1"))
	_ = initGitRepo(t, filepath.Join(dir, "repos", "repo2"))
	_ = repo1

	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s/repos
alice:
  - repo: alice/repo1
bob:
  - repo: bob/repo2
`, dir))

	binary := filepath.Join(testBinaryDir, "rp")
	result := runRPJSON(t, binary, manifest, "list", "--filter", "alice/")

	repos, ok := result["repos"].([]interface{})
	if !ok {
		t.Fatalf("repos is not an array: %v", result["repos"])
	}

	// Should only contain alice's repos.
	for _, r := range repos {
		repo := r.(map[string]interface{})
		owner, _ := repo["owner"].(string)
		if owner != "alice" {
			t.Errorf("expected only alice repos, got owner=%s", owner)
		}
	}
}

func TestFilterInstallPositionalOverridesFilter(t *testing.T) {
	dir := t.TempDir()

	repoPath := initGitRepo(t, filepath.Join(dir, "repos", "myrepo"))
	_ = repoPath

	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s/repos
me:
  - repo: me/myrepo
    install:
      - echo hello
`, dir))

	binary := filepath.Join(testBinaryDir, "rp")

	// Run install with both --filter and a positional arg.
	args := []string{"--json", "--manifest", manifest, "--filter", "nonexistent/", "install", "me/myrepo"}
	cmd := exec.Command(binary, args...)
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	out, _ := cmd.Output()

	// The positional arg should win, so we get a result (not empty/error from filter).
	if len(out) == 0 {
		t.Fatalf("expected JSON output, got empty")
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}

	// Should have processed the repo (positional wins).
	if result["command"] != "install" {
		t.Errorf("expected command=install, got %v", result["command"])
	}

	// Stderr should contain a warning about --filter being ignored.
	stderr := stderrBuf.String()
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "filter") {
		t.Errorf("expected stderr warning about filter being ignored, got: %s", stderr)
	}
}

// ---------------------------------------------------------------------------
// 7.1 install --dry-run Tests
// ---------------------------------------------------------------------------

// TestInstallDryRunListsCommands: manifest with install, --dry-run --json →
// dry_run:true, status:would_run per command, exit 0.
func TestInstallDryRunListsCommands(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
      install:
        - echo hello
        - echo world
`, base))

	result := runRPJSON(t, binary, manifest, "install", "--dry-run")

	assertString(t, result, "command", "install")
	assertFloat(t, result, "exit_code", 0)

	dryRun, ok := result["dry_run"].(bool)
	if !ok || !dryRun {
		t.Fatalf("expected dry_run:true, got %v", result["dry_run"])
	}

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})
	assertString(t, repo, "status", "ok")

	commands, ok := repo["commands"].([]interface{})
	if !ok || len(commands) == 0 {
		t.Fatalf("expected commands array, got %v", repo["commands"])
	}

	for i, c := range commands {
		cmd := c.(map[string]interface{})
		status, _ := cmd["status"].(string)
		if status != "would_run" {
			t.Errorf("commands[%d]: expected status=would_run, got %q", i, status)
		}
	}
}

// TestInstallDryRunSkipsMissing: repo not on disk with install →
// status:skipped, reason:not_on_disk.
func TestInstallDryRunSkipsMissing(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Repo is NOT created on disk.
	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/missing
      install:
        - echo hello
`, base))

	result := runRPJSON(t, binary, manifest, "install", "--dry-run")

	assertString(t, result, "command", "install")
	assertFloat(t, result, "exit_code", 0)

	dryRun, ok := result["dry_run"].(bool)
	if !ok || !dryRun {
		t.Fatalf("expected dry_run:true, got %v", result["dry_run"])
	}

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})
	assertString(t, repo, "status", "skipped")
	assertString(t, repo, "reason", "not_on_disk")
}

// TestUpDryRunIncludesUpdate: rp up --dry-run --json → update key present (not nil).
func TestUpDryRunIncludesUpdate(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, repoDir)

	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
      update:
        - echo hello
`, base))

	result := runUpJSON(t, binary, manifestPath, "--dry-run")

	assertString(t, result, "command", "up")

	dryRun, ok := result["dry_run"].(bool)
	if !ok || !dryRun {
		t.Fatalf("expected dry_run:true, got %v", result["dry_run"])
	}

	// update key must be present for pre-existing repos with update commands.
	assertSubResult(t, result, "update")
}

// ---------------------------------------------------------------------------
// 7.2 sync error message Tests
// ---------------------------------------------------------------------------

// TestSyncCleanErrorDetachedHead: create repo, detach HEAD, sync --json →
// error contains "detached HEAD".
func TestSyncCleanErrorDetachedHead(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "detached")
	initGitRepo(t, repoDir)

	// Detach HEAD.
	c := exec.Command("git", "checkout", "--detach")
	c.Dir = repoDir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach: %v\n%s", err, out)
	}

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/detached
`, base))

	result := runRPJSON(t, binary, manifest, "sync")

	assertString(t, result, "command", "sync")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})
	errField, _ := repo["error"].(string)
	if !strings.Contains(errField, "detached HEAD") {
		t.Errorf("expected error to contain 'detached HEAD', got %q", errField)
	}

	// Error must be a single line (no newlines).
	if strings.Contains(errField, "\n") {
		t.Errorf("expected single-line error, got multi-line: %q", errField)
	}
}

// TestSyncCleanErrorEmptyRepo: git init (no commits), sync --json →
// error contains "empty repo".
func TestSyncCleanErrorEmptyRepo(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create a git repo with NO commits (just git init, no commit).
	emptyDir := filepath.Join(base, "owner", "projects", "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	c := exec.Command("git", "init")
	c.Dir = emptyDir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Configure git identity so it doesn't fail for other reasons.
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
	} {
		c2 := exec.Command("git", args...)
		c2.Dir = emptyDir
		c2.CombinedOutput() //nolint:errcheck
	}

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/empty
`, base))

	result := runRPJSON(t, binary, manifest, "sync")

	assertString(t, result, "command", "sync")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo entry, got %d", len(repos))
	}

	repo := repos[0].(map[string]interface{})
	errField, _ := repo["error"].(string)
	if !strings.Contains(errField, "empty repo") {
		t.Errorf("expected error to contain 'empty repo', got %q", errField)
	}

	// Error must be a single line (no newlines).
	if strings.Contains(errField, "\n") {
		t.Errorf("expected single-line error, got multi-line: %q", errField)
	}
}

// ---------------------------------------------------------------------------
// 7.3 check command Tests
// ---------------------------------------------------------------------------

// runCheckCmd runs rp check (without --json) and returns stdout bytes, stderr
// bytes, and the process exit code.
func runCheckCmd(binary, manifestPath string, extraArgs ...string) (stdout, stderr []byte, exitCode int) {
	args := append([]string{"--manifest", manifestPath, "check"}, extraArgs...)
	cmd := exec.Command(binary, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Run() //nolint:errcheck
	ec := 0
	if cmd.ProcessState != nil {
		ec = cmd.ProcessState.ExitCode()
	}
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), ec
}

// TestCheckAllClean: all repos clean → exit 0, stdout empty, stderr empty.
func TestCheckAllClean(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "clean")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/clean
`, base))

	stdout, stderr, exitCode := runCheckCmd(binary, manifest)

	if exitCode != 0 {
		t.Errorf("expected exit 0 for all-clean repos, got %d", exitCode)
	}
	if len(stdout) != 0 {
		t.Errorf("expected empty stdout, got: %q", stdout)
	}
	if len(stderr) != 0 {
		t.Errorf("expected empty stderr, got: %q", stderr)
	}
}

// TestCheckDirtyRepo: dirty file → exit 1, stdout empty, stderr empty.
func TestCheckDirtyRepo(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "dirty")
	makeDirtyGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/dirty
`, base))

	stdout, stderr, exitCode := runCheckCmd(binary, manifest)

	if exitCode != 1 {
		t.Errorf("expected exit 1 for dirty repo, got %d", exitCode)
	}
	if len(stdout) != 0 {
		t.Errorf("expected empty stdout, got: %q", stdout)
	}
	if len(stderr) != 0 {
		t.Errorf("expected empty stderr, got: %q", stderr)
	}
}

// TestCheckMissingRepo: repo not cloned → exit 1, stdout empty, stderr empty.
func TestCheckMissingRepo(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Do NOT create the repo directory.
	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/missing
`, base))

	stdout, stderr, exitCode := runCheckCmd(binary, manifest)

	if exitCode != 1 {
		t.Errorf("expected exit 1 for missing repo, got %d", exitCode)
	}
	if len(stdout) != 0 {
		t.Errorf("expected empty stdout, got: %q", stdout)
	}
	if len(stderr) != 0 {
		t.Errorf("expected empty stderr, got: %q", stderr)
	}
}

// TestCheckBadManifest: invalid manifest path → exit 2, stderr has error.
func TestCheckBadManifest(t *testing.T) {
	binary := binaryForTest(t)

	stdout, stderr, exitCode := runCheckCmd(binary, "/nonexistent/path/manifest.yaml")

	if exitCode != 2 {
		t.Errorf("expected exit 2 for bad manifest, got %d", exitCode)
	}
	if len(stdout) != 0 {
		t.Errorf("expected empty stdout, got: %q", stdout)
	}
	if !strings.Contains(string(stderr), "error") {
		t.Errorf("expected stderr to contain 'error', got: %q", stderr)
	}
}

// ---------------------------------------------------------------------------
// 7.4 diff command Tests
// ---------------------------------------------------------------------------

// TestDiffBasic: repos with commits → JSON has sha/message/date per repo.
func TestDiffBasic(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
`, base))

	result := runRPJSON(t, binary, manifest, "diff")

	assertString(t, result, "command", "diff")
	assertFloat(t, result, "exit_code", 0)
	assertKey(t, result, "summary")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) < 1 {
		t.Fatal("expected at least 1 repo in diff output")
	}

	repo := repos[0].(map[string]interface{})

	sha, ok := repo["sha"].(string)
	if !ok || sha == "" {
		t.Errorf("expected non-empty sha field, got %v", repo["sha"])
	}

	message, ok := repo["message"].(string)
	if !ok || message == "" {
		t.Errorf("expected non-empty message field, got %v", repo["message"])
	}

	dateStr, ok := repo["date"].(string)
	if !ok || dateStr == "" {
		t.Errorf("expected non-empty date field, got %v", repo["date"])
	} else {
		if _, err := time.Parse(time.RFC3339, dateStr); err != nil {
			t.Errorf("date %q is not RFC 3339: %v", dateStr, err)
		}
	}

	// days_ago must be present as a number.
	if _, ok := repo["days_ago"].(float64); !ok {
		t.Errorf("expected days_ago field (number), got %T (%v)", repo["days_ago"], repo["days_ago"])
	}
}

// TestDiffSinceFilter: --since 9999d → all repos shown (cutoff far in past).
func TestDiffSinceFilter(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
`, base))

	result := runRPJSON(t, binary, manifest, "diff", "--since", "9999d")

	assertString(t, result, "command", "diff")
	assertFloat(t, result, "exit_code", 0)

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) < 1 {
		t.Error("expected at least 1 repo with --since 9999d")
	}

	summary := assertKey(t, result, "summary").(map[string]interface{})
	shown, _ := summary["shown"].(float64)
	if shown < 1 {
		t.Errorf("expected summary.shown >= 1 with --since 9999d, got %v", shown)
	}
}

// TestDiffInvalidSince: --since 1w → exit 2.
func TestDiffInvalidSince(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
`, base))

	// Use runRPJSON to capture the JSON error envelope.
	result := runRPJSON(t, binary, manifest, "diff", "--since", "1w")

	// Must have an error and exit_code 2.
	assertKey(t, result, "error")

	ec, ok := result["exit_code"].(float64)
	if !ok || ec != 2 {
		t.Errorf("expected exit_code 2 for invalid --since format, got %v", result["exit_code"])
	}
}

// ---------------------------------------------------------------------------
// 7.5 Hint Tests
// ---------------------------------------------------------------------------

// TestHintNoOwners: manifest with no owners defined, --json →
// hint field non-empty.
func TestHintNoOwners(t *testing.T) {
	binary := binaryForTest(t)

	// Write a manifest with only base_dir and no owners.
	dir := t.TempDir()
	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s
`, dir))

	result := runRPJSON(t, binary, manifest, "status")

	assertKey(t, result, "error")

	hint, ok := result["hint"].(string)
	if !ok || hint == "" {
		t.Fatalf("expected non-empty hint field for no-owners manifest, got %v", result["hint"])
	}
}

// TestHintEmptyCategory: projects: [] in manifest, --json →
// hint field non-empty.
func TestHintEmptyCategory(t *testing.T) {
	binary := binaryForTest(t)

	dir := t.TempDir()
	// projects: [] is an empty category list — should trigger a validation hint.
	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s
owner:
  projects: []
`, dir))

	result := runRPJSON(t, binary, manifest, "status")

	assertKey(t, result, "error")

	hint, ok := result["hint"].(string)
	if !ok || hint == "" {
		t.Fatalf("expected non-empty hint field for empty-category manifest, got %v", result["hint"])
	}
}

// ---------------------------------------------------------------------------
// Test 1 (new): install --dry-run positional with no install → "no install commands configured",
// exit 0
// ---------------------------------------------------------------------------

// TestInstallDryRunPositionalNoInstall: manifest with a repo that has NO install.
// Running `install --dry-run --json reponame` should exit 0 and return an empty
// repos array (the "no install commands configured" fast path).
func TestInstallDryRunPositionalNoInstall(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "noinstall")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/noinstall
`, base))

	result := runRPJSON(t, binary, manifest, "install", "--dry-run", "owner/noinstall")

	assertString(t, result, "command", "install")
	assertFloat(t, result, "exit_code", 0)

	// repos array must be empty (no install commands to report).
	repos, ok := result["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected repos array, got %T (%v)", result["repos"], result["repos"])
	}
	if len(repos) != 0 {
		t.Errorf("expected empty repos array for no-install repo, got %d entries", len(repos))
	}
}

// ---------------------------------------------------------------------------
// Test 2 (new): sync --json --filter on a repo whose remote has been deleted
// → error contains "pull failed", no newlines, no raw "fatal:"
// ---------------------------------------------------------------------------

// TestSyncCleanErrorGeneric: init a bare repo, clone it, delete the bare repo,
// then sync. The clone's remote points to a deleted path → pull fails with a
// generic error that must say "pull failed" but must NOT contain newlines or
// raw git stderr like "fatal:".
func TestSyncCleanErrorGeneric(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create a bare "upstream" repo.
	bareDir := filepath.Join(base, "bare.git")
	if err := os.MkdirAll(bareDir, 0755); err != nil {
		t.Fatalf("MkdirAll bare: %v", err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	runGit(bareDir, "init", "--bare")

	// Clone the bare repo so we have a local copy with a valid remote.
	cloneDir := filepath.Join(base, "owner", "projects", "thatrepo")
	if err := os.MkdirAll(filepath.Dir(cloneDir), 0755); err != nil {
		t.Fatalf("MkdirAll clone parent: %v", err)
	}
	c := exec.Command("git", "clone", bareDir, cloneDir)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	runGit(cloneDir, "config", "user.email", "test@example.com")
	runGit(cloneDir, "config", "user.name", "Test User")

	// Make a commit in the clone so it has history.
	readmePath := filepath.Join(cloneDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(cloneDir, "add", "README.md")
	runGit(cloneDir, "commit", "-m", "init")

	// Delete the bare (upstream) repo — the remote now points to a missing path.
	if err := os.RemoveAll(bareDir); err != nil {
		t.Fatalf("RemoveAll bare: %v", err)
	}

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/thatrepo
`, base))

	result := runRPJSON(t, binary, manifest, "sync", "--filter", "owner/")

	assertString(t, result, "command", "sync")

	repos, ok := result["repos"].([]interface{})
	if !ok || len(repos) == 0 {
		t.Fatalf("expected at least 1 repo entry in sync result, got %v", result["repos"])
	}

	repo := repos[0].(map[string]interface{})

	errField, _ := repo["error"].(string)
	if !strings.Contains(errField, "pull failed") {
		t.Errorf("expected error to contain 'pull failed', got %q", errField)
	}

	// Must be single-line — no raw git stderr.
	if strings.Contains(errField, "\n") {
		t.Errorf("expected single-line error, got multi-line: %q", errField)
	}
	if strings.Contains(errField, "fatal:") {
		t.Errorf("expected clean error without raw git stderr, got %q", errField)
	}
}

// ---------------------------------------------------------------------------
// Test 3 (new): check --filter on a clean subset → exit 0
// ---------------------------------------------------------------------------

// TestCheckWithFilter: create 2 repos, make one dirty. Run `check
// --filter clean-owner/`. Should exit 0 because the dirty repo is excluded.
func TestCheckWithFilter(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	cleanDir := filepath.Join(base, "clean-owner", "projects", "clean")
	initGitRepo(t, cleanDir)

	dirtyDir := filepath.Join(base, "dirty-owner", "projects", "dirty")
	makeDirtyGitRepo(t, dirtyDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
clean-owner:
  projects:
    - repo: clean-owner/clean
dirty-owner:
  projects:
    - repo: dirty-owner/dirty
`, base))

	_, _, exitCode := runCheckCmd(binary, manifest, "--filter", "clean-owner/")

	if exitCode != 0 {
		t.Errorf("expected exit 0 when filtering to clean repos, got %d", exitCode)
	}
}

// ---------------------------------------------------------------------------
// Test 4 (new): check with repo that has no remote → exit 0 (clean is fine)
// ---------------------------------------------------------------------------

// TestCheckNoUpstreamClean: create a repo with no remote (just git init +
// commit). Run check. Should exit 0 — no upstream is OK when the repo is clean.
func TestCheckNoUpstreamClean(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "local")
	initGitRepo(t, repoDir)
	// initGitRepo creates a clean repo with no remote.

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/local
`, base))

	_, _, exitCode := runCheckCmd(binary, manifest)

	if exitCode != 0 {
		t.Errorf("expected exit 0 for clean repo with no upstream, got %d", exitCode)
	}
}

// ---------------------------------------------------------------------------
// Test 5 (new): diff --since 1d excludes a repo backdated to 2020
// ---------------------------------------------------------------------------

// TestDiffSinceExcludes: create a repo whose commit is backdated to 2020.
// Running `diff --json --since 1d` should NOT include that repo in the output.
func TestDiffSinceExcludes(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "oldrepo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	runCmd := func(env []string, dir string, args ...string) {
		t.Helper()
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	baseEnv := os.Environ()

	runCmd(baseEnv, repoDir, "git", "init")
	runCmd(baseEnv, repoDir, "git", "config", "user.email", "test@example.com")
	runCmd(baseEnv, repoDir, "git", "config", "user.name", "Test User")

	readmePath := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("old\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runCmd(baseEnv, repoDir, "git", "add", "README.md")

	// Backdate both author date and committer date to 2020.
	backdatedEnv := append(append([]string{}, baseEnv...), "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z")
	runCmd(backdatedEnv, repoDir,
		"git", "commit", "-m", "old", "--date=2020-01-01T00:00:00Z")

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/oldrepo
`, base))

	result := runRPJSON(t, binary, manifest, "diff", "--since", "1d")

	assertString(t, result, "command", "diff")
	assertFloat(t, result, "exit_code", 0)

	repos, ok := result["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected repos array, got %T", result["repos"])
	}

	// The backdated repo must NOT appear.
	for _, r := range repos {
		entry := r.(map[string]interface{})
		if entry["repo"] == "owner/oldrepo" {
			t.Errorf("expected backdated repo to be excluded by --since 1d, but it appeared in output")
		}
	}
}

// ---------------------------------------------------------------------------
// Test 6 (new): diff --json on an empty repo (no commits) → no crash, repo absent
// ---------------------------------------------------------------------------

// TestDiffEmptyRepoSkipped: create an empty repo (git init, no commits).
// Run `diff --json`. Should not crash and the empty repo must not appear in
// the repos array.
func TestDiffEmptyRepoSkipped(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	emptyDir := filepath.Join(base, "owner", "projects", "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	c := exec.Command("git", "init")
	c.Dir = emptyDir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
	} {
		c2 := exec.Command("git", args...)
		c2.Dir = emptyDir
		c2.CombinedOutput() //nolint:errcheck
	}

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/empty
`, base))

	result := runRPJSON(t, binary, manifest, "diff")

	assertString(t, result, "command", "diff")
	assertFloat(t, result, "exit_code", 0)

	repos, ok := result["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected repos array, got %T", result["repos"])
	}

	// Empty repo has no commits, so diff skips it silently.
	for _, r := range repos {
		entry := r.(map[string]interface{})
		if entry["repo"] == "owner/empty" {
			t.Errorf("expected empty repo to be skipped in diff output, but it appeared")
		}
	}
}

// ---------------------------------------------------------------------------
// Test 7 (new): list --json with flat: 42 (non-bool) → hint field present
// ---------------------------------------------------------------------------

// TestHintFlatNonBool: manifest with flat: 42 (a non-boolean value).
// Running `list --json` should fail manifest validation and return a non-empty
// hint field explaining the problem.

// ---------------------------------------------------------------------------
// Test 8 (new): list --json with install: [""] (empty string) → hint present
// ---------------------------------------------------------------------------

// TestHintEmptyInstall: manifest with install: [""] (an empty string in the install
// list). Running `list --json` should fail manifest validation and return a
// non-empty hint field explaining the problem.
func TestHintEmptyInstall(t *testing.T) {
	binary := binaryForTest(t)

	dir := t.TempDir()
	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
      install:
        - ""
`, dir))

	result := runRPJSON(t, binary, manifest, "list")

	assertKey(t, result, "error")

	hint, ok := result["hint"].(string)
	if !ok || hint == "" {
		t.Fatalf("expected non-empty hint field for empty-install manifest, got %v", result["hint"])
	}
}

// --- QA Regression Tests ---

// QA5-R1: diff must handle pipe character in commit message (was using | as delimiter)
func TestQA_DiffPipeInCommitMessage(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()
	repoDir := initGitRepo(t, filepath.Join(base, "owner", "repos", "pipe"))

	// Add commit with pipe in message
	cmd := exec.Command("git", "-C", repoDir, "commit", "--allow-empty", "-m", "feat: support x | y | z parsing", "--no-gpg-sign")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit failed: %s\n%s", err, out)
	}

	manifest := writeManifest(t, base, fmt.Sprintf(`
base_dir: %s
owner:
  repos:
    - repo: owner/pipe
`, base))

	result := runRPJSON(t, binary, manifest, "diff")

	repos, ok := result["repos"].([]interface{})
	if !ok || len(repos) == 0 {
		t.Fatalf("expected repos in diff output, got %v", result["repos"])
	}
	repo := repos[0].(map[string]interface{})
	msg, _ := repo["message"].(string)
	if !strings.Contains(msg, "|") {
		t.Errorf("expected pipe character in message, got %q", msg)
	}
	if msg != "feat: support x | y | z parsing" {
		t.Errorf("message mismatch: got %q", msg)
	}
}

// QA4-R1: up --dry-run must NOT create any directories or clone any repos
func TestQA_UpDryRunDoesNotClone(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()
	wsDir := filepath.Join(base, "workspace")
	// Do NOT create wsDir — it should not exist after dry-run

	manifest := writeManifest(t, base, fmt.Sprintf(`
base_dir: %s
test:
  - repo: test/repo
`, wsDir))

	result := runUpJSON(t, binary, manifest, "--dry-run")

	// Verify dry_run flag
	if dr, ok := result["dry_run"].(bool); !ok || !dr {
		t.Errorf("expected dry_run=true")
	}

	// Verify workspace directory was NOT created
	if _, err := os.Stat(wsDir); err == nil {
		t.Errorf("workspace directory %s should NOT exist after dry-run, but it does", wsDir)
	}
}

// QA2-R1: bootstrap summary should not produce "cloneds" or "faileds"
func TestQA_BootstrapSummaryPlural(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()
	initGitRepo(t, filepath.Join(base, "owner", "repos", "existing"))

	manifest := writeManifest(t, base, fmt.Sprintf(`
base_dir: %s
owner:
  repos:
    - repo: owner/existing
`, base))

	// Run bootstrap (human mode) and check summary
	args := []string{"--manifest", manifest, "bootstrap"}
	cmd := exec.Command(binary, args...)
	out, _ := cmd.CombinedOutput()
	output := string(out)

	if strings.Contains(output, "cloneds") {
		t.Errorf("summary should not contain 'cloneds': %s", output)
	}
	if strings.Contains(output, "faileds") {
		t.Errorf("summary should not contain 'faileds': %s", output)
	}
	if !strings.Contains(output, "already existed") {
		t.Errorf("summary should contain 'already existed': %s", output)
	}
}

// ---------------------------------------------------------------------------
// Discover Tests
// ---------------------------------------------------------------------------

// skipWithoutGh skips the test if gh CLI is not available or -short is set.
func skipWithoutGh(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping gh-dependent test in short mode")
	}
	cmd := exec.Command("gh", "auth", "status")
	if cmd.Run() != nil {
		t.Skip("gh CLI not available")
	}
}

// TestDiscoverGhNotFound verifies exit 2 + hint when gh is not in PATH.
func TestDiscoverGhNotFound(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()
	repoDir := filepath.Join(base, "owner", "repo")
	initGitRepo(t, repoDir)
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  - repo: owner/repo
`, base))

	// Run with PATH set to a nonexistent dir so gh cannot be found.
	cmd := exec.Command(binary, "--json", "--manifest", manifestPath, "discover")
	cmd.Env = append(os.Environ(), "PATH=/nonexistent")
	out, _ := cmd.Output()

	if cmd.ProcessState.ExitCode() != 2 {
		t.Fatalf("expected exit 2, got %d", cmd.ProcessState.ExitCode())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}

	assertString(t, result, "command", "discover")
	assertFloat(t, result, "exit_code", 2)

	errMsg, ok := result["error"].(string)
	if !ok || !strings.Contains(errMsg, "gh CLI not found") {
		t.Fatalf("expected error containing 'gh CLI not found', got %q", errMsg)
	}

	hint, ok := result["hint"].(string)
	if !ok || hint == "" {
		t.Fatalf("expected non-empty hint, got %q", hint)
	}
}

// TestDiscoverJSONSchema verifies JSON structure with real gh.
func TestDiscoverJSONSchema(t *testing.T) {
	skipWithoutGh(t)
	binary := binaryForTest(t)
	base := t.TempDir()
	repoDir := filepath.Join(base, "nobody", "nonexistent")
	initGitRepo(t, repoDir)
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
nobody:
  - repo: nobody/nonexistent
`, base))

	cmd := exec.Command(binary, "--json", "--manifest", manifestPath, "discover")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Run()

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	assertString(t, result, "command", "discover")
	assertKey(t, result, "exit_code")

	// Verify exit_code is 0 or 1.
	exitCode := result["exit_code"].(float64)
	if exitCode != 0 && exitCode != 1 {
		t.Fatalf("expected exit_code 0 or 1, got %v", exitCode)
	}

	// Verify summary has all 4 keys with numeric values.
	summary, ok := result["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary to be object, got %T", result["summary"])
	}
	for _, key := range []string{"untracked", "owners_scanned", "total_remote", "total_manifest"} {
		v, exists := summary[key]
		if !exists {
			t.Fatalf("summary missing key %q", key)
		}
		if _, ok := v.(float64); !ok {
			t.Fatalf("summary[%q] expected number, got %T", key, v)
		}
	}

	// Verify repos is an array.
	repos := assertKey(t, result, "repos")
	if _, ok := repos.([]interface{}); !ok {
		t.Fatalf("expected repos to be array, got %T", repos)
	}
}

// TestDiscoverCompact verifies --compact omits repos key.
func TestDiscoverCompact(t *testing.T) {
	skipWithoutGh(t)
	binary := binaryForTest(t)
	base := t.TempDir()
	repoDir := filepath.Join(base, "nobody", "nonexistent")
	initGitRepo(t, repoDir)
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
nobody:
  - repo: nobody/nonexistent
`, base))

	cmd := exec.Command(binary, "--json", "--compact", "--manifest", manifestPath, "discover")
	out, _ := cmd.Output()

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}

	assertString(t, result, "command", "discover")
	assertNoKey(t, result, "repos")
	assertKey(t, result, "summary")
}

// TestDiscoverFilterNonexistent verifies --filter with no matches returns exit 0.
func TestDiscoverFilterNonexistent(t *testing.T) {
	skipWithoutGh(t)
	binary := binaryForTest(t)
	base := t.TempDir()
	repoDir := filepath.Join(base, "nobody", "nonexistent")
	initGitRepo(t, repoDir)
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
nobody:
  - repo: nobody/nonexistent
`, base))

	cmd := exec.Command(binary, "--json", "--manifest", manifestPath, "--filter", "nonexistent/", "discover")
	out, _ := cmd.Output()

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}

	assertString(t, result, "command", "discover")
	assertFloat(t, result, "exit_code", 0)

	summary := result["summary"].(map[string]interface{})
	if summary["untracked"].(float64) != 0 {
		t.Fatalf("expected 0 untracked with nonexistent filter, got %v", summary["untracked"])
	}
}

// TestDiscoverExitCode1 verifies exit 1 when untracked repos exist.
func TestDiscoverExitCode1(t *testing.T) {
	skipWithoutGh(t)
	binary := binaryForTest(t)
	base := t.TempDir()
	repoDir := filepath.Join(base, "nobody", "nonexistent")
	initGitRepo(t, repoDir)

	// Manifest with a single dummy repo — all real GitHub repos will be untracked.
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
nobody:
  - repo: nobody/nonexistent
`, base))

	cmd := exec.Command(binary, "--json", "--manifest", manifestPath, "discover")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Run()

	exitCode := cmd.ProcessState.ExitCode()

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	summary := result["summary"].(map[string]interface{})
	untracked := summary["untracked"].(float64)

	if untracked == 0 {
		t.Skip("user has zero GitHub repos — cannot test exit code 1")
	}

	if exitCode != 1 {
		t.Fatalf("expected exit 1 (untracked repos found), got %d", exitCode)
	}
	assertFloat(t, result, "exit_code", 1)
}

// QA v0.5.0 — Q4: old-format manifest with owners: key produces JSON error with migration hint
func TestQA_IntegrationOldFormatRejectsJSON(t *testing.T) {
	binary := binaryForTest(t)
	dir := t.TempDir()

	manifest := writeManifest(t, dir, `
base_dir: `+dir+`
owners:
  owner:
    projects:
      - repo: owner/myrepo
`)

	result := runRPJSON(t, binary, manifest, "status")

	assertFloat(t, result, "exit_code", 2)

	errVal, ok := result["error"].(string)
	if !ok || errVal == "" {
		t.Fatalf("expected non-empty error field, got %v", result["error"])
	}
	if !strings.Contains(errVal, "is no longer a valid manifest key") {
		t.Errorf("error should contain migration message, got: %q", errVal)
	}

	hintVal, ok := result["hint"].(string)
	if !ok || hintVal == "" {
		t.Fatalf("expected non-empty hint field, got %v", result["hint"])
	}
	if !strings.Contains(hintVal, "dedent owner blocks by one level") {
		t.Errorf("hint should contain dedent instruction, got: %q", hintVal)
	}
}

// ---------------------------------------------------------------------------
// QA Regression Tests (v0.6.0 spec §4.4)
// ---------------------------------------------------------------------------

// Q1: Manifest with "deps:" key must be rejected with migration hint.
func TestQA_DepsKeyRejectedJSON(t *testing.T) {
	binary := binaryForTest(t)
	dir := t.TempDir()

	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
      deps:
        - echo hello
`, dir))

	result := runRPJSON(t, binary, manifest, "status")

	assertFloat(t, result, "exit_code", 2)

	errVal, ok := result["error"].(string)
	if !ok || errVal == "" {
		t.Fatalf("expected non-empty error field, got %v", result["error"])
	}
	if !strings.Contains(errVal, `removed key "deps"`) {
		t.Errorf("error should contain removed key message, got: %q", errVal)
	}

	hintVal, ok := result["hint"].(string)
	if !ok || hintVal == "" {
		t.Fatalf("expected non-empty hint field, got %v", result["hint"])
	}
	if !strings.Contains(hintVal, `Rename "deps:" to "install:"`) {
		t.Errorf("hint should contain rename instruction, got: %q", hintVal)
	}
}

// Q2: "rp install --json" runs install commands on an existing repo.
func TestQA_InstallStandalone(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
      install:
        - echo hello
`, base))

	result := runRPJSON(t, binary, manifest, "install")

	assertString(t, result, "command", "install")
	assertFloat(t, result, "exit_code", 0)

	summary, ok := result["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object, got %T", result["summary"])
	}
	succeeded, ok := summary["succeeded"].(float64)
	if !ok || succeeded != 1 {
		t.Errorf("expected summary.succeeded=1, got %v", summary["succeeded"])
	}
}

// Q3: "rp update --json" on a repo without update: commands.
func TestQA_UpdateSkipsNoUpdate(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "myrepo")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/myrepo
      install:
        - echo hello
`, base))

	result := runRPJSON(t, binary, manifest, "update")

	assertString(t, result, "command", "update")
	assertFloat(t, result, "exit_code", 0)

	repos, ok := result["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected repos array, got %T", result["repos"])
	}
	if len(repos) != 0 {
		t.Errorf("expected empty repos array (no update commands), got %d entries", len(repos))
	}
}

// Q4: "rp up --json" clones a repo and runs install (not update) on it.
func TestQA_UpInstallNewClone(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create a bare repo to clone from.
	bareDir := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(bareDir, "git", "init", "--bare", bareDir)
	// Seed the bare repo with an initial commit.
	seedClone := filepath.Join(t.TempDir(), "seed")
	run(t.TempDir(), "git", "clone", bareDir, seedClone)
	run(seedClone, "git", "config", "user.email", "test@test.com")
	run(seedClone, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(seedClone, "seed.txt"), []byte("seed"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run(seedClone, "git", "add", ".")
	run(seedClone, "git", "commit", "-m", "seed commit")
	run(seedClone, "git", "push")

	// Write a temporary gitconfig that redirects the SSH clone URL to our local bare repo.
	gitConfigDir := t.TempDir()
	gitConfigPath := filepath.Join(gitConfigDir, ".gitconfig")
	repoName := "owner/cloneme"
	gitConfigContent := fmt.Sprintf("[url %q]\n\tinsteadOf = git@github.com:%s.git\n", bareDir, repoName)
	if err := os.WriteFile(gitConfigPath, []byte(gitConfigContent), 0644); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}

	cloneDir := filepath.Join(base, "owner", "projects", "cloneme")

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/cloneme
      install:
        - echo installed
      update:
        - echo updated
`, base))

	// Run rp up --json with the custom gitconfig.
	args := []string{"--json", "--manifest", manifest, "up"}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitConfigPath)
	out, _ := cmd.Output()
	if len(out) == 0 {
		cmd2 := exec.Command(binary, args...)
		cmd2.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitConfigPath)
		combined, _ := cmd2.CombinedOutput()
		t.Fatalf("empty output from binary\ncombined: %s", combined)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON from binary: %v\nraw: %s", err, out)
	}

	assertString(t, result, "command", "up")

	// Verify the clone succeeded (repo dir should exist).
	if _, err := os.Stat(cloneDir); os.IsNotExist(err) {
		t.Fatalf("clone directory %q does not exist — bootstrap failed", cloneDir)
	}

	// install.repos must contain the repo (newly cloned).
	installSub, ok := result["install"].(map[string]interface{})
	if !ok || installSub == nil {
		t.Fatalf("expected install sub-result, got %v", result["install"])
	}
	installRepos, ok := installSub["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected install.repos array, got %T", installSub["repos"])
	}
	found := false
	for _, r := range installRepos {
		repo, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if repo["repo"] == repoName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected install.repos to contain %q (newly cloned), got %v", repoName, installRepos)
	}

	// update.repos must NOT contain the repo (it was newly cloned).
	updateSub, ok := result["update"].(map[string]interface{})
	if !ok || updateSub == nil {
		// update being null is also acceptable — repo was cloned, not pre-existing.
		return
	}
	updateRepos, ok := updateSub["repos"].([]interface{})
	if !ok {
		return
	}
	for _, r := range updateRepos {
		repo, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if repo["repo"] == repoName {
			t.Errorf("update.repos should NOT contain %q (newly cloned), but it does", repoName)
		}
	}
}

// Q5: "rp up --json" on existing repo runs update (not install).
func TestQA_UpUpdateExisting(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owner:
  projects:
    - repo: owner/existing
      install:
        - echo installed
      update:
        - echo updated
`, base))

	result := runUpJSON(t, binary, manifest)

	assertString(t, result, "command", "up")

	// update.repos must contain the repo (pre-existing).
	updateSub, ok := result["update"].(map[string]interface{})
	if !ok || updateSub == nil {
		t.Fatalf("expected update sub-result, got %v", result["update"])
	}
	updateRepos, ok := updateSub["repos"].([]interface{})
	if !ok {
		t.Fatalf("expected update.repos array, got %T", updateSub["repos"])
	}
	foundUpdate := false
	for _, r := range updateRepos {
		repo, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if repo["repo"] == "owner/existing" {
			foundUpdate = true
			break
		}
	}
	if !foundUpdate {
		t.Errorf("expected update.repos to contain %q (pre-existing), got %v", "owner/existing", updateRepos)
	}

	// install.repos must NOT contain the repo (it was pre-existing, not newly cloned).
	installRaw := result["install"]
	if installRaw == nil {
		// install being null is acceptable — no newly cloned repos.
		return
	}
	installSub, ok := installRaw.(map[string]interface{})
	if !ok {
		return
	}
	installRepos, ok := installSub["repos"].([]interface{})
	if !ok {
		return
	}
	for _, r := range installRepos {
		repo, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if repo["repo"] == "owner/existing" {
			t.Errorf("install.repos should NOT contain %q (pre-existing), but it does", "owner/existing")
		}
	}
}

// Q6: "rp up --json --no-install --no-update" produces null install/update.
func TestQA_UpNoInstallNoUpdate(t *testing.T) {
	binary := binaryForTest(t)
	manifestPath, _ := upManifest(t)

	result := runUpJSON(t, binary, manifestPath, "--no-install", "--no-update")

	assertString(t, result, "command", "up")

	// install key must be present but null.
	installVal, installPresent := result["install"]
	if !installPresent {
		t.Fatal("expected 'install' key to be present in JSON output")
	}
	if installVal != nil {
		t.Errorf("expected install to be null, got %v", installVal)
	}

	// update key must be present but null.
	updateVal, updatePresent := result["update"]
	if !updatePresent {
		t.Fatal("expected 'update' key to be present in JSON output")
	}
	if updateVal != nil {
		t.Errorf("expected update to be null, got %v", updateVal)
	}
}

// Q7: "rp deps" migration stub prints error and exits 2.
func TestQA_DepsCmdMigration(t *testing.T) {
	binary := binaryForTest(t)

	cmd := exec.Command(binary, "deps")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error running rp deps: %v", err)
		}
	}

	if exitCode != 2 {
		t.Errorf("expected exit code 2, got %d", exitCode)
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, `replaced by "rp install" and "rp update"`) {
		t.Errorf("stderr should contain migration message, got: %q", stderrStr)
	}
}
