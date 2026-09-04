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

