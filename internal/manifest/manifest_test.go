package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deligoez/rp/internal/output"
)

// writeManifest writes content to a temp file and returns its path.
func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	os.WriteFile(path, []byte(content), 0644)
	return path
}

// Test 1: Parse valid manifest — full YAML with multiple owners, categories, flat and archive.
func TestParseValidManifest(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
  projects:
    - repo: deligoez/tp
    - repo: deligoez/rp
phonyland:
  - repo: phonyland/cloud
  - repo: phonyland/framework
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if m.BaseDir != baseDir {
		t.Errorf("BaseDir = %q, want %q", m.BaseDir, baseDir)
	}

	owners := m.Owners()
	if len(owners) != 2 {
		t.Fatalf("expected 2 owners, got %d", len(owners))
	}

	// deligoez owner (categorized)
	deligoez := owners[0]
	if deligoez.Name != "deligoez" {
		t.Errorf("owners[0].Name = %q, want %q", deligoez.Name, "deligoez")
	}
	if deligoez.IsFlat {
		t.Error("deligoez should not be flat")
	}
	if len(deligoez.Repos) != 2 {
		t.Fatalf("deligoez: expected 2 repos, got %d", len(deligoez.Repos))
	}

	// phonyland owner (flat)
	phonyland := owners[1]
	if phonyland.Name != "phonyland" {
		t.Errorf("owners[1].Name = %q, want %q", phonyland.Name, "phonyland")
	}
	if !phonyland.IsFlat {
		t.Error("phonyland should be flat")
	}
	if len(phonyland.Repos) != 2 {
		t.Fatalf("phonyland: expected 2 repos, got %d", len(phonyland.Repos))
	}

	// Spot-check repo fields
	tp := deligoez.Repos[0]
	if tp.Repo != "deligoez/tp" {
		t.Errorf("tp.Repo = %q, want %q", tp.Repo, "deligoez/tp")
	}
	if tp.CloneURL != "git@github.com:deligoez/tp.git" {
		t.Errorf("tp.CloneURL = %q", tp.CloneURL)
	}
	if tp.Category != "projects" {
		t.Errorf("tp.Category = %q, want %q", tp.Category, "projects")
	}

	cloud := phonyland.Repos[0]
	if cloud.Category != "" {
		t.Errorf("cloud.Category = %q, want empty (flat)", cloud.Category)
	}
}

// Test 2: Categorized path — deligoez owner, projects category, tp repo → ends with /deligoez/projects/tp
func TestCategorizedPath(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	localPath := repos[0].LocalPath
	suffix := filepath.Join("deligoez", "projects", "tp")
	if !strings.HasSuffix(localPath, suffix) {
		t.Errorf("LocalPath %q should end with %q", localPath, suffix)
	}
}

// Test 3: Flat path — phonyland owner (flat), cloud repo → ends with /phonyland/cloud
func TestFlatPath(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
phonyland:
    - repo: phonyland/cloud
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	localPath := repos[0].LocalPath
	suffix := filepath.Join("phonyland", "cloud")
	if !strings.HasSuffix(localPath, suffix) {
		t.Errorf("LocalPath %q should end with %q", localPath, suffix)
	}
	// Must NOT contain a category segment between owner and repo name.
	notWanted := filepath.Join("phonyland", "cloud", "cloud")
	if strings.HasSuffix(localPath, notWanted) {
		t.Errorf("flat path %q must not contain category segment", localPath)
	}
}

// Test 4: Archive path (categorized) — deligoez owner, archive, roast → ends with /deligoez/archive/roast

// Test 5: Archive path (flat) — phonyland (flat), archive, framework → ends with /phonyland/archive/framework

// Test 6: Tilde expansion — base_dir: ~/Developer → expands to real home dir
func TestTildeExpansion(t *testing.T) {
	content := `
base_dir: ~/Developer
deligoez:
  projects:
    - repo: deligoez/tp
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("could not get home dir: %v", err)
	}

	expected := filepath.Join(home, "Developer")
	if m.BaseDir != expected {
		t.Errorf("BaseDir = %q, want %q", m.BaseDir, expected)
	}
	if strings.Contains(m.BaseDir, "~") {
		t.Errorf("BaseDir still contains tilde: %q", m.BaseDir)
	}
}

// Test 7: Missing base_dir — YAML without base_dir → error
func TestMissingBaseDir(t *testing.T) {
	content := `
  deligoez:
    projects:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing base_dir, got nil")
	}
}

// Test 8: Invalid repo format — repo: "invalid" → error
func TestInvalidRepoFormat(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: invalid
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid repo format, got nil")
	}
}

// Test 9: Duplicate repos — same repo in two categories → error
func TestDuplicateRepos(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
    tools:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate repos, got nil")
	}
}

