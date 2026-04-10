# rp v0.4.0 — Discover Untracked Repos

## 1. Overview

Adds `rp discover` command that compares the user's GitHub repos against the manifest to surface repos not yet tracked. Uses the `gh` CLI for GitHub API access.

**Backwards compatibility:** Purely additive — new command, no changes to existing behavior.

**Post-implementation:** Update CLAUDE.md to include `discover` in Commands, Per-Command Flags, Command Behavior, and Project Structure sections.

## 2. `rp discover` — Find Untracked GitHub Repos

### 2.1 Problem

The manifest tracks a subset of the user's GitHub repos. Over time, new repos are created or the user joins new orgs, but the manifest isn't updated. There's no way to know what's missing.

### 2.2 Solution

Add `rp discover` command. Lists GitHub repos not present in the manifest.

**Behavior:**
1. Verify `gh` CLI is available and authenticated (exit 2 with hint if not)
2. Load manifest to get the set of tracked repos (exit 2 with hint if manifest fails)
3. Fetch the authenticated user's login via `gh api user --jq '.login'`
4. Fetch the user's GitHub orgs via `gh api user/orgs --paginate --jq '.[].login'`
5. For each owner (personal account + all orgs), list repos via `gh repo list {owner} --json nameWithOwner,isFork,isArchived --limit 1000`
6. Deduplicate by `nameWithOwner` (case-insensitive), keeping the first occurrence (personal account is scanned first)
7. Filter out forks (unless `--forks`) and archived repos (unless `--archived`). Each filter is applied independently — a repo that is both forked and archived requires **both** `--forks` and `--archived` to be included
8. Subtract manifest repos (case-insensitive match on the full `owner/name` string via `strings.EqualFold`). Note: case-insensitive matching is intentional because GitHub normalizes repository casing, which may differ from what users write in the manifest.
9. Apply `--filter` to narrow the result set. Since untracked repos are `ghRepo` (not `RepoEntry`), discover implements its own filter matching on `nameWithOwner`: owner prefix match (`owner/`) and exact repo match (`owner/name`), both case-insensitive.
10. Display untracked repos grouped by owner

**Execution order:** Steps 1-2 are validation (fail fast). Steps 3-4 must complete before step 5 begins (to determine total owner count for progress). Step 5 scans owners **sequentially** to avoid `gh` rate limiting. Steps 6-10 are post-processing.

**Error handling:** Any `gh` command failure (network error, timeout, rate limit, malformed JSON response) results in exit 2 with the `gh` stderr as the error message. Partial results are **not** emitted — the command either succeeds fully or fails.

**Exit codes:**
- 0 = no untracked repos found after filtering (everything is tracked)
- 1 = untracked repos found (attention needed)
- 2 = hard error (`gh` not found, not authenticated, API failure, manifest load failure)

Exit codes reflect the **filtered** result: if `--filter` narrows results to zero while untracked repos exist elsewhere, exit 0.

### 2.3 Human Output

The personal account (identified by matching the login from `gh api user`) gets a `(personal)` suffix. Org owners are displayed without a suffix.

```
github-user (personal)
  github-user/side-project
  github-user/dotfiles

acme
  acme/internal-tool
  acme/legacy-api

-- 4 untracked repos across 2 owners --
```

When no untracked repos:
```
-- all repos tracked --
```

### 2.4 JSON Output

```json
{
  "command": "discover",
  "exit_code": 1,
  "summary": {
    "untracked": 4,
    "owners_scanned": 3,
    "total_remote": 25,
    "total_manifest": 12
  },
  "repos": [
    {
      "repo": "github-user/side-project",
      "owner": "github-user",
      "fork": false,
      "archived": false
    }
  ]
}
```

