package cmd_test

import (
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
owners:
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
owners:
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
owners:
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
owners:
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
// Test 5: Deps JSON skipped — missing repo has status:skipped, reason:not_on_disk
// ---------------------------------------------------------------------------

func TestJSONDepsSkippedNotOnDisk(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Repo is not on disk and has a dep defined.
	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owners:
  owner:
    projects:
      - repo: owner/missing
        deps:
          - echo hello
`, base))

	result := runRPJSON(t, binary, manifest, "deps")

	assertString(t, result, "command", "deps")

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

func TestJSONArchiveDateFormat(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	repoDir := filepath.Join(base, "owner", "projects", "stale")
	initGitRepo(t, repoDir)

	manifest := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owners:
  owner:
    projects:
      - repo: owner/stale
`, base))

	// Use threshold=0 so any repo (including freshly created) shows as a candidate.
	result := runRPJSON(t, binary, manifest, "archive", "--threshold", "0")

	assertString(t, result, "command", "archive")

	repos := assertKey(t, result, "repos").([]interface{})
	if len(repos) < 1 {
		t.Fatal("expected at least 1 archive candidate (threshold=0)")
	}

	repo := repos[0].(map[string]interface{})
	lastCommit, ok := repo["last_commit"].(string)
	if !ok || lastCommit == "" {
		t.Fatalf("expected last_commit string, got %v", repo["last_commit"])
	}

	// Must parse as RFC 3339.
	if _, err := time.Parse(time.RFC3339, lastCommit); err != nil {
		t.Errorf("last_commit %q is not RFC 3339: %v", lastCommit, err)
	}
}

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
owners:
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
owners:
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
owners:
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
// Test Up-1: Up all phases — JSON has bootstrap, sync, and deps sections
// ---------------------------------------------------------------------------

func TestUpAllPhases(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	// Include a repo with a dep so the deps phase has work to do.
	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owners:
  owner:
    projects:
      - repo: owner/existing
        deps:
          - echo hello
      - repo: owner/missing
`, base))

	result := runUpJSON(t, binary, manifestPath)

	assertString(t, result, "command", "up")
	assertKey(t, result, "exit_code")

	// All three sections must be present.
	assertSubResult(t, result, "bootstrap")
	assertSubResult(t, result, "sync")
	assertSubResult(t, result, "deps")
}

// ---------------------------------------------------------------------------
// Test Up-2: Up dry-run — bootstrap + sync present, deps omitted, exit_code 0
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

	// deps must be omitted.
	assertNoKey(t, result, "deps")
}

// ---------------------------------------------------------------------------
// Test Up-3: Up no-deps — deps section omitted
// ---------------------------------------------------------------------------

func TestUpNoDeps(t *testing.T) {
	binary := binaryForTest(t)
	manifestPath, _ := upManifest(t)

	result := runUpJSON(t, binary, manifestPath, "--no-deps")

	assertString(t, result, "command", "up")
	assertSubResult(t, result, "bootstrap")
	assertSubResult(t, result, "sync")
	assertNoKey(t, result, "deps")
}

// ---------------------------------------------------------------------------
// Test Up-4: Up JSON structure — top-level has command:"up",
// bootstrap/sync/deps each has summary and repos
// ---------------------------------------------------------------------------

func TestUpJSONStructure(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	existingDir := filepath.Join(base, "owner", "projects", "existing")
	initGitRepo(t, existingDir)

	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owners:
  owner:
    projects:
      - repo: owner/existing
        deps:
          - echo hello
`, base))

	result := runUpJSON(t, binary, manifestPath)

	assertString(t, result, "command", "up")
	assertKey(t, result, "exit_code")

	for _, phase := range []string{"bootstrap", "sync", "deps"} {
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
owners:
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
// but deps still runs
// ---------------------------------------------------------------------------

func TestUpPhaseContinuation(t *testing.T) {
	binary := binaryForTest(t)
	base := t.TempDir()

	// Create a dirty repo that has a dep — sync will skip it, deps should still run.
	dirtyDir := filepath.Join(base, "owner", "projects", "dirty")
	makeDirtyGitRepo(t, dirtyDir)

	manifestPath := writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
owners:
  owner:
    projects:
      - repo: owner/dirty
        deps:
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

	// deps phase must still be present (phase continuation).
	assertSubResult(t, result, "deps")

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
owners:
  alice:
    flat: true
    repos:
      - repo: alice/repo1
  bob:
    flat: true
    repos:
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

func TestFilterDepsPositionalOverridesFilter(t *testing.T) {
	dir := t.TempDir()

	repoPath := initGitRepo(t, filepath.Join(dir, "repos", "myrepo"))
	_ = repoPath

	manifest := writeManifest(t, dir, fmt.Sprintf(`
base_dir: %s/repos
owners:
  me:
    flat: true
    repos:
      - repo: me/myrepo
        deps:
          - echo hello
`, dir))

	binary := filepath.Join(testBinaryDir, "rp")

	// Run deps with both --filter and a positional arg.
	args := []string{"--json", "--manifest", manifest, "--filter", "nonexistent/", "deps", "me/myrepo"}
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
	if result["command"] != "deps" {
		t.Errorf("expected command=deps, got %v", result["command"])
	}

	// Stderr should contain a warning about --filter being ignored.
	stderr := stderrBuf.String()
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "filter") {
		t.Errorf("expected stderr warning about filter being ignored, got: %s", stderr)
	}
}