// Test 10: Empty manifest — no owners → error
func TestEmptyManifest(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty manifest (no owners), got nil")
	}
}


// Test 13: Repo with install — install: ["npm install"] → Install: ["npm install"]
func TestRepoWithInstall(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
        install:
          - npm install
          - go build ./...
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	install := repos[0].Install
	if len(install) != 2 {
		t.Fatalf("expected 2 install commands, got %d", len(install))
	}
	if install[0] != "npm install" {
		t.Errorf("install[0] = %q, want %q", install[0], "npm install")
	}
	if install[1] != "go build ./..." {
		t.Errorf("install[1] = %q, want %q", install[1], "go build ./...")
	}
}

// Test 14: Repo without install or update → Install and Update are nil/empty
func TestRepoWithoutCommands(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if len(repos[0].Install) != 0 {
		t.Errorf("expected Install to be empty, got %v", repos[0].Install)
	}
	if len(repos[0].Update) != 0 {
		t.Errorf("expected Update to be empty, got %v", repos[0].Update)
	}
}

// Test 15: Cross-owner repo — repo: acme/tool under owner deligoez → CloneURL has acme/tool, path under deligoez/
func TestCrossOwnerRepo(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: acme/tool
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	r := repos[0]
	if r.CloneURL != "git@github.com:acme/tool.git" {
		t.Errorf("CloneURL = %q, want %q", r.CloneURL, "git@github.com:acme/tool.git")
	}
	// Local path should be under the manifest owner "deligoez", not "acme".
	ownerSegment := filepath.Join(baseDir, "deligoez")
	if !strings.HasPrefix(r.LocalPath, ownerSegment) {
		t.Errorf("LocalPath %q should start with %q (manifest owner)", r.LocalPath, ownerSegment)
	}
	// The repo name segment at the end should be "tool", not "acme/tool".
	if !strings.HasSuffix(r.LocalPath, string(filepath.Separator)+"tool") {
		t.Errorf("LocalPath %q should end with the repo name 'tool'", r.LocalPath)
	}
}

// Test 16: Empty category list — category with [] → error
func TestEmptyCategoryList(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects: []
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty category list, got nil")
	}
}

// Test 17: Manifest order preserved — multiple owners/categories → Repos() in YAML order
func TestManifestOrderPreserved(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
alice:
  alpha:
    - repo: alice/aaa
    - repo: alice/bbb
  beta:
    - repo: alice/ccc
bob:
  gamma:
    - repo: bob/ddd
    - repo: bob/eee
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	expected := []string{"alice/aaa", "alice/bbb", "alice/ccc", "bob/ddd", "bob/eee"}
	if len(repos) != len(expected) {
		t.Fatalf("expected %d repos, got %d", len(expected), len(repos))
	}
	for i, want := range expected {
		if repos[i].Repo != want {
			t.Errorf("repos[%d].Repo = %q, want %q", i, repos[i].Repo, want)
		}
	}
}

// Test 18: flat with non-boolean — flat: 42 → error

// Test 19: Empty string in install — install: [""] → error
func TestEmptyStringInInstall(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
        install:
          - ""
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty string in install, got nil")
	}
}

// --- QA Regression Tests ---

// QA-R1: Empty manifest file should fail with base_dir hint
func TestQA_EmptyManifestFile(t *testing.T) {
	path := writeManifest(t, "")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty manifest")
	}
	if !strings.Contains(err.Error(), "manifest is empty") {
		t.Errorf("error should mention empty manifest: %v", err)
	}
}

// QA-R2: Manifest with only base_dir, no owners
func TestQA_NoOwners(t *testing.T) {
	path := writeManifest(t, "base_dir: /tmp/test\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for no owners")
	}
	if !strings.Contains(err.Error(), "at least one owner") {
		t.Errorf("error should mention owners: %v", err)
	}
}

// QA-R3: flat: 42 (non-boolean) should fail with tag check

// QA-R4: flat as a sequence (reserved key collision)

