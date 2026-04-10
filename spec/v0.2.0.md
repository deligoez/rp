# rp v0.2.0 — Quality of Life Improvements

## 1. Overview

This spec adds 5 quality-of-life improvements discovered during hands-on QA testing. Each addresses a real friction point observed when using rp on a workspace with 12+ repos across different git states.

**Backwards compatibility:** All changes are additive. The only behavioral change is that `rp up --dry-run` now includes a deps preview (previously omitted). Agents checking for absence of the `deps` key in `rp up --dry-run --json` must be updated.

## 2. `deps --dry-run`

### 2.1 Problem

`rp up --dry-run` skips the deps phase entirely — there's no way to preview which dep commands would run.

### 2.2 Solution

Add `--dry-run` flag to `rp deps`.

**Behavior:**
1. Load manifest, resolve targets (same filtering as normal: positional arg, `--filter`)
2. For each target repo with deps defined:
   - Check if repo exists on disk (skip with warning if not)
   - List commands that would run, but don't execute them
3. Always exit 0

**Human output:**
```
acme
  services/api             would run: go mod download
vendor
  payments                 would run: composer install
  payments                 would run: npm install

-- Summary --
2 repos, 3 commands
```

**JSON output:** Uses the same `SuccessResult` envelope. Summary uses `repos` and `commands` counts for dry-run (different from normal mode's `succeeded`/`failed`/`skipped`/`total`). Per-repo commands have `"status": "would_run"`:

```json
{
  "command": "deps",
  "exit_code": 0,
  "dry_run": true,
  "summary": {
    "repos": 2,
    "commands": 3,
    "skipped": 0
  },
  "repos": [
    {
      "repo": "acme/api",
      "status": "ok",
      "commands": [
        {"command": "go mod download", "status": "would_run"}
      ]
    }
  ]
}
```

Skipped repos (not on disk) use `{"status": "skipped", "reason": "not_on_disk"}` same as normal mode. Positional `[repo]` arg with no deps + dry-run: prints "no deps configured" and exits 0 (same as normal mode).

### 2.3 Impact on `rp up --dry-run`

With deps `--dry-run` available, `rp up --dry-run` now includes the deps preview as a third phase instead of omitting it.

**Breaking change from spec-ax.md:** spec-ax.md §5.3 stated `deps` is omitted from `rp up --dry-run --json`. This is now changed — `deps` sub-object is present with the dry-run deps schema. The `UpResult.Deps` uses the same `*SubResult` type; the per-repo/per-command `"status": "would_run"` values are in the `Repos` array within the sub-result. The top-level `dry_run: true` on `UpResult` signals dry-run mode. Agents parsing `rp up --dry-run --json` must handle the new `deps` key.

In human mode, the `== Deps ==` section header appears with "would run" entries.

## 3. Clean Sync Error Messages

### 3.1 Problem

When sync fails on a repo, the raw git stderr is included in the output, producing multi-line noise.

### 3.2 Solution

Classify known git errors and display clean single-line messages. Preserve raw stderr for unclassified errors behind `--verbose`.

**Error classification in `processSyncRepo`:**

| Detection point | Condition | Clean message |
|----------------|-----------|---------------|
| `git.Pull` returns `ErrDiverged` | (already handled) | `diverged` |
| `git.Pull` returns `ErrNoUpstream` | (already handled) | `no upstream` |
| `git.Pull` error contains "not currently on a branch" | detached HEAD | `pull failed (detached HEAD)` |
| `git.Pull` error OR pre-pull `rev-parse HEAD` fails with exit 128 | empty repo | `pull failed (empty repo)` |
| Any other `git.Pull` error | generic | `pull failed` |

**Implementation:** Add a `classifySyncError(err error) string` function in `cmd/sync.go`. It checks `err.Error()` for known substrings. The function is called for the `default:` case in the pull error switch.

For empty repos: `git.Pull` currently fails at `git rev-parse HEAD` (exit 128) before attempting pull. The error message contains "exit status 128". Detect this pattern and classify as "empty repo".

**Verbose mode:** The existing `--verbose` flag (removed in spec v0.1) is NOT re-added. The clean messages are sufficient — users who need raw stderr can run `git pull` manually on the problematic repo. The JSON `error` field contains the clean message.

### 3.3 Human output after fix

```
archive/net               !! pull failed (detached HEAD)
empty-repo                !! pull failed (empty repo)
broken-remote             !! pull failed
```

## 4. `rp check` — Boolean Exit Code Command

### 4.1 Problem

An agent or script that just needs "is everything OK?" has to parse status output.

### 4.2 Solution

Add `rp check` command. Zero output, only exit code.

**Behavior:**
1. Load manifest
2. For each repo: check exists on disk and git status (clean, not ahead/behind when upstream exists)
3. Exit 0 if all repos are clean and cloned
4. Exit 1 if any repo is dirty, missing, ahead, or behind
5. Exit 2 if manifest cannot be loaded (hard error)

**No output** to stdout. No progress on stderr. The `--json` flag and `RP_JSON` env var produce no effect on this command (no JSON envelope — exit code IS the output).

**Error output:** On exit 2 (manifest load failure), the standard error handler writes `error:` and `hint:` lines to stderr. This is the only case where stderr output is produced. Exit 0 and exit 1 produce zero output on both stdout and stderr.

**Implementation:** The `check` command's `RunE` handles manifest loading itself and calls `os.Exit` directly, bypassing the root error handler for exit 0/1 cases. For exit 2, it falls through to the root handler which writes to stderr.

**Flags:**
- `--filter` — works as usual (only check filtered repos)

**Edge case:** Repos with no upstream tracking branch (`HasUpstream=false`) are considered OK as long as they are clean. This matches the behavior of `rp status` where no-upstream repos show just the branch name without warnings.

### 4.3 Usage

```bash
rp check && echo "all good" || echo "needs attention"
rp check --filter acme/ && echo "acme repos OK"
```

## 5. `rp diff` — Recent Activity Across Repos

### 5.1 Problem

After a sync, the user wants to know "what's the latest commit in each repo?" There's no quick way to see recent activity.

### 5.2 Solution

Add `rp diff` command. Shows the most recent commit for each cloned repo.

**Behavior:**
1. Load manifest
2. For each cloned repo, run `git -C {path} log -1 --format=%h|%s|%cI` (single subprocess call per repo)
3. Show repos grouped by owner with their latest commit
4. Repos not cloned are skipped silently

This is purely local — no `git fetch`. It shows what's on disk right now.

**Human output:**
```
acme
  services/api               a1b2c3d feat: add new feature (2 days ago)
  services/web               d4e5f6a fix: typo in header (5 days ago)

opensource
  design-system              f7g8h9i chore: bump deps (30 days ago)

-- 3 repos shown --
```

**JSON output:**
```json
{
  "command": "diff",
  "exit_code": 0,
  "summary": {
    "total": 3,
    "shown": 3
  },
  "repos": [
    {
      "repo": "acme/api",
      "sha": "a1b2c3d",
      "message": "feat: add new feature",
      "date": "2026-04-03T10:30:00Z",
      "days_ago": 2
    }
  ]
}
```

**Flags:**
- `--filter` — filter repos
- `--since <duration>` — only show repos whose last commit is newer than duration. Repos with older commits are excluded from output and from summary counts.

### 5.3 Duration Parsing

`--since` accepts:
- `Nd` — N days (e.g., `7d`, `30d`)
- `Nh` — N hours (e.g., `24h`, `48h`)

Any other format is a parse error (exit 2). `N` must be a non-negative integer.

**Filter semantics:** A repo is shown if `last_commit_date >= (now - duration)`. The cutoff is computed as `time.Now().Add(-duration)`. This means:
- `--since 7d` = show repos with commits in the last 7 days
- `--since 0d` = cutoff is `now`, so only future-dated commits would match — effectively empty. To show all repos regardless of date, omit `--since`.

Without `--since`, all cloned repos are shown.

### 5.4 Git Operations

Single call per repo:
```bash
git -C {path} log -1 --format=%h|%s|%cI
```

Parse by splitting on `|`. If the repo has no commits (empty repo), skip it silently.

## 6. Remaining Validation Hints

### 6.1 Problem

5 out of 9 manifest validation errors are missing actionable hints.

### 6.2 Solution

Add `output.NewHintError` wrapping to these errors:

| Error location | Error | Hint |
|---------------|-------|------|
| `validate()` | No owners defined | `add at least one owner with repos to manifest` |
| `validate()` | Empty deps string | `deps entries must be non-empty command strings` |
| `parseOwnerNode()` | `flat` as non-boolean | `flat must be true or false, e.g. flat: true` |
| `parseOwnerNode()` | Empty category list | `add at least one repo to category, or remove it` |

Note: "flat used as category name" is caught by `parseOwnerNode()` when `flat` has a sequence value — the error message already says "flat must be a boolean" which is descriptive enough. No separate hint needed for this case.

### 6.3 Implementation

Wrap errors in both `validate()` and `parseOwnerNode()` — not just `validate()`. Remove the duplicate empty-category check in `validate()` (it's dead code since `parseOwnerNode` catches it first).

## 7. Testing Strategy

### 7.1 deps --dry-run Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Dry-run lists commands | Manifest with deps | "would run" per command, exit 0 |
| 2 | Dry-run skips missing repos | Repo not on disk | `status: "skipped"` in JSON |
| 3 | Dry-run JSON schema | `--dry-run --json` | `dry_run: true`, `status: "would_run"` per command |
| 4 | Up dry-run includes deps | `rp up --dry-run --json` | `deps` key present (was previously omitted) |
| 5 | Dry-run with positional no-deps | `rp deps --dry-run repo` where repo has no deps | "no deps configured" message, exit 0 |

### 7.2 Clean Error Message Tests

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | Detached HEAD sync | `git checkout --detach` in test repo | Status contains `pull failed (detached HEAD)` |
| 2 | Empty repo sync | `git init` (no commits) in test repo | Status contains `pull failed (empty repo)` |
| 3 | Generic pull error | `git remote set-url origin file:///nonexistent` | Status contains `pull failed` (no raw stderr) |
| 4 | JSON error field clean | Detached HEAD + `--json` | `error` field is single line, no newlines |

### 7.3 check Command Tests

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | All clean | All repos cloned and clean | Exit 0, stdout empty (0 bytes), stderr empty (0 bytes) |
| 2 | Dirty repo | One modified file | Exit 1, stdout empty, stderr empty |
| 3 | Missing repo | One not cloned | Exit 1, stdout empty, stderr empty |
| 4 | With filter | `--filter owner/` on clean subset | Exit 0, stdout empty, stderr empty |
| 5 | Bad manifest | Invalid manifest path | Exit 2, stdout empty, stderr has error+hint |
| 6 | No upstream clean | Repo with no tracking branch, clean | Exit 0 (no-upstream is OK when clean) |

### 7.4 diff Command Tests

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | Basic diff | Repos with commits | Shows sha, message, date per repo |
| 2 | JSON output | `--json` | Valid JSON with sha/message/date/days_ago per repo |
| 3 | Since filter includes | `--since 9999d` | All repos shown (cutoff far in past) |
| 4 | Since filter excludes | Create repo, backdate commit to 2020, `--since 1d` | That repo excluded |
| 5 | Filter combined | `--filter owner/ --since 7d` | Only matching repos |
| 6 | Empty repo skipped | Repo with no commits | Silently excluded from output |
| 7 | Invalid since format | `--since 1w` | Exit 2, error message |

### 7.5 Hint Coverage Tests

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | No owners hint | Manifest with no owners, `--json` | JSON `hint` field non-empty |
| 2 | Flat non-bool hint | `flat: 42`, `--json` | JSON `hint` field non-empty |
| 3 | Empty category hint | `projects: []`, `--json` | JSON `hint` field non-empty |
| 4 | Empty deps hint | `deps: [""]`, `--json` | JSON `hint` field non-empty |
| 5 | 8 of 9 errors have hints | Each validation error via `--json` | 8 have non-empty `hint` field; flat-as-sequence error has descriptive message without separate hint |
