# rp AX (Agent Experience) Improvements Spec

## 1. Overview

This spec adds AX (Agent Experience) capabilities to rp, making it a first-class tool for AI agents alongside human users. The changes are additive — existing human-readable output remains the default. Pipe auto-detection is NOT used; JSON mode is always explicit via `--json` flag to avoid breaking existing shell pipelines.

**Core principle:** An agent should be able to discover workspace state, make decisions, and execute actions in minimal round-trips with minimal token overhead.

## 2. JSON Output Mode

### 2.1 Global `--json` Flag

Add `--json` persistent flag to root command. When set:
- All commands output structured JSON to stdout
- Human-readable text is suppressed
- Progress indicators on stderr are suppressed
- Color is suppressed (`ui.SetNoColor(true)` is called implicitly)
- `os.Exit()` is never called directly; commands return the exit code through the JSON envelope and the runner exits after printing

Environment variable: `RP_JSON=1` enables JSON mode. CLI flag `--json` takes precedence.

Precedence: `--json` flag > `RP_JSON` env > default (human).

### 2.2 JSON Output Contract

Every command outputs a single JSON object to stdout. The object always contains:

```json
{
  "command": "status",
  "exit_code": 0,
  "summary": { ... },
  "repos": [ ... ]
}
```

- `command`: string, the subcommand name
- `exit_code`: int, what the process will exit with (0, 1, or 2). Follows the same per-command rules defined in the original spec section 6.
- `summary`: object, command-specific summary counts (always present, never null)
- `repos`: array, per-repo results (always present as `[]` when empty, never null, never omitted — except in compact mode and error responses)
- `dry_run`: bool, present and `true` only when `--dry-run` is active

**Exception:** The composite command `rp up` uses a different envelope with per-phase sub-objects instead of top-level `summary`/`repos`. See section 5.3.

**Error responses** in JSON mode:

```json
{
  "command": "bootstrap",
  "exit_code": 2,
  "error": "manifest parse error: base_dir must be present and non-empty",
  "hint": "add base_dir to ~/.config/rp/manifest.yaml"
}
```

Error responses have `error` and `hint` fields. `summary` and `repos` are omitted (not present in the JSON at all).

### 2.3 Per-Command JSON Schemas

#### `rp status --json`

```json
{
  "command": "status",
  "exit_code": 0,
  "summary": {
    "ok": 39,
    "attention": 2,
    "not_cloned": 1,
    "total": 42
  },
  "repos": [
    {
      "repo": "deligoez/tp",
      "owner": "deligoez",
      "category": "projects",
      "local_path": "/Users/x/Developer/deligoez/projects/tp",
      "cloned": true,
      "branch": "main",
      "clean": true,
      "dirty_files": 0,
      "ahead": 0,
      "behind": 0,
      "has_upstream": true
    },
    {
      "repo": "phonyland/framework",
      "owner": "phonyland",
      "category": "archive",
      "local_path": "/Users/x/Developer/phonyland/archive/framework",
      "cloned": false
    }
  ]
}
```

When `cloned` is `false`, the fields `branch`, `clean`, `dirty_files`, `ahead`, `behind`, `has_upstream` are omitted (not present).

#### `rp bootstrap --json`

```json
{
  "command": "bootstrap",
  "exit_code": 2,
  "summary": {
    "cloned": 40,
    "already_existed": 1,
    "failed": 1,
    "total": 42
  },
  "repos": [
    {
      "repo": "deligoez/tp",
      "action": "cloned",
      "local_path": "/Users/x/Developer/deligoez/projects/tp"
    },
    {
      "repo": "phonyland/cloud",
      "action": "already_exists",
      "local_path": "/Users/x/Developer/phonyland/cloud"
    },
    {
      "repo": "deligoez/forum",
      "action": "failed",
      "error": "git clone exit code 128",
      "local_path": "/Users/x/Developer/deligoez/experiments/forum"
    }
  ]
}
```

`action` values: `"cloned"`, `"already_exists"`, `"failed"`. Note: `exit_code` is 2 because `failed > 0` (per original spec).

#### `rp sync --json`

