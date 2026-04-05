package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
      - repo: deligoez/rp
    archive:
      - repo: deligoez/roast
  phonyland:
    flat: true
    cloud:
      - repo: phonyland/cloud
    archive:
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

	// deligoez owner
	deligoez := owners[0]
	if deligoez.Name != "deligoez" {
		t.Errorf("owners[0].Name = %q, want %q", deligoez.Name, "deligoez")
	}
	if deligoez.IsFlat {
		t.Error("deligoez should not be flat")
	}
	if len(deligoez.Repos) != 3 {
		t.Fatalf("deligoez: expected 3 repos, got %d", len(deligoez.Repos))
	}

	// phonyland owner
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
	if tp.IsArchive {
		t.Error("tp should not be archive")
	}

	roast := deligoez.Repos[2]
	if !roast.IsArchive {
		t.Error("roast should be archive")
	}

	cloud := phonyland.Repos[0]
	if !cloud.IsFlat {
		t.Error("cloud should be flat")
	}
}

// Test 2: Categorized path — deligoez owner, projects category, tp repo → ends with /deligoez/projects/tp
func TestCategorizedPath(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
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
owners:
  phonyland:
    flat: true
    cloud:
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
	// Must NOT contain the category segment between owner and repo name.
	notWanted := filepath.Join("phonyland", "cloud", "cloud")
	if strings.HasSuffix(localPath, notWanted) {
		t.Errorf("flat path %q must not contain category segment", localPath)
	}
}

// Test 4: Archive path (categorized) — deligoez owner, archive, roast → ends with /deligoez/archive/roast
func TestArchivePathCategorized(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
    archive:
      - repo: deligoez/roast
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var archiveRepo *RepoEntry
	for i, r := range m.Repos() {
		if r.IsArchive {
			archiveRepo = &m.Repos()[i]
			break
		}
	}
	if archiveRepo == nil {
		t.Fatal("expected an archive repo")
	}

	suffix := filepath.Join("deligoez", "archive", "roast")
	if !strings.HasSuffix(archiveRepo.LocalPath, suffix) {
		t.Errorf("LocalPath %q should end with %q", archiveRepo.LocalPath, suffix)
	}
}

// Test 5: Archive path (flat) — phonyland (flat), archive, framework → ends with /phonyland/archive/framework
func TestArchivePathFlat(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  phonyland:
    flat: true
    cloud:
      - repo: phonyland/cloud
    archive:
      - repo: phonyland/framework
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var archiveRepo *RepoEntry
	for i, r := range m.Repos() {
		if r.IsArchive {
			archiveRepo = &m.Repos()[i]
			break
		}
	}
	if archiveRepo == nil {
		t.Fatal("expected an archive repo")
	}

	suffix := filepath.Join("phonyland", "archive", "framework")
	if !strings.HasSuffix(archiveRepo.LocalPath, suffix) {
		t.Errorf("LocalPath %q should end with %q", archiveRepo.LocalPath, suffix)
	}
}

// Test 6: Tilde expansion — base_dir: ~/Developer → expands to real home dir
func TestTildeExpansion(t *testing.T) {
	content := `
base_dir: ~/Developer
owners:
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
owners:
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
owners:
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
owners:
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
owners: {}
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty manifest (no owners), got nil")
	}
}

// Test 11: Reserved category name "flat" → error
func TestReservedCategoryNameFlat(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  deligoez:
    flat:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for reserved category name 'flat', got nil")
	}
}

// Test 12: Archive entries have IsArchive — archive repos → IsArchive: true
func TestArchiveEntriesHaveIsArchive(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
    archive:
      - repo: deligoez/roast
      - repo: deligoez/old-project
`
	path := writeManifest(t, content)
	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, r := range m.Repos() {
		if r.Repo == "deligoez/roast" || r.Repo == "deligoez/old-project" {
			if !r.IsArchive {
				t.Errorf("repo %q: expected IsArchive=true", r.Repo)
			}
		}
		if r.Repo == "deligoez/tp" {
			if r.IsArchive {
				t.Errorf("repo %q: expected IsArchive=false", r.Repo)
			}
		}
	}
}

// Test 13: Repo with deps — deps: ["npm install"] → Deps: ["npm install"]
func TestRepoWithDeps(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
        deps:
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

	deps := repos[0].Deps
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(deps))
	}
	if deps[0] != "npm install" {
		t.Errorf("deps[0] = %q, want %q", deps[0], "npm install")
	}
	if deps[1] != "go build ./..." {
		t.Errorf("deps[1] = %q, want %q", deps[1], "go build ./...")
	}
}

// Test 14: Repo without deps → Deps is nil/empty
func TestRepoWithoutDeps(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
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
	if len(repos[0].Deps) != 0 {
		t.Errorf("expected Deps to be empty, got %v", repos[0].Deps)
	}
}

// Test 15: Cross-owner repo — repo: acme/tool under owner deligoez → CloneURL has acme/tool, path under deligoez/
func TestCrossOwnerRepo(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
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
owners:
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
owners:
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
func TestFlatNonBoolean(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  deligoez:
    flat: 42
    projects:
      - repo: deligoez/tp
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for non-boolean flat value, got nil")
	}
}

// Test 19: Empty string in deps — deps: [""] → error
func TestEmptyStringInDeps(t *testing.T) {
	baseDir := t.TempDir()
	content := `
base_dir: ` + baseDir + `
owners:
  deligoez:
    projects:
      - repo: deligoez/tp
        deps:
          - ""
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty string in deps, got nil")
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
	if !strings.Contains(err.Error(), "base_dir") {
		t.Errorf("error should mention base_dir: %v", err)
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
func TestQA_FlatNonBoolInt(t *testing.T) {
	content := `
base_dir: /tmp/test
owners:
  owner1:
    flat: 42
    repos:
      - repo: test/repo
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for flat: 42")
	}
	if !strings.Contains(err.Error(), "boolean") {
		t.Errorf("error should mention boolean: %v", err)
	}
}

// QA-R4: flat as a sequence (reserved key collision)
func TestQA_FlatAsSequence(t *testing.T) {
	content := `
base_dir: /tmp/test
owners:
  owner1:
    flat:
      - repo: test/repo
`
	path := writeManifest(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for flat as sequence")
	}
}

// QA-R5: Empty category list should fail
func TestQA_EmptyCategoryList(t *testing.T) {
	content := `
base_dir: /tmp/test
owners:
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
owners:
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