**Field definitions:**
- `total_remote`: total unique repos fetched from GitHub across all owners **after** deduplication but **before** fork/archived/manifest filtering
- `total_manifest`: total repos in the full manifest (unfiltered — reflects how many repos were used for subtraction in step 8)
- `untracked`: count of repos in the `repos` array (after all filtering)
- `owners_scanned`: number of GitHub owners queried (personal + orgs)
- `fork` / `archived`: metadata about the untracked repo (these fields reflect the repo's GitHub state; when `--forks` is false, fork=true repos are absent from the array entirely)

When no untracked repos: `exit_code` is 0, `repos` is an empty array `[]`.

**Compact mode (`--compact`):** Omits the `repos` array, summary only.

### 2.5 Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--forks` | `false` | Include forked repos in results (boolean, presence toggles to true) |
| `--archived` | `false` | Include archived repos in results (boolean, presence toggles to true) |
| `--filter` | — | Filter output repos (standard `owner/` or `owner/repo` pattern) |
| `--json` | `false` | Structured JSON output |
| `--compact` | `false` | Summary only (with `--json`) |

`--filter` applies to the **output** side: all orgs are always scanned, but only untracked repos matching the filter are displayed. This is consistent with other commands where `--filter` narrows the repo list. If no repos match the filter, output is empty with exit code 0.

`--concurrency` has no effect on `discover` since owner scanning is sequential.

### 2.6 `gh` CLI Dependency

`rp discover` requires the `gh` CLI to be installed and authenticated.

**Detection:** Run `gh auth status` before any API calls. This checks both installation and authentication in one call.

If `gh` is not found (exec error):
- Exit 2
- Error: `gh CLI not found`
- Hint: `install gh from https://cli.github.com and run 'gh auth login'`

If `gh auth status` fails (not authenticated):
- Exit 2
- Error: `gh is not authenticated`
- Hint: `run 'gh auth login' to authenticate`

### 2.7 `gh` Commands Used

```bash
# Check auth (combines install + auth check)
gh auth status

# Get authenticated user login
gh api user --jq '.login'

# Get user's orgs (paginated to handle >30 orgs)
gh api user/orgs --paginate --jq '.[].login'

# List repos for an owner (org or user)
gh repo list {owner} --json nameWithOwner,isFork,isArchived --limit 1000
```

All commands are executed via `os/exec` with stdout/stderr capture, consistent with how `internal/git` shells out to the `git` binary.

**JSON parse failures** from `gh` commands result in exit 2 with an error message including the raw output for debugging.

### 2.8 Progress

Steps 3-4 (fetching user login and orgs) complete first to determine the total owner count. Then scanning proceeds sequentially:

```
[1/3] scanning github-user...
[2/3] scanning acme...
[3/3] scanning opensource...
```

Progress is on stderr (TTY only), suppressed in JSON mode (same as other commands).

### 2.9 Implementation

**New file:** `cmd/discover.go`

No new internal packages. The `gh` shell-outs live in the command file since they're specific to this command.

**Key functions:**

```go
// Command handler
func runDiscover(cmd *cobra.Command, args []string) error

// gh helpers (unexported, in cmd/discover.go)
func ghAuthCheck() error                         // gh auth status
func ghCurrentUser() (string, error)             // gh api user
func ghListOrgs() ([]string, error)              // gh api user/orgs
func ghListRepos(owner string) ([]ghRepo, error) // gh repo list

// Core logic (pure, testable, unexported — consistent with other cmd helpers)
func filterUntracked(remote []ghRepo, manifestRepos []string, forks, archived bool) []ghRepo

// Filter logic (custom, operates on nameWithOwner strings)
func matchesDiscoverFilter(nameWithOwner string, filters []string) bool
```

**`ghRepo` struct:**
```go
type ghRepo struct {
    NameWithOwner string `json:"nameWithOwner"`
    IsFork        bool   `json:"isFork"`
    IsArchived    bool   `json:"isArchived"`
}
```

**Scanning order:** Personal account first, then orgs in alphabetical order.

**Deduplication:** Defensive dedup by case-insensitive `nameWithOwner`. First occurrence wins. In practice, `gh repo list {owner}` scopes to that owner, so cross-owner duplicates are unlikely, but dedup handles edge cases (e.g., transferred repos) safely.

**`filterUntracked` logic:**
1. Deduplicate remote repos by lowercase `nameWithOwner`
2. If `forks=false`, exclude repos where `isFork=true`
3. If `archived=false`, exclude repos where `isArchived=true`
4. Build a set from `manifestRepos` (lowercased)
5. Return remote repos whose lowercase `nameWithOwner` is not in the manifest set

### 2.10 Limitations

- **Scope:** Discovery covers the authenticated user's personal account and orgs where the user is a **member**. Repos where the user is an outside collaborator in other orgs are not discovered.
- **1000-repo cap:** `gh repo list --limit 1000` caps results per owner. Owners with more than 1000 repos may have incomplete results. This is sufficient for most users; large enterprise orgs may see truncation.

## 3. Testing Strategy

### 3.1 Unit-Testable Logic

The `filterUntracked` function is pure (no I/O) and testable directly in `cmd/discover_test.go`:

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Basic subtraction | Remote has 5 repos, manifest has 3 | 2 untracked |
| 2 | All tracked | Remote = manifest | 0 untracked |
| 3 | Fork exclusion | Remote has forks, `forks=false` | Forks excluded |
| 4 | Fork inclusion | Remote has forks, `forks=true` | Forks included |
| 5 | Archived exclusion | Remote has archived, `archived=false` | Archived excluded |
| 6 | Archived inclusion | Remote has archived, `archived=true` | Archived included |
| 7 | Case insensitive match | Remote `Acme/Repo`, manifest `acme/repo` | Not shown as untracked |
| 8 | Deduplication | Same repo appears twice in remote | Shown once |
| 9 | Fork AND archived | Repo is fork+archived, only `--forks` set | Excluded (both flags needed) |
| 10 | Empty manifest | Remote has 3 repos, manifest empty | 3 untracked |

### 3.2 Integration Tests (require `gh`)

Tests in `cmd/json_test.go` using the subprocess pattern. Tests skip with `t.Skip("gh CLI not available")` if `gh` is not installed or not authenticated.

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | gh not found | `PATH` set to `/usr/bin` only (no `gh`) | Exit 2, error `gh CLI not found`, hint present |
| 2 | JSON schema | `--json` with real `gh` | Valid JSON: `command`=`"discover"`, `exit_code` is 0 or 1, `summary` has all 4 keys with int values, `repos` is array (structure only, no specific counts) |
| 3 | Compact mode | `--compact --json` | No `repos` key in output |
| 4 | Filter output | `--filter nonexistent/` | Empty results, exit 0 (no repos match filter) |
| 5 | Exit code 1 | Real `gh`, manifest with single dummy repo (`nobody/nonexistent`) | Exit code 1 (real GitHub repos are untracked). Skip if user has zero GitHub repos. |