```json
{
  "command": "sync",
  "exit_code": 1,
  "summary": {
    "pulled": 5,
    "up_to_date": 34,
    "cloned": 0,
    "skipped": 3,
    "failed": 0,
    "total": 42
  },
  "repos": [
    {
      "repo": "phonyland/cloud",
      "action": "up_to_date"
    },
    {
      "repo": "TestFlowLabs/doctest",
      "action": "pulled",
      "new_commits": 3
    },
    {
      "repo": "tarfin-labs/backend",
      "action": "skipped",
      "reason": "unpushed",
      "ahead": 2,
      "branch": "feature/new-api"
    },
    {
      "repo": "tarfin-labs/frontend",
      "action": "skipped",
      "reason": "dirty",
      "dirty_files": 3
    },
    {
      "repo": "deligoez/forum",
      "action": "skipped",
      "reason": "diverged"
    },
    {
      "repo": "deligoez/experiment",
      "action": "skipped",
      "reason": "no_upstream"
    }
  ]
}
```

`action` values: `"pulled"`, `"up_to_date"`, `"cloned"`, `"skipped"`, `"failed"`.
`reason` values (only when action is `"skipped"`): `"dirty"`, `"unpushed"`, `"diverged"`, `"no_upstream"`.
Extra fields per reason: `dirty` -> `dirty_files`, `unpushed` -> `ahead` + `branch`, `pulled` -> `new_commits`.

Summary breaks down into granular categories instead of ambiguous "synced" count.

#### `rp deps --json`

```json
{
  "command": "deps",
  "exit_code": 2,
  "summary": {
    "succeeded": 2,
    "failed": 1,
    "skipped": 0,
    "total": 3
  },
  "repos": [
    {
      "repo": "deligoez/blog",
      "status": "ok",
      "commands": [
        {"command": "composer install", "status": "ok"},
        {"command": "npm install", "status": "ok"}
      ]
    },
    {
      "repo": "tarfin-labs/frontend",
      "status": "failed",
      "commands": [
        {"command": "npm install", "status": "failed", "error": "exit code 1"}
      ]
    }
  ]
}
```

When a repo is not on disk: `{"repo": "x/y", "status": "skipped", "reason": "not_on_disk"}`.

#### `rp archive --json`

```json
{
  "command": "archive",
  "exit_code": 0,
  "summary": {
    "candidates": 2,
    "threshold_days": 365
  },
  "repos": [
    {
      "repo": "deligoez/forum",
      "owner": "deligoez",
      "category": "projects",
      "last_commit": "2024-08-15T00:00:00Z",
      "days_ago": 547,
      "suggestion": "move to deligoez/archive/ and update manifest"
    }
  ]
}
```

`last_commit` uses RFC 3339 format (consistent with git internals). `suggestion` field included for agent actionability.

#### `rp list --json`

```json
{
  "command": "list",
  "exit_code": 0,
  "summary": {
    "total": 42,
    "missing": 2
  },
  "repos": [
    {
      "repo": "deligoez/tp",
      "owner": "deligoez",
      "category": "projects",
      "local_path": "/Users/x/Developer/deligoez/projects/tp",
      "exists": true,
      "is_archive": false,
      "is_flat": false
    }
  ]
}
```

#### `rp manifest init --json`

```json
{
  "command": "manifest_init",
  "exit_code": 0,
  "summary": {
    "discovered": 42,
    "skipped": 3
  },
  "repos": [
    {
      "repo": "deligoez/tp",
      "local_path": "/Users/x/Developer/deligoez/projects/tp",
      "inferred_owner": "deligoez",
      "inferred_layout": "categorized",
      "inferred_category": "projects"
    }
  ]
}
```

The `manifest_yaml` field is NOT included in JSON output. The generated YAML is written to `--output` file or can be obtained by running without `--json`. This avoids embedding YAML inside JSON and keeps token count low.

**Dry-run** in JSON mode: same structure but `"dry_run": true` at top level. Bootstrap repos have `"action": "would_clone"` / `"action": "would_skip"`. Sync repos have similar `"would_pull"` / `"would_skip"` actions. Exit code is always 0 for dry-run.

## 3. Structured Errors with Hints

### 3.1 Error Format

All errors include an actionable `hint` field. Hints are best-effort suggestions, not contractual exact strings. The hint should be contextually useful, not a fixed template.

