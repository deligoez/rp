package cmd

import "github.com/deligoez/rp/internal/manifest"

// repoCommandResult holds the outcome of running a repo's manifest commands.
// install and update differ only in which command list they read, so they
// share one result shape.
type repoCommandResult struct {
	entry   manifest.RepoEntry
	skipped bool
	skipMsg string
	results []commandOutcome
}

// commandOutcome holds the outcome of a single command within a repo.
type commandOutcome struct {
	command string
	failed  bool
	errMsg  string
}
