package cmd

import (
	"path/filepath"
	"testing"
)

func TestParseGitHubRemote(t *testing.T) {
	cases := []struct {
		name        string
		url         string
		owner, repo string
	}{
		{"ssh with .git", "git@github.com:deligoez/rp.git", "deligoez", "rp"},
		{"ssh without .git", "git@github.com:deligoez/rp", "deligoez", "rp"},
		{"https with .git", "https://github.com/deligoez/rp.git", "deligoez", "rp"},
		{"https without .git", "https://github.com/deligoez/rp", "deligoez", "rp"},
		{"surrounding whitespace", "  git@github.com:deligoez/rp.git\n", "deligoez", "rp"},
		{"name containing dots", "git@github.com:deligoez/my.tool.git", "deligoez", "my.tool"},
		{"name ending in git", "git@github.com:deligoez/gogit.git", "deligoez", "gogit"},

		{"other host over ssh", "git@gitlab.com:deligoez/rp.git", "", ""},
		{"other host over https", "https://gitlab.com/deligoez/rp.git", "", ""},
		{"http is not https", "http://github.com/deligoez/rp", "", ""},
		{"extra path segment", "https://github.com/deligoez/rp/extra", "", ""},
		{"missing repo", "git@github.com:deligoez", "", ""},
		{"empty", "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, repo := parseGitHubRemote(tc.url)
			if owner != tc.owner || repo != tc.repo {
				t.Errorf("parseGitHubRemote(%q) = (%q, %q), want (%q, %q)", tc.url, owner, repo, tc.owner, tc.repo)
			}
		})
	}
}

func TestInferOwnerDirFindsAncestorByName(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "deligoez", "projects", "rp")

	got, found := inferOwnerDir(repo, root, "deligoez")

	if !found {
		t.Error("expected the owner directory to be found by name")
	}
	if want := filepath.Join(root, "deligoez"); got != want {
		t.Errorf("inferOwnerDir = %q, want %q", got, want)
	}
}

func TestInferOwnerDirMatchesCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "Deligoez", "rp")

	got, found := inferOwnerDir(repo, root, "deligoez")

	if !found {
		t.Error("expected a case-insensitive match on the owner directory")
	}
	if want := filepath.Join(root, "Deligoez"); got != want {
		t.Errorf("inferOwnerDir = %q, want %q", got, want)
	}
}

func TestInferOwnerDirPrefersTheNearestMatch(t *testing.T) {
	// The owner name appears twice on the path; the one closest to the repo
	// wins, because that is the directory actually grouping it.
	root := t.TempDir()
	repo := filepath.Join(root, "acme", "vendor", "acme", "tool")

	got, _ := inferOwnerDir(repo, root, "acme")

	if want := filepath.Join(root, "acme", "vendor", "acme"); got != want {
		t.Errorf("inferOwnerDir = %q, want the nearest match %q", got, want)
	}
}

func TestInferOwnerDirFallsBackToTheParent(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "somewhere", "rp")

	got, found := inferOwnerDir(repo, root, "deligoez")

	if found {
		t.Error("no ancestor is named after the owner, so found must be false")
	}
	if want := filepath.Join(root, "somewhere"); got != want {
		t.Errorf("inferOwnerDir = %q, want the immediate parent %q", got, want)
	}
}

func TestInferOwnerDirStopsAtTheScanRoot(t *testing.T) {
	// A directory above the scan root shares the owner's name; the walk must
	// not reach it, because --dir is the boundary of the scan.
	// A directory named after the owner sits above the scan root.
	base := filepath.Join(t.TempDir(), "acme")
	root := filepath.Join(base, "root")
	repo := filepath.Join(root, "projects", "tool")

	got, found := inferOwnerDir(repo, root, "acme")

	if found {
		t.Error("the walk must not climb above the scan root")
	}
	if want := filepath.Join(root, "projects"); got != want {
		t.Errorf("inferOwnerDir = %q, want %q", got, want)
	}
}

