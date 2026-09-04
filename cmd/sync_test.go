package cmd

import (
	"testing"

	"github.com/deligoez/rp/internal/git"
)

func TestSyncSkipUnsafeAllowsACleanRepo(t *testing.T) {
	s := git.RepoStatus{Clean: true, Branch: "main", HasUpstream: true}

	if _, skip := syncSkipUnsafe(s, "label", "a/b", false); skip {
		t.Error("a clean repo with nothing unpushed is safe to pull")
	}
}

func TestSyncSkipUnsafeSkipsDirty(t *testing.T) {
	s := git.RepoStatus{DirtyFiles: 3, Branch: "main", HasUpstream: true}

	res, skip := syncSkipUnsafe(s, "label", "a/b", false)

	if !skip {
		t.Fatal("a dirty repo must be skipped")
	}
	if res.action != syncActionSkipped {
		t.Errorf("action = %v, want skipped", res.action)
	}
	if res.skipReason != syncSkipDirty {
		t.Errorf("reason = %v, want dirty", res.skipReason)
	}
	if res.dirtyFiles != 3 {
		t.Errorf("dirtyFiles = %d, want 3", res.dirtyFiles)
	}
	if res.exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", res.exitCode)
	}
}

func TestSyncSkipUnsafeSkipsUnpushed(t *testing.T) {
	s := git.RepoStatus{Clean: true, Ahead: 2, Branch: "feature", HasUpstream: true}

	res, skip := syncSkipUnsafe(s, "label", "a/b", false)

	if !skip {
		t.Fatal("a repo with unpushed commits must be skipped")
	}
	if res.skipReason != syncSkipUnpushed {
		t.Errorf("reason = %v, want unpushed", res.skipReason)
	}
	if res.ahead != 2 || res.branch != "feature" {
		t.Errorf("ahead/branch = %d/%q, want 2/feature", res.ahead, res.branch)
	}
}

func TestSyncSkipUnsafePrefersDirtyOverUnpushed(t *testing.T) {
	// Both conditions hold. Dirty is the more urgent one to report, and the
	// evaluation order is part of the documented behaviour.
	s := git.RepoStatus{DirtyFiles: 1, Ahead: 4, Branch: "main", HasUpstream: true}

	res, skip := syncSkipUnsafe(s, "label", "a/b", false)

	if !skip {
		t.Fatal("expected a skip")
	}
	if res.skipReason != syncSkipDirty {
		t.Errorf("reason = %v, want dirty to win over unpushed", res.skipReason)
	}
}

func TestSyncSkipUnsafeIgnoresAheadWithoutUpstream(t *testing.T) {
	// Without an upstream there is nothing to be ahead of, so the repo is not
	// holding unpushed work in any meaningful sense.
	s := git.RepoStatus{Clean: true, Ahead: 7, Branch: "wip"}

	if _, skip := syncSkipUnsafe(s, "label", "a/b", false); skip {
		t.Error("ahead without an upstream must not trigger a skip")
	}
}

func TestSyncSkipUnsafeDryRunReportsWouldSkip(t *testing.T) {
	s := git.RepoStatus{DirtyFiles: 1, Branch: "main", HasUpstream: true}

	res, _ := syncSkipUnsafe(s, "label", "a/b", true)

	if res.action != syncActionWouldSkip {
		t.Errorf("action = %v, want would_skip under --dry-run", res.action)
	}
	if res.exitCode != 0 {
		t.Errorf("exitCode = %d, want 0: a dry run reports, it does not fail", res.exitCode)
	}
	if res.skipReason != syncSkipDirty {
		t.Errorf("reason = %v, want the same reason as a real run", res.skipReason)
	}
}

func TestSetSyncSkipReason(t *testing.T) {
	cases := []struct {
		name       string
		result     syncResult
		wantReason string
		check      func(t *testing.T, rj syncRepoJSON)
	}{
		{
			name:       "dirty carries the file count",
			result:     syncResult{skipReason: syncSkipDirty, dirtyFiles: 4},
			wantReason: "dirty",
			check: func(t *testing.T, rj syncRepoJSON) {
				if rj.DirtyFiles != 4 {
					t.Errorf("DirtyFiles = %d, want 4", rj.DirtyFiles)
				}
			},
		},
		{
			name:       "unpushed carries ahead and branch",
			result:     syncResult{skipReason: syncSkipUnpushed, ahead: 2, branch: "main"},
			wantReason: "unpushed",
			check: func(t *testing.T, rj syncRepoJSON) {
				if rj.Ahead != 2 || rj.Branch != "main" {
					t.Errorf("Ahead/Branch = %d/%q, want 2/main", rj.Ahead, rj.Branch)
				}
			},
		},
		{name: "diverged", result: syncResult{skipReason: syncSkipDiverged}, wantReason: "diverged"},
		{name: "no upstream", result: syncResult{skipReason: syncSkipNoUpstream}, wantReason: "no_upstream"},
		{name: "not a repo", result: syncResult{skipReason: syncSkipNotARepo}, wantReason: "not_a_repo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rj syncRepoJSON
			setSyncSkipReason(&rj, tc.result)

			if rj.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", rj.Reason, tc.wantReason)
			}
			if tc.check != nil {
				tc.check(t, rj)
			}
		})
	}
}

func TestSetSyncSkipReasonLeavesNonSkipsAlone(t *testing.T) {
	var rj syncRepoJSON
	setSyncSkipReason(&rj, syncResult{skipReason: syncSkipNone})

	if rj.Reason != "" {
		t.Errorf("Reason = %q, want it left empty when there is no skip", rj.Reason)
	}
}