// QA-R5: Empty category list should fail
func TestQA_EmptyCategoryList(t *testing.T) {
	content := `
base_dir: /tmp/test
owner1:
  projects: []
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty category list")
	}
	if !strings.Contains(err.Error(), "empty repo list") {
		t.Errorf("error should mention empty repo list: %v", err)
	}
}

// QA-R6: Cross-owner repo path uses manifest owner, not github owner
func TestQA_CrossOwnerPath(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
myteam:
    libs:
      - repo: spf13/cobra
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := m.Repos()[0]
	if !strings.Contains(r.LocalPath, "myteam") {
		t.Errorf("path should contain manifest owner 'myteam': %s", r.LocalPath)
	}
	if strings.Contains(r.LocalPath, "spf13") {
		t.Errorf("path should NOT contain github owner 'spf13': %s", r.LocalPath)
	}
	if r.CloneURL != "git@github.com:spf13/cobra.git" {
		t.Errorf("clone URL should use github owner: %s", r.CloneURL)
	}
}

func TestFlatOwnerAsSequence(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
myowner:
    - repo: myowner/repo1
    - repo: myowner/repo2
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owners := m.Owners()
	if len(owners) != 1 {
		t.Fatalf("expected 1 owner, got %d", len(owners))
	}
	if !owners[0].IsFlat {
		t.Error("sequence owner should be flat")
	}
	for _, r := range owners[0].Repos {
		if r.Category != "" {
			t.Errorf("flat repo %q should have empty category, got %q", r.Repo, r.Category)
		}
	}
}

func TestCategorizedOwnerAsMapping(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
myowner:
    tools:
      - repo: myowner/tool1
    libs:
      - repo: myowner/lib1
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owners := m.Owners()
	if owners[0].IsFlat {
		t.Error("mapping owner should not be flat")
	}
	if owners[0].Repos[0].Category != "tools" {
		t.Errorf("expected category 'tools', got %q", owners[0].Repos[0].Category)
	}
}

func TestOwnerValueScalarError(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
myowner: invalid
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for scalar owner value, got nil")
	}
}

func TestMixedOwnerTypes(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
catowner:
  projects:
    - repo: catowner/proj1
flatowner:
  - repo: flatowner/repo1
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owners := m.Owners()
	if len(owners) != 2 {
		t.Fatalf("expected 2 owners, got %d", len(owners))
	}
	if owners[0].IsFlat {
		t.Error("catowner should not be flat")
	}
	if !owners[1].IsFlat {
		t.Error("flatowner should be flat")
	}
}

// QA v0.5.0 — Q1: owners key rejected with migration hint
func TestQA_OwnersKeyRejectsWithHint(t *testing.T) {
	content := `
base_dir: /tmp/test
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for legacy owners key")
	}
	if !strings.Contains(err.Error(), `"owners" is no longer a valid manifest key`) {
		t.Errorf("error should contain migration message, got: %v", err)
	}
	var he *output.HintError
	if !errors.As(err, &he) {
		t.Fatalf("expected HintError, got %T", err)
	}
	if !strings.Contains(he.Hint, "dedent owner blocks by one level") {
		t.Errorf("hint should contain dedent instruction, got: %q", he.Hint)
	}
}

// QA v0.5.0 — Q2: root not mapping
func TestQA_RootNotMapping(t *testing.T) {
	content := `
- item1
- item2
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for non-mapping root")
	}
	if !strings.Contains(err.Error(), "manifest must be a YAML mapping at the top level") {
		t.Errorf("error should mention mapping requirement, got: %v", err)
	}
}

// QA v0.5.0 — Q3: owners key short-circuits before base_dir validation
func TestQA_OwnersKeyShortCircuits(t *testing.T) {
	// Manifest has owners: but no base_dir — only the owners error should appear.
	content := `
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"owners" is no longer a valid manifest key`) {
		t.Errorf("should get owners migration error, got: %v", err)
	}
	if strings.Contains(err.Error(), "base_dir") {
		t.Errorf("should NOT get base_dir error when owners key is present, got: %v", err)
	}
}

// Test: deps key rejected with migration hint
func TestDepsKeyRejected(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
        deps:
          - npm install
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for removed deps key, got nil")
	}
	if !strings.Contains(err.Error(), `removed key "deps"`) {
		t.Errorf("error should mention removed deps key, got: %v", err)
	}
	var he *output.HintError
	if !errors.As(err, &he) {
		t.Fatalf("expected HintError, got %T", err)
	}
	if !strings.Contains(he.Hint, `Rename "deps:" to "install:"`) {
		t.Errorf("hint should contain rename instruction, got: %q", he.Hint)
	}
}

// Test: repo with both install and update
func TestRepoWithInstallAndUpdate(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
        install:
          - npm install
        update:
          - npm update
          - go mod tidy
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repos := m.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	r := repos[0]
	if len(r.Install) != 1 {
		t.Fatalf("expected 1 install command, got %d", len(r.Install))
	}
	if r.Install[0] != "npm install" {
		t.Errorf("Install[0] = %q, want %q", r.Install[0], "npm install")
	}
	if len(r.Update) != 2 {
		t.Fatalf("expected 2 update commands, got %d", len(r.Update))
	}
	if r.Update[0] != "npm update" {
		t.Errorf("Update[0] = %q, want %q", r.Update[0], "npm update")
	}
	if r.Update[1] != "go mod tidy" {
		t.Errorf("Update[1] = %q, want %q", r.Update[1], "go mod tidy")
	}
}

// Test: empty string in update list
func TestEmptyStringInUpdate(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
deligoez:
    projects:
      - repo: deligoez/tp
        update:
          - ""
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty string in update, got nil")
	}
}
