package cmd

import (
	"testing"

	"github.com/deligoez/rp/internal/git"
	"github.com/deligoez/rp/internal/manifest"
)

func TestNeedsAttention(t *testing.T) {
	cases := []struct {
		name   string
		status git.RepoStatus
		want   bool
	}{
		{"clean and in sync", git.RepoStatus{Clean: true, HasUpstream: true}, false},
		{"dirty", git.RepoStatus{Clean: false, DirtyFiles: 2, HasUpstream: true}, true},
		{"ahead", git.RepoStatus{Clean: true, HasUpstream: true, Ahead: 1}, true},
		{"behind", git.RepoStatus{Clean: true, HasUpstream: true, Behind: 3}, true},

		// A branch that was never pushed has nothing to be ahead or behind of,
		// so it is OK rather than something to act on.
		{"clean without upstream", git.RepoStatus{Clean: true}, false},
		{"ahead counted without upstream", git.RepoStatus{Clean: true, Ahead: 5}, false},
		{"behind counted without upstream", git.RepoStatus{Clean: true, Behind: 5}, false},

		// Dirty always wins, upstream or not.
		{"dirty without upstream", git.RepoStatus{Clean: false, DirtyFiles: 1}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsAttention(tc.status); got != tc.want {
				t.Errorf("needsAttention(%+v) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestStatusDetails(t *testing.T) {
	cases := []struct {
		name   string
		status git.RepoStatus
		want   string
	}{
		{"clean", git.RepoStatus{Branch: "main", Clean: true, HasUpstream: true}, "main"},
		{"dirty", git.RepoStatus{Branch: "main", DirtyFiles: 3, HasUpstream: true}, "main ~3 dirty"},
		{"ahead", git.RepoStatus{Branch: "main", Clean: true, HasUpstream: true, Ahead: 2}, "main +2 ahead"},
		{"behind", git.RepoStatus{Branch: "main", Clean: true, HasUpstream: true, Behind: 4}, "main -4 behind"},
		{"diverged", git.RepoStatus{Branch: "dev", Clean: true, HasUpstream: true, Ahead: 1, Behind: 2}, "dev +1 ahead -2 behind"},
		{"everything", git.RepoStatus{Branch: "dev", DirtyFiles: 1, HasUpstream: true, Ahead: 1, Behind: 1}, "dev ~1 dirty +1 ahead -1 behind"},

		// Without an upstream the counts are meaningless and must not be shown.
		{"counts hidden without upstream", git.RepoStatus{Branch: "wip", Clean: true, Ahead: 9, Behind: 9}, "wip"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusDetails(tc.status); got != tc.want {
				t.Errorf("statusDetails = %q, want %q", got, tc.want)
			}
		})
	}
}

// jsonRepo builds a statusRepoJSON for a cloned repo with the given counts.
func jsonRepo(repo string, clean bool, dirty, ahead, behind int, upstream bool) statusRepoJSON {
	return statusRepoJSON{
		Repo:        repo,
		Cloned:      true,
		Clean:       &clean,
		DirtyFiles:  &dirty,
		Ahead:       &ahead,
		Behind:      &behind,
		HasUpstream: &upstream,
	}
}

func TestStatusPassesFiltersWithNoFlags(t *testing.T) {
	if !statusPassesFilters(jsonRepo("a/b", true, 0, 0, 0, true)) {
		t.Error("with no flags set every repo must pass")
	}
	if !statusPassesFilters(statusRepoJSON{Repo: "a/b", Cloned: false}) {
		t.Error("an uncloned repo must pass when no flags are set")
	}
}

func TestStatusPassesFiltersDirty(t *testing.T) {
	statusDirty = true
	t.Cleanup(func() { statusDirty = false })

	if statusPassesFilters(jsonRepo("a/b", true, 0, 0, 0, true)) {
		t.Error("--dirty must drop a clean repo")
	}
	if !statusPassesFilters(jsonRepo("a/b", false, 2, 0, 0, true)) {
		t.Error("--dirty must keep a dirty repo")
	}
	// An uncloned repo has no Clean pointer at all; it cannot be dirty.
	if statusPassesFilters(statusRepoJSON{Repo: "a/b"}) {
		t.Error("--dirty must drop a repo with no recorded state")
	}
}

func TestStatusPassesFiltersAheadAndBehind(t *testing.T) {
	statusAhead = true
	t.Cleanup(func() { statusAhead = false })

	if statusPassesFilters(jsonRepo("a/b", true, 0, 0, 5, true)) {
		t.Error("--ahead must drop a repo that is only behind")
	}
	if !statusPassesFilters(jsonRepo("a/b", true, 0, 1, 0, true)) {
		t.Error("--ahead must keep a repo that is ahead")
	}

	statusBehind = true
	t.Cleanup(func() { statusBehind = false })

	// Both flags now set: they are ANDed, so only a repo that is both ahead
	// and behind survives.
	if statusPassesFilters(jsonRepo("a/b", true, 0, 1, 0, true)) {
		t.Error("--ahead --behind must drop a repo that is only ahead")
	}
	if !statusPassesFilters(jsonRepo("a/b", true, 0, 1, 1, true)) {
		t.Error("--ahead --behind must keep a diverged repo")
	}
}

func TestCountStatusJSON(t *testing.T) {
	repos := []statusRepoJSON{
		jsonRepo("ok/one", true, 0, 0, 0, true),
		jsonRepo("ok/two", true, 0, 0, 0, false),
		jsonRepo("dirty/one", false, 1, 0, 0, true),
		jsonRepo("ahead/one", true, 0, 2, 0, true),
		jsonRepo("behind/one", true, 0, 0, 3, true),
		{Repo: "missing/one", Cloned: false},
	}

	ok, attention, notCloned := countStatusJSON(repos)

	if ok != 2 {
		t.Errorf("ok = %d, want 2", ok)
	}
	if attention != 3 {
		t.Errorf("attention = %d, want 3", attention)
	}
	if notCloned != 1 {
		t.Errorf("notCloned = %d, want 1", notCloned)
	}
}

func TestFilterStatusLinesPairsRowsWithJSONByIndex(t *testing.T) {
	statusDirty = true
	t.Cleanup(func() { statusDirty = false })

	lines := []statusOwnerLines{
		{name: "acme", repos: []statusRepoLine{{label: "clean"}, {label: "dirty"}}},
		{name: "solo", repos: []statusRepoLine{{label: "also-clean"}}},
	}
	jsonRepos := []statusRepoJSON{
		jsonRepo("acme/clean", true, 0, 0, 0, true),
		jsonRepo("acme/dirty", false, 1, 0, 0, true),
		jsonRepo("solo/also-clean", true, 0, 0, 0, true),
	}

	got := filterStatusLines(lines, jsonRepos)

	// Only acme survives, and only its dirty row: an owner whose repos are all
	// filtered out is dropped entirely.
	if len(got) != 1 {
		t.Fatalf("got %d owner blocks, want 1", len(got))
	}
	if got[0].name != "acme" {
		t.Errorf("surviving owner = %q, want acme", got[0].name)
	}
	if len(got[0].repos) != 1 || got[0].repos[0].label != "dirty" {
		t.Errorf("surviving rows = %v, want just the dirty one", got[0].repos)
	}
}

