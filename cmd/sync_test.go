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

