package manifest_test

import (
	"testing"

	"github.com/deligoez/rp/internal/manifest"
)

var testRepos = []manifest.RepoEntry{
	{Repo: "deligoez/tp", Owner: "deligoez"},
	{Repo: "deligoez/blog", Owner: "deligoez"},
	{Repo: "phonyland/cloud", Owner: "phonyland"},
	{Repo: "phonyland/framework", Owner: "phonyland"},
}

// Test 1: Exact repo filter returns only the matching repo.
func TestFilterRepos_ExactMatch(t *testing.T) {
	result := manifest.FilterRepos(testRepos, []string{"deligoez/tp"})

	if len(result) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(result))
	}
	if result[0].Repo != "deligoez/tp" {
		t.Errorf("expected deligoez/tp, got %q", result[0].Repo)
	}
}

// Test 2: Owner prefix with trailing slash returns all repos for that owner.
func TestFilterRepos_OwnerPrefixWithSlash(t *testing.T) {
	result := manifest.FilterRepos(testRepos, []string{"deligoez/"})

	if len(result) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(result))
	}
	for _, r := range result {
		if r.Owner != "deligoez" {
			t.Errorf("expected owner deligoez, got %q (repo %q)", r.Owner, r.Repo)
		}
	}
}

// Test 3: Owner prefix without slash returns the same result as with slash.
func TestFilterRepos_OwnerPrefixWithoutSlash(t *testing.T) {
	withSlash := manifest.FilterRepos(testRepos, []string{"deligoez/"})
	withoutSlash := manifest.FilterRepos(testRepos, []string{"deligoez"})

	if len(withSlash) != len(withoutSlash) {
		t.Fatalf("expected same length: with slash %d, without slash %d", len(withSlash), len(withoutSlash))
	}
	for i := range withSlash {
		if withSlash[i].Repo != withoutSlash[i].Repo {
			t.Errorf("index %d: with slash %q, without slash %q", i, withSlash[i].Repo, withoutSlash[i].Repo)
		}
	}
}

// Test 4: Multiple filters return the union of matches.
func TestFilterRepos_MultipleFilters(t *testing.T) {
	result := manifest.FilterRepos(testRepos, []string{"deligoez/", "phonyland/cloud"})

	// Expects deligoez/tp, deligoez/blog, phonyland/cloud — 3 repos total.
	if len(result) != 3 {
		t.Fatalf("expected 3 repos, got %d", len(result))
	}

	want := map[string]bool{
		"deligoez/tp":    true,
		"deligoez/blog":  true,
		"phonyland/cloud": true,
	}
	for _, r := range result {
		if !want[r.Repo] {
			t.Errorf("unexpected repo %q in result", r.Repo)
		}
	}
}

// Test 5: No matches returns an empty (non-nil) slice of length 0.
func TestFilterRepos_NoMatches(t *testing.T) {
	result := manifest.FilterRepos(testRepos, []string{"nonexistent/repo"})

	if result == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(result) != 0 {
		t.Errorf("expected length 0, got %d", len(result))
	}
}
