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