| Error | Example Hint |
|-------|-------------|
| Manifest file not found | `create manifest with: rp manifest init --dir ~/Developer` |
| base_dir missing | `add base_dir to manifest: base_dir: ~/Developer` |
| Invalid repo format | `repo must be owner/name, e.g. deligoez/tp` |
| Duplicate repo | `remove duplicate entry for {repo} in manifest` |
| Clone failed | `check SSH keys: ssh -T git@github.com` |
| Non-git dir at target | `remove or rename {path} before cloning` |
| Repo not found (deps filter) | `check repo name, available: rp list --json` |
| Output file exists (manifest init) | `delete {path} first, or use stdout` |

### 3.2 Implementation

Define a `HintError` type in the output package:

```go
type HintError struct {
    Err  error
    Hint string
}

func (e *HintError) Error() string { return e.Err.Error() }
func (e *HintError) Unwrap() error { return e.Err }
```

Commands return `&HintError{...}` from `RunE`. The root command's error handler checks for `HintError` and formats accordingly (JSON or human mode).

### 3.3 Human Mode Errors

```
error: manifest parse error: base_dir must be present and non-empty
hint:  add base_dir to manifest: base_dir: ~/Developer
```

## 4. Repo Filtering

### 4.1 `--filter` Flag

Add `--filter` persistent flag (string slice) to root command so it's available to all subcommands.

