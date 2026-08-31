package cmd

import (
	"fmt"
	"os"

	"github.com/deligoez/rp/internal/manifest"
	"github.com/deligoez/rp/internal/runner"
)

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

// runRepoCommands executes one repo's commands in its directory, stopping at
// the first failure. A repo missing from disk is reported as skipped rather
// than failed: bootstrap may simply not have cloned it.
func runRepoCommands(entry manifest.RepoEntry, commands []string) repoCommandResult {
	result := repoCommandResult{entry: entry}

	if _, err := os.Stat(entry.LocalPath); os.IsNotExist(err) {
		result.skipped = true
		result.skipMsg = fmt.Sprintf("warning: %s not found on disk, skipping", entry.LocalPath)
		return result
	}

	for _, command := range commands {
		outcome := commandOutcome{command: command}
		if err := runner.RunCommands(entry.LocalPath, []string{command}); err != nil {
			outcome.failed = true
			outcome.errMsg = err.Error()
			result.results = append(result.results, outcome)
			break
		}
		result.results = append(result.results, outcome)
	}

	return result
}
