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

// --- QA Regression Tests ---

// QA-R11: Empty string filter matches nothing
func TestQA_FilterEmptyString(t *testing.T) {
	result := manifest.FilterRepos(testRepos, []string{""})
	if len(result) != 0 {
		t.Errorf("empty string filter should match nothing, got %d", len(result))
	}
}

// QA-R12: Overlapping filters don't produce duplicates
func TestQA_FilterOverlappingNoDupes(t *testing.T) {
	result := manifest.FilterRepos(testRepos, []string{"deligoez/", "deligoez/tp"})
	// deligoez/ matches 2 repos, deligoez/tp also matches 1 of those — union should be 2
	if len(result) != 2 {
		t.Errorf("expected 2 (no dupes), got %d", len(result))
	}
	seen := make(map[string]bool)
	for _, r := range result {
		if seen[r.Repo] {
			t.Errorf("duplicate repo in result: %s", r.Repo)
		}
		seen[r.Repo] = true
	}
}

// QA-R13: Filter on archive repo by exact match
func TestQA_FilterArchiveRepo(t *testing.T) {
	repos := []manifest.RepoEntry{
		{Repo: "owner/regular", Owner: "owner"},
		{Repo: "owner/archived", Owner: "owner", IsArchive: true},
	}
	result := manifest.FilterRepos(repos, []string{"owner/archived"})
	if len(result) != 1 || result[0].Repo != "owner/archived" {
		t.Errorf("expected only owner/archived, got %v", result)
	}
}

// QA-R14: FilterOwners removes empty owner groups
func TestQA_FilterOwnersRemovesEmpty(t *testing.T) {
	owners := []manifest.OwnerGroup{
		{Name: "alice", Repos: []manifest.RepoEntry{{Repo: "alice/a", Owner: "alice"}}},
		{Name: "bob", Repos: []manifest.RepoEntry{{Repo: "bob/b", Owner: "bob"}}},
	}
	result := manifest.FilterOwners(owners, []string{"alice/"})
	if len(result) != 1 {
		t.Errorf("expected 1 owner group, got %d", len(result))
	}
	if result[0].Name != "alice" {
		t.Errorf("expected alice, got %s", result[0].Name)
	}
}