Filter syntax:
- `--filter deligoez/tp` — exact match on `repo` field (contains `/`, so it's a repo match)
- `--filter deligoez/` — owner prefix match (trailing `/` means "all repos under this owner")
- `--filter deligoez` — also treated as owner prefix (equivalent to `deligoez/`). Any value without `/` or with trailing `/` is an owner prefix.
- Multiple filters: `--filter deligoez/ --filter phonyland/cloud` — union of all matches

Matching rule: a filter containing exactly one `/` and non-empty parts on both sides is an exact repo match. Everything else is an owner prefix match.

Filtering happens before dispatch to the worker pool. Summary counts and exit codes reflect the filtered set only. Progress denominator uses filtered count.

When `--filter` produces zero matches (no repos match any filter), the command outputs an empty repos array and exits 0.

### 4.2 `--filter` on `deps` Command

The existing positional `[repo]` argument on `deps` is kept for backwards compatibility. When both `--filter` and a positional argument are provided, the positional argument takes precedence and `--filter` is ignored with a stderr warning.

### 4.3 Status Convenience Filters

Add to `status` only:
- `--dirty` — show only dirty repos
- `--behind` — show only repos behind remote
- `--ahead` — show only repos with unpushed commits

These are post-filters: the command processes all repos but only displays (and counts in summary) those matching. Combinable with AND logic: `--dirty --ahead` shows repos that are BOTH dirty AND ahead.

## 5. Composite Command: `rp up`

### 5.1 Behavior

`rp up` runs bootstrap + sync + deps in sequence:

1. **Bootstrap phase:** Clone missing repos (same logic as `rp bootstrap`)
2. **Sync phase:** Pull clean repos (same logic as `rp sync`). Repos cloned in phase 1 are treated as "up to date" (just cloned, nothing to pull).
3. **Deps phase:** Run deps for all repos that have deps defined (same logic as `rp deps` with no filter)

**Phase gating:** All three phases always run. A failure in one phase does not abort subsequent phases. The exit code is the highest across all three phases (2 > 1 > 0).

### 5.2 Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Preview bootstrap and sync phases. Deps phase is skipped entirely in dry-run (deps has no preview capability). |
| `--no-deps` | `false` | Skip dependency installation phase entirely |
| `--filter` | | Repo filter (same as 4.1), applied to all three phases |

### 5.3 Output

In human mode, three sections:

```
== Bootstrap ==
deligoez
  projects/tp              cloned
  ...

== Sync ==
deligoez
  projects/blog            OK pulled 1 commit
  ...

== Deps ==
deligoez
  projects/blog            OK composer install
  ...

-- Summary --
40 cloned, 2 already existed, 0 failed
38 pulled, 2 up to date, 2 skipped
3 deps succeeded, 0 failed
```

In JSON mode:

```json
{
  "command": "up",
  "exit_code": 0,
  "dry_run": false,
  "bootstrap": {
    "summary": {"cloned": 40, "already_existed": 2, "failed": 0, "total": 42},
    "repos": [...]
  },
  "sync": {
    "summary": {"pulled": 5, "up_to_date": 35, "cloned": 0, "skipped": 2, "failed": 0, "total": 42},
    "repos": [...]
  },
  "deps": {
    "summary": {"succeeded": 3, "failed": 0, "skipped": 0, "total": 3},
    "repos": [...]
  }
}
```

Each sub-object uses the same schema as its standalone command. `deps` is omitted (not present) when `--no-deps` is used or when in `--dry-run` mode.

### 5.4 Exit Codes

- `--dry-run` always exits 0
- Otherwise: highest exit code across all phases (2 > 1 > 0)
- Phase-level exit codes follow the same rules as standalone commands

## 6. Compact Output Mode

### 6.1 `--compact` Flag

Add `--compact` persistent flag. Only meaningful with `--json`; ignored without it.

In compact mode, `repos` is omitted entirely from the JSON output. Only `command`, `exit_code`, `summary`, and `dry_run` are present:

```json
{
  "command": "status",
  "exit_code": 1,
  "summary": {
    "ok": 39,
    "attention": 2,
    "not_cloned": 1,
    "total": 42
  }
}
```

For `rp up --json --compact`, the sub-objects contain only `summary` (no `repos`):

```json
{
  "command": "up",
  "exit_code": 0,
  "bootstrap": {"summary": {...}},
  "sync": {"summary": {...}},
  "deps": {"summary": {...}}
}
```

## 7. Exit Code Handling in JSON Mode

### 7.1 No Direct os.Exit()

In JSON mode, commands must NOT call `os.Exit()` directly. Instead:

1. Commands build their result struct with the computed `exit_code`
2. Commands return the result to a JSON printer
3. The JSON printer writes to stdout, then calls `os.Exit(result.ExitCode)`

This ensures JSON is always written before the process exits.

### 7.2 Implementation Pattern

Each command's `RunE` function checks `cmd.JSONOutput`:
- If false: existing human output path (unchanged)
- If true: build result struct, call `output.PrintAndExit(result)` which prints JSON and exits

The `output.PrintAndExit` function:
```go
func PrintAndExit(r Result) {
    json.NewEncoder(os.Stdout).Encode(r)
    os.Exit(r.ExitCode)
}
```

## 8. Implementation Notes

### 8.1 Output Package

Create `internal/output/output.go`:

```go
package output

// SuccessResult is used when the command succeeds (with or without warnings).
// Summary and Repos are always present — use make([]T, 0) for empty repos, never nil.
type SuccessResult struct {
    Command  string      `json:"command"`
    ExitCode int         `json:"exit_code"`
    DryRun   bool        `json:"dry_run,omitempty"`
    Summary  interface{} `json:"summary"`
    Repos    interface{} `json:"repos"`
}

// ErrorResult is used when the command fails with a hard error.
// Summary and Repos are never present.
type ErrorResult struct {
    Command  string `json:"command"`
    ExitCode int    `json:"exit_code"`
    Error    string `json:"error"`
    Hint     string `json:"hint,omitempty"`
}

// UpResult is for the composite rp up command (section 5.3).
// Uses per-phase sub-objects instead of top-level summary/repos.
type UpResult struct {
    Command   string     `json:"command"`
    ExitCode  int        `json:"exit_code"`
    DryRun    bool       `json:"dry_run,omitempty"`
    Bootstrap *SubResult `json:"bootstrap,omitempty"`
    Sync      *SubResult `json:"sync,omitempty"`
    Deps      *SubResult `json:"deps,omitempty"`
}

type SubResult struct {
    Summary interface{} `json:"summary"`
    Repos   interface{} `json:"repos,omitempty"`
}

type HintError struct {
    Err  error
    Hint string
}
```

Using separate `SuccessResult` and `ErrorResult` types ensures the JSON contract is enforced at compile time: success always has `summary`/`repos`, errors never do.

### 8.2 TTY Detection for Testing

The `output.IsJSON()` function checks:
1. `--json` flag (highest priority)
2. `RP_JSON` env var
3. Default: false

No auto-pipe detection. This makes testing straightforward — tests explicitly pass `--json` when testing JSON output.

### 8.3 Backwards Compatibility

All changes are strictly additive:
- Default behavior (no flags) is identical to current implementation
- `--json`, `--compact`, `--filter` are opt-in flags
- `rp up` is a new command
- Existing `deps [repo]` positional argument is unchanged
- No auto-detection magic that could break existing scripts

### 8.4 New Package

```
internal/
  output/
    output.go     # Result types, HintError, PrintAndExit, IsJSON
```

The `internal/ui` package remains unchanged and handles human-mode output. Commands check `output.IsJSON()` to decide which path to take.

## 9. Testing Strategy

### 9.1 Output Package Tests (`internal/output/output_test.go`)

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | SuccessResult serialization | SuccessResult with summary and empty repos | Valid JSON, `repos` is `[]` not null |
| 2 | SuccessResult non-empty repos | SuccessResult with repo entries | `repos` array populated |
| 3 | ErrorResult serialization | ErrorResult with error and hint | No `summary` or `repos` keys in JSON |
| 4 | ErrorResult no hint | ErrorResult without hint | `hint` key omitted |
| 5 | Compact serialization | SuccessResult rendered in compact mode | `repos` key omitted |
| 6 | HintError wrapping | HintError with inner error | `Error()` returns inner message, `Unwrap()` works |
| 7 | UpResult serialization | UpResult with 3 sub-results | All 3 sections present |
| 8 | UpResult with omitted deps | UpResult with nil Deps | `deps` key omitted |

### 9.2 Filter Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Exact repo filter | `--filter deligoez/tp` | Only that repo in output |
| 2 | Owner prefix with slash | `--filter deligoez/` | All deligoez repos |
| 3 | Owner prefix without slash | `--filter deligoez` | All deligoez repos (same as trailing /) |
| 4 | Multiple filters | `--filter deligoez/ --filter phonyland/cloud` | Union of matches |
| 5 | No matches | `--filter nonexistent/repo` | Empty repos array, exit 0 |
| 6 | Filter + JSON | `--filter deligoez/ --json` | Filtered JSON output |
| 7 | Filter on deps with positional | `--filter x/ repo` | Positional wins, stderr warning |

### 9.3 JSON Integration Tests (per command)

| # | Test | Command | Assertion |
|---|------|---------|-----------|
| 1 | Status JSON | `status --json` | Valid JSON, `exit_code` matches process exit |
| 2 | Status JSON not cloned | `status --json` with missing repo | `cloned: false`, no branch/clean fields |
| 3 | Bootstrap JSON | `bootstrap --json` | `action` field per repo |
| 4 | Sync JSON skipped | `sync --json` with dirty repo | `action: "skipped"`, `reason: "dirty"`, `dirty_files` present |
| 5 | Deps JSON skipped | `deps --json` with missing repo | `status: "skipped"`, `reason: "not_on_disk"` |
| 6 | Archive JSON date | `archive --json` | `last_commit` is RFC 3339 |
| 7 | Error JSON | `status --json` with bad manifest | `error` and `hint` present, no `summary`/`repos` |
| 8 | Compact JSON | `status --json --compact` | No `repos` field in output |
| 9 | Dry-run JSON | `bootstrap --json --dry-run` | `dry_run: true`, `action: "would_clone"` |

### 9.4 Up Command Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Up all phases | Manifest with missing + dirty + deps | Three sections in output |
| 2 | Up dry-run | `--dry-run` | Bootstrap + sync previewed, deps omitted, exit 0 |
| 3 | Up no-deps | `--no-deps` | Deps section omitted |
| 4 | Up JSON | `--json` | Single JSON with bootstrap/sync/deps |
| 5 | Up exit code | Bootstrap has failure | Exit 2 (highest wins) |
| 6 | Up phase continuation | Sync has dirty repos | Deps still runs, exit 1 |

### 9.5 Hint Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Missing manifest | Invalid path | Error contains hint text (not exact string match) |
| 2 | JSON error with hint | `--json` + invalid manifest | JSON has both `error` and `hint` fields |
| 3 | Human error with hint | Invalid manifest | stderr shows `error:` and `hint:` lines |
