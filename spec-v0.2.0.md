# rp v0.2.0 — Quality of Life Improvements

## 1. Overview

This spec adds 5 quality-of-life improvements discovered during hands-on QA testing. Each addresses a real friction point observed when using rp on a workspace with 12+ repos across different git states.

## 2. `deps --dry-run`

### 2.1 Problem

`rp up --dry-run` skips the deps phase entirely — there's no way to preview which dep commands would run. The user has to either read the manifest or run `deps` for real.

### 2.2 Solution

Add `--dry-run` flag to `rp deps`.

**Behavior:**
1. Load manifest, resolve targets (same as normal deps)
2. For each target repo with deps defined:
   - Check if repo exists on disk (skip with warning if not)
   - List commands that would run, but don't execute them
3. Always exit 0

**Human output:**
```
deligoez
  projects/blog            would run: composer install
  projects/blog            would run: npm install
junegunn
  fzf                      would run: go mod download
  fzf                      would run: echo "fzf deps done"

-- Summary --
2 repos, 4 commands
```

**JSON output:**
```json
{
  "command": "deps",
  "exit_code": 0,
  "dry_run": true,
  "summary": {
    "repos": 2,
    "commands": 4,
    "skipped": 0
  },
  "repos": [
    {
      "repo": "deligoez/blog",
      "status": "ok",
      "commands": [
        {"command": "composer install", "status": "would_run"},
        {"command": "npm install", "status": "would_run"}
      ]
    }
  ]
}
```

Skipped repos (not on disk) use `{"status": "skipped", "reason": "not_on_disk"}` same as normal mode.

### 2.3 Impact on `rp up --dry-run`

With deps --dry-run available, `rp up --dry-run` now includes the deps preview as a third phase instead of omitting it. The `deps` sub-object uses the dry-run schema above.

## 3. Clean Sync Error Messages

### 3.1 Problem

When sync fails on a repo, the raw git stderr is included in the output. This produces multi-line noise:

```
!! pull failed: git pull --ff-only: exit status 1
You are not currently on a branch.
Please specify which branch you want to merge with.
See git-pull(1) for details.

    git pull <remote> <branch>
```

### 3.2 Solution

Truncate sync error messages to a single descriptive line. Parse known git error patterns and replace with clean summaries.

**Error classification:**

| Git stderr pattern | Clean message |
|-------------------|---------------|
| "not currently on a branch" | `pull failed (detached HEAD)` |
| "does not have a commit checked out" or rev-parse HEAD fails | `pull failed (empty repo)` |
| "diverged" or "not possible to fast-forward" | `diverged` (already handled) |
| "no tracking information" | `no upstream` (already handled) |
| Any other git error | `pull failed` (single line, no stderr dump) |

**Human output after fix:**
```
archive/net               !! pull failed (detached HEAD)
empty-repo                !! pull failed (empty repo)
```

**JSON output:** The `error` field in failed sync repos also uses the clean single-line message. No raw git stderr.

### 3.3 Implementation

In `processSyncRepo`, when `git.Pull` returns a generic error (not `ErrDiverged` or `ErrNoUpstream`), classify it:

```go
default:
    msg := classifySyncError(pullErr)
    return syncResult{..., status: ui.SymbolWarn() + " " + msg, ...}
```

`classifySyncError` checks the error string for known patterns and returns a one-liner. The `errMsg` field on `syncResult` (used in JSON) gets the same clean message.

## 4. `rp check` — Boolean Exit Code Command

### 4.1 Problem

An agent or script that just wants to know "is everything OK?" has to parse `status --json --compact` output. For a simple boolean check in shell scripts or CI, this is overhead.

### 4.2 Solution

Add `rp check` command. Zero output, only exit code.

**Behavior:**
1. Load manifest
2. For each repo: check exists on disk and git status (clean, not ahead/behind)
3. Exit 0 if all repos are clean and cloned
4. Exit 1 if any repo is dirty, missing, ahead, or behind

**No output to stdout.** No progress on stderr. No JSON mode (exit code IS the output).

**Usage:**
```bash
rp check && echo "all good" || echo "needs attention"
rp check --filter deligoez/ && echo "deligoez repos OK"
```

**Flags:**
- `--filter` works as usual (only check filtered repos)

### 4.3 Implementation

Reuse the same `git.Status` logic from the status command. Just don't print anything — compute exit code and return.

## 5. `rp diff` — Recent Changes Across Repos

### 5.1 Problem

