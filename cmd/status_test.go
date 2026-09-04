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

