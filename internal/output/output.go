package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// SuccessResult is used when a command succeeds (with or without warnings).
// Summary and Repos are always present — use make([]T, 0) for empty repos, never nil.
type SuccessResult struct {
	Command  string      `json:"command"`
	ExitCode int         `json:"exit_code"`
	DryRun   bool        `json:"dry_run,omitempty"`
	Summary  interface{} `json:"summary"`
	Repos    interface{} `json:"repos"`
}

// ErrorResult is used when a command fails with a hard error.
// Summary and Repos are never present.
type ErrorResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error"`
	Hint     string `json:"hint,omitempty"`
}

// UpResult is for the composite rp up command.
// Uses per-phase sub-objects instead of top-level summary/repos.
type UpResult struct {
	Command   string     `json:"command"`
	ExitCode  int        `json:"exit_code"`
	DryRun    bool       `json:"dry_run,omitempty"`
	Bootstrap *SubResult `json:"bootstrap,omitempty"`
	Sync      *SubResult `json:"sync,omitempty"`
	Install   *SubResult `json:"install"`
	Update    *SubResult `json:"update"`
}

// SubResult holds one phase's output within an UpResult.
type SubResult struct {
	Summary interface{} `json:"summary"`
	Repos   interface{} `json:"repos,omitempty"`
}

// HintError wraps an error with an actionable hint for the user.
type HintError struct {
	Err  error
	Hint string
}

func (e *HintError) Error() string { return e.Err.Error() }
func (e *HintError) Unwrap() error { return e.Err }

// NewHintError creates a HintError from a message and hint.
func NewHintError(err error, hint string) *HintError {
	return &HintError{Err: err, Hint: hint}
}

// jsonMode tracks whether JSON output is enabled.
var jsonMode bool

// SetJSON enables or disables JSON output mode.
func SetJSON(v bool) {
	jsonMode = v
}

// IsJSON reports whether JSON output mode is active.
// Checks the package variable (set by SetJSON) and falls back to RP_JSON env var.
func IsJSON() bool {
	if jsonMode {
		return true
	}
	return os.Getenv("RP_JSON") != ""
}

// compact tracks whether compact mode is enabled.
var compact bool

// SetCompact enables or disables compact mode.
func SetCompact(v bool) {
	compact = v
}

// IsCompact reports whether compact mode is active.
func IsCompact() bool {
	return compact
}

// PrintAndExit writes the result as JSON to stdout and exits with the given exit code.
func PrintAndExit(v interface{}) {
	var exitCode int
	switch r := v.(type) {
	case SuccessResult:
		exitCode = r.ExitCode
		if compact {
			r.Repos = nil
			v = struct {
				Command  string      `json:"command"`
				ExitCode int         `json:"exit_code"`
				DryRun   bool        `json:"dry_run,omitempty"`
				Summary  interface{} `json:"summary"`
			}{r.Command, r.ExitCode, r.DryRun, r.Summary}
		}
	case ErrorResult:
		exitCode = r.ExitCode
	case UpResult:
		exitCode = r.ExitCode
		if compact {
			if r.Bootstrap != nil {
				r.Bootstrap.Repos = nil
			}
			if r.Sync != nil {
				r.Sync.Repos = nil
			}
			if r.Install != nil {
				r.Install.Repos = nil
			}
			if r.Update != nil {
				r.Update.Repos = nil
			}
			v = r
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	os.Exit(exitCode)
}

// PrintErrorAndExit prints an ErrorResult for the given command and error, then exits.
// Default exit code is 2. Use PrintErrorAndExitCode for a custom exit code.
func PrintErrorAndExit(command string, err error) {
	PrintErrorAndExitCode(command, err, 2)
}

// PrintErrorAndExitCode prints an ErrorResult with a specific exit code.
func PrintErrorAndExitCode(command string, err error, exitCode int) {
	result := ErrorResult{
		Command:  command,
		ExitCode: exitCode,
		Error:    err.Error(),
	}
	var hintErr *HintError
	if errors.As(err, &hintErr) {
		result.Hint = hintErr.Hint
	}
	PrintAndExit(result)
}

// FormatHumanError formats an error for human display (stderr).
func FormatHumanError(err error) string {
	msg := fmt.Sprintf("error: %s", err.Error())
	var hintErr *HintError
	if errors.As(err, &hintErr) {
		msg += fmt.Sprintf("\nhint:  %s", hintErr.Hint)
	}
	return msg
}