After a sync, the user wants to know "what changed across my repos?" There's no quick way to see recent activity.

### 5.2 Solution

Add `rp diff` command. Shows the most recent commit for each repo that has new commits since the last known state.

**Behavior:**
1. Load manifest
2. For each cloned repo, run `git log --oneline -1`
3. Show repos grouped by owner with their latest commit

**Human output:**
```
deligoez
  projects/tp              a1b2c3d feat: add new feature
  projects/blog            d4e5f6a fix: typo in header

phonyland
  cloud                    (no new commits)

-- 4 repos, 2 with recent commits --
```

**JSON output:**
```json
{
  "command": "diff",
  "exit_code": 0,
  "summary": {
    "total": 4,
    "with_commits": 2
  },
  "repos": [
    {
      "repo": "deligoez/tp",
      "last_commit_sha": "a1b2c3d",
      "last_commit_message": "feat: add new feature",
      "last_commit_date": "2026-04-05T10:30:00Z"
    }
  ]
}
```

**Flags:**
- `--filter` — filter repos
- `--since <duration>` — only show commits newer than duration (e.g., `--since 7d`, `--since 24h`). Default: show all (no time filter).

### 5.3 Duration Parsing

`--since` accepts:
- `Nd` — N days (e.g., `7d`, `30d`)
- `Nh` — N hours (e.g., `24h`, `48h`)

Implementation: parse the suffix, compute cutoff time, filter repos where `last_commit_date < cutoff`.

### 5.4 git Operations

For each repo:
```bash
git -C {path} log -1 --format="%h %s"        # sha + message
git -C {path} log -1 --format=%cI             # date (reuse LastCommitDate)
```

## 6. Remaining Validation Hints

### 6.1 Problem

During QA, 5 out of 9 manifest validation errors were missing actionable hints. Only `base_dir missing`, `invalid repo format`, `duplicate repo`, `manifest not found`, and `YAML parse error` had hints.

### 6.2 Solution

Add hints to the remaining validation errors:

| Error | Hint |
|-------|------|
| No owners defined | `add at least one owner with repos to manifest` |
| `flat` as non-boolean | `flat must be true or false, e.g. flat: true` |
| `flat` used as category name | `"flat" is reserved — rename the category` |
| Empty deps string | `deps entries must be non-empty command strings` |
| Empty category list | `add at least one repo to category, or remove it` |

### 6.3 Implementation

Wrap each validation error in `manifest.go` with `output.NewHintError`.

## 7. Testing Strategy

### 7.1 deps --dry-run Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Dry-run lists commands | Manifest with deps | "would run" per command, exit 0 |
| 2 | Dry-run skips missing repos | Repo not on disk | `status: "skipped"` in JSON |
| 3 | Dry-run JSON schema | `--dry-run --json` | `dry_run: true`, `status: "would_run"` per command |
| 4 | Up dry-run now includes deps | `rp up --dry-run` | Three sections (not two) |

### 7.2 Clean Error Message Tests

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | Detached HEAD sync | `git checkout --detach` | `pull failed (detached HEAD)` |
| 2 | Empty repo sync | `git init` (no commits) | `pull failed (empty repo)` |
| 3 | Generic pull error | Unreachable remote | `pull failed` (no stderr dump) |
| 4 | JSON error field clean | Detached HEAD + `--json` | `error` field is single line |

### 7.3 check Command Tests

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | All clean | All repos cloned and clean | Exit 0, no stdout |
| 2 | Dirty repo | One modified file | Exit 1, no stdout |
| 3 | Missing repo | One not cloned | Exit 1, no stdout |
| 4 | With filter | `--filter owner/` on clean subset | Exit 0 |

### 7.4 diff Command Tests

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | Basic diff | Repos with commits | Shows latest commit per repo |
| 2 | JSON output | `--json` | Valid JSON with sha/message/date |
| 3 | Since filter | `--since 0d` | All repos (0 days = everything) |
| 4 | Since filter excludes | `--since 99999d` | No repos (all too recent) |
| 5 | Filter combined | `--filter owner/ --since 7d` | Only matching repos |

### 7.5 Hint Coverage Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | No owners hint | Manifest with no owners | Error contains hint text |
| 2 | Flat non-bool hint | `flat: 42` | Error contains hint text |
| 3 | Empty category hint | `projects: []` | Error contains hint text |
| 4 | Empty deps hint | `deps: [""]` | Error contains hint text |
| 5 | All errors have hints in JSON | Each validation error via `--json` | All have `hint` field |
