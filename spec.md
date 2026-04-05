# rp — Repo Manager CLI Spec

## 1. Overview

`rp` is a Go CLI tool that manages a developer's local workspace of git repositories. It reads a YAML manifest declaring which repos should exist where, then provides commands to bootstrap, sync, report status, and manage dependencies across all repos.

**Design principles:**
- Report state, never mutate silently (no auto-commit, auto-push, auto-stash)
- Shell out to `git` binary for all git operations
- Parallel operations via worker pool with configurable concurrency
- Styled terminal output via lipgloss

## 2. Manifest Format

Location: `~/.config/rp/manifest.yaml` (overridable via `--manifest` global flag or `RP_MANIFEST` env var).

### 2.1 Schema

```yaml
base_dir: ~/Developer   # required, supports ~ expansion

owners:
  <owner_name>:          # e.g. "deligoez", "phonyland"
    flat: true           # optional, default false
    <category>:          # e.g. "projects", "packages", "repos"
      - repo: <github_owner>/<repo_name>
        deps:            # optional, list of shell commands to run
          - composer install
          - npm install
      - repo: <github_owner>/<repo_name>
    archive:             # reserved category — repos under archive/ subdirectory
      - repo: <github_owner>/<repo_name>
```

**Reserved keys** at the owner level: `flat`, `archive`. These cannot be used as category names. The parser distinguishes them as follows:
- `flat` — always a boolean scalar, never a list. If present with a non-boolean value, validation fails.
- `archive` — always a repo list (same structure as any other category), but receives special path treatment (see 2.2). The custom `UnmarshalYAML` explicitly checks for the `archive` key and sets `IsArchive: true` on its entries.
- All other keys under an owner are treated as category names and must contain a non-empty list of repo entries. A YAML null or empty list for a category is a validation error.

**Go type strategy:** Each owner is parsed via a custom `UnmarshalYAML` that:
1. Reads `flat` as a boolean field (if present)
2. Reads `archive` as `[]RepoEntry` (if present), marks entries with `IsArchive: true`
3. Iterates remaining keys as `map[string][]RepoEntry` for categories

To preserve YAML key order for `Manifest.Repos()`, use `yaml.Node` for iterating owner-level keys rather than `map` iteration.

### 2.2 Directory Mapping Rules

`repo_name` is the substring after the `/` in the `repo` field (e.g., `deligoez/tp` -> `tp`).

| Mode | Category | Path |
|------|----------|------|
| Default (flat: false) | any non-archive | `{base_dir}/{owner}/{category}/{repo_name}/` |
| Default (flat: false) | archive | `{base_dir}/{owner}/archive/{repo_name}/` |
| Flat (flat: true) | any non-archive | `{base_dir}/{owner}/{repo_name}/` |
| Flat (flat: true) | archive | `{base_dir}/{owner}/archive/{repo_name}/` |

Examples:
- `deligoez/projects/tp` -> `~/Developer/deligoez/projects/tp/`
- `deligoez/archive/roast` -> `~/Developer/deligoez/archive/roast/`
- `phonyland/cloud` (flat) -> `~/Developer/phonyland/cloud/`
- `phonyland/archive/framework` (flat) -> `~/Developer/phonyland/archive/framework/`

Note: The `github_owner` in the `repo` field does not need to match the manifest `owner_name`. A repo `acme/tool` can live under manifest owner `deligoez`. The manifest owner determines the local directory grouping; the repo field determines the clone URL.

### 2.3 Clone URL

All repos are cloned via SSH: `git@github.com:{repo}.git`

### 2.4 Validation Rules

1. `base_dir` must be present and non-empty
2. Each `repo` field must match pattern `{owner}/{name}` (exactly one `/`). Both parts must be non-empty and contain only alphanumeric characters, hyphens, underscores, and dots.
3. No duplicate `repo` entries across the entire manifest
4. Owner names and category names must be valid directory names: non-empty, no `/`, no `..`, no null bytes
5. At least one owner with at least one repo must exist
6. `flat` and `archive` are reserved and cannot be used as category names. `flat` must be a boolean if present.
7. `deps` values must be non-empty strings
8. Categories must contain a non-empty list of repo entries (no null or empty lists)

## 3. Commands

### 3.0 Output Label Convention

All commands use the same label format for each repo in output lines:
- Categorized owners: `{category}/{repo_name}` (e.g., `projects/tp`)
- Flat owners: `{repo_name}` (e.g., `cloud`)
- Archive (both modes): `archive/{repo_name}` (e.g., `archive/roast`)

Labels are grouped under owner headers. This applies to `bootstrap`, `sync`, `status`, `deps`, and `archive`. Exception: `list` uses a different layout with category sub-headers and bare repo names (see 3.7).

### 3.1 Global Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--manifest` | `-m` | `~/.config/rp/manifest.yaml` | Path to manifest file |
| `--concurrency` | `-c` | `4` | Max parallel operations (must be >= 1) |
| `--no-color` | | `false` | Disable colored output. Also respects `NO_COLOR` env var per no-color.org. |

Environment variables: `RP_MANIFEST` (string path), `RP_CONCURRENCY` (positive integer, invalid values are ignored and default is used).

### 3.2 `rp bootstrap`

Clone every repo that doesn't exist locally to its target path.

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Show what would be cloned without cloning |

**Behavior:**
1. Load and validate manifest
2. For each repo, resolve target path
3. If path exists and is a git repo -> skip, report "already exists"
4. If path exists but is NOT a git repo -> skip, report error
5. If path doesn't exist -> create parent directories (`os.MkdirAll` with 0755), then clone `git@github.com:{repo}.git` to target path
6. Run clones in parallel (bounded by `--concurrency`)
7. `--dry-run` lists what would be cloned and always exits 0

**Output:**
```
Bootstrapping 42 repos (concurrency: 4)...

deligoez
  projects/tp              cloned
  projects/blog            cloned
phonyland
  cloud                    already exists
  archive/framework        FAILED: git clone exit code 128

-- Summary --
40 cloned, 1 already existed, 1 failed
```

**Exit codes:** 0 = all cloned or existed, 2 = any failed (clone failure or non-git-dir at target path)

### 3.3 `rp sync`

Pull all clean repos, skip dirty ones, report status.

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Show what would happen without pulling |

**Behavior per repo (evaluated in order, first match wins):**
1. Not cloned -> clone it (clone only, no deps). Create parent dirs. If clone fails -> report error (contributes exit 2, but other workers continue).
2. Path exists but is NOT a git repo -> report error (contributes exit 2, but other workers continue)
3. Dirty (uncommitted changes) -> skip, report warning with changed file count. Note: a repo that is both dirty and has unpushed commits reports as "dirty" (dirty takes precedence).
4. Has unpushed commits (only checked when `HasUpstream` is true) -> skip, report warning with commit count and branch. When no upstream exists, this check is skipped and the repo proceeds to step 5.
5. Clean -> run `git pull --ff-only`
   - Success with new commits -> report "pulled N commits"
   - Already up to date -> report "up to date"
   - ff-only fails (diverged) -> report warning "diverged" (exit 1)
   - Pull fails for other reasons (network error) -> report warning (exit 1)
   - No upstream and pull fails -> report warning "no upstream" (exit 1). This is the expected outcome for repos with no tracking branch — they are reported as needing attention.
6. `--dry-run` lists what would happen and always exits 0

**"Dirty" definition:** A repo is dirty if `git status --porcelain` produces any output. This includes changed files, staged changes, and untracked files. All of these block a sync pull.

**Pull mechanism:** Before pull, record `HEAD` SHA via `git rev-parse HEAD`. After pull, count commits with `git rev-list <old-HEAD>..HEAD --count`. This avoids locale-dependent stdout parsing.

**Dry-run output:** Same format as normal output but with prefixed actions instead of results:
```
deligoez
  projects/tp              would pull
  projects/blog            would skip (dirty - 3 changed files)
phonyland
  cloud                    would clone
```

**Output:**
```
deligoez
  projects/tp              OK up to date
  projects/blog            OK pulled 1 commit
phonyland
  cloud                    OK pulled 3 commits
tarfin-labs
  backend                  !! 2 unpushed commits (feature/new-api)
  frontend                 !! dirty - 3 changed files

-- Summary --
42 repos synced, 3 need attention
```

The summary count "synced" means total repos processed (including skipped). "Need attention" means repos that were skipped or had warnings.

**Exit codes:** 0 = all synced, 1 = any repo skipped (dirty/unpushed/diverged/pull-failed), 2 = hard error (clone failure, non-git-dir, manifest error). When multiple codes apply, highest wins.

### 3.4 `rp status`

Show the state of every repo in the manifest.

**Per-repo info:**
- Exists on disk (yes/no)
- Clean / dirty (with modified file count)
- Current branch name. On detached HEAD, show short SHA instead (via `git rev-parse --short HEAD`).
- Ahead/behind remote (unpushed/unpulled commit counts). Determined via `git rev-list --left-right --count {branch}...{upstream}`. If no upstream tracking branch exists, show branch name only (no ahead/behind info).

**Output grouped by owner, color-coded:**
- Green `OK` = clean, up to date
- Yellow `!!` = dirty, ahead, behind, or diverged
- Red `XX` = not cloned / error

For categorized owners, repos are shown as `{category}/{repo_name}`. For flat owners, repos are shown as just `{repo_name}`. Archive repos always shown under `archive/` prefix regardless of flat setting.

Status format: `{label} {symbol} {branch} [{details}]`
- Clean: just branch name
- Dirty: `{branch} ~N dirty` (e.g., `main ~3 dirty`)
- Ahead: `{branch} +N ahead` (e.g., `main +2 ahead`)
- Behind: `{branch} -N behind` (e.g., `main -3 behind`)
- Both ahead and behind: `{branch} +1 ahead -3 behind`
- Dirty + ahead: `{branch} ~3 dirty +1 ahead` (all conditions shown)

```
deligoez
  projects/tp              OK main
  projects/blog            !! main +2 ahead
  projects/forum           !! main +1 ahead -3 behind
  projects/cli             !! main ~3 dirty
  packages/laravel-...     OK main
  archive/roast            OK main

phonyland
  cloud                    OK main
  archive/framework        XX not cloned

-- Summary --
39 OK, 2 need attention, 1 not cloned
```

**Exit codes:** 0 = all clean and cloned, 1 = any dirty/missing/ahead/behind

### 3.5 `rp deps [repo_filter]`

Run dependency install commands defined in the manifest for repos.

**Arguments:**
- No args -> all repos in manifest that have `deps` defined
- `rp deps tarfin-labs/backend` -> exact match on `repo` field. If no match found, exit 2 with error message. If match found but repo has no `deps` defined, print message "no deps configured for {repo}" and exit 0.

**Behavior:**
1. For each target repo, check if it exists on disk. If not, skip with warning.
2. Read `deps` list from manifest for that repo
3. Run each command via `sh -c` in the repo directory (supports pipes, env vars, shell features). Working directory is set via `exec.Cmd.Dir`.
4. If a repo has no `deps` defined -> skip silently (in no-filter mode)
5. Commands within a single repo run sequentially; repos run in parallel (bounded by `--concurrency`). A failure in one repo does not stop other repos in the pool.
6. Report results

**Output:**
```
deligoez
  projects/blog            OK composer install
  projects/blog            OK npm install
tarfin-labs
  backend                  OK npm install
  frontend                 FAILED: npm install (exit code 1)

-- Summary --
2 repos succeeded, 1 failed
```

**Exit codes:** 0 = all succeeded, 2 = any command failed or filter not found

### 3.6 `rp archive`

Scan non-archive repos (repos where `IsArchive == false` in manifest) and report those with stale last commits.

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--threshold` | `365` | Days since last commit to consider stale |

**Behavior:**
1. For each non-archive repo that exists on disk (uncloned repos are silently skipped and not counted in summary)
2. Get last commit date via `git log -1 --format=%cI` (committer date, ISO 8601)
3. Compare against current date. A repo is stale if `today - commit_date >= threshold` days (calendar days).
4. Does NOT move anything — report only. Note: moving a repo requires both filesystem move AND manifest edit; the output reminds the user of this.

**Output:**
```
Archive candidates (last commit >= {threshold} days ago):

deligoez
  projects/forum           last commit: 2024-08-15 (547 days ago)
    -> suggestion: move to deligoez/archive/ and update manifest

phonyland
  phony-generator-sequence last commit: 2024-01-20 (806 days ago)
    -> suggestion: move to phonyland/archive/ and update manifest

2 repos could be archived
```

**Exit codes:** 0 always (informational only)

### 3.7 `rp list`

List all repos in manifest.

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--missing` | `false` | Show only repos not cloned locally |

**Output grouped by owner/category:**

Unlike other commands, `list` uses a hierarchical layout with category sub-headers and bare repo names (not the `{category}/{repo_name}` label convention from 3.0). For flat owners, repos are listed at the same indent level as category headers. Archive is always shown as a separate sub-group. With `--missing`, owners/categories with no missing repos are omitted entirely.

```
deligoez
  projects
    tp                   ~/Developer/deligoez/projects/tp          OK
    blog                 ~/Developer/deligoez/projects/blog        OK
  archive
    roast                ~/Developer/deligoez/archive/roast        XX missing

phonyland (flat)
  cloud                  ~/Developer/phonyland/cloud               OK
  archive
    framework            ~/Developer/phonyland/archive/framework   XX missing

-- 42 repos total, 2 missing --
```

**Exit codes:** 0 = all exist, 1 = any missing

### 3.8 `rp manifest init`

Scan a directory tree and generate a manifest. This is a best-effort helper for initial setup — the generated manifest will likely need manual editing.

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `.` | Directory to scan |
| `--output` | `stdout` | Output file path (use `--output ~/.config/rp/manifest.yaml` to write directly) |
| `--dry-run` | `false` | Show discovered repos without generating YAML |

**Behavior:**
1. Walk directory tree up to 4 levels deep from `--dir` (i.e., `--dir` is depth 0, max traversal is depth 4). Skip `.git` internal directories and symlinks.
2. For each git repo found, read remote `origin` URL. Parse both GitHub SSH (`git@github.com:owner/name.git` or `git@github.com:owner/name`) and HTTPS (`https://github.com/owner/name.git` or `https://github.com/owner/name`) formats. Strip `.git` suffix if present.
3. Skip repos with no `origin` remote, non-GitHub remotes, or unparseable URLs. Report skipped repos to stderr.
4. Group by owner (from the remote URL's owner component)
5. Infer flat vs categorized per owner based on the directory path between the owner directory and the repo directory. The "owner directory" is identified by finding an ancestor directory whose name matches the GitHub owner from the remote URL (case-insensitive). If no ancestor matches, the repo's immediate parent directory is treated as the owner directory and the owner is marked as flat.
   - All repos at `{owner}/{repo_name}/` (1 level below owner dir) -> mark as flat
   - All repos at `{owner}/{category}/{repo_name}/` (2 levels below owner dir) -> use intermediate directory name as category
   - Mixed depths within one owner -> use categorized, place depth-1 repos in a `repos` category, and report a warning to stderr
6. If `--output` targets an existing file, abort with error (no silent overwrite). User must delete first.
7. Generate YAML manifest, output to stdout or file

**Dry-run output:** Lists discovered repos, one per line, with their inferred owner and path:
```
Found 42 repos:
  deligoez/tp              ~/Developer/deligoez/projects/tp
  deligoez/blog            ~/Developer/deligoez/projects/blog
  phonyland/cloud          ~/Developer/phonyland/cloud
  (3 repos skipped — no GitHub remote)
```

**Exit codes:** 0 = success, 2 = error

## 4. Internal Packages

### 4.1 `internal/manifest`

- `Load(path string) (*Manifest, error)` — parse YAML via `yaml.Node`, validate, expand `~`
- `Manifest.Repos() []RepoEntry` — flat list of all repos with resolved paths, in manifest order (guaranteed by `yaml.Node` iteration)
- `Manifest.Owners() []OwnerGroup` — ordered list of owners with their repos, preserving manifest order. Used by commands that group output by owner.
- `OwnerGroup` struct: `Name string`, `IsFlat bool`, `Repos []RepoEntry`
- `RepoEntry` struct: `Repo`, `Owner`, `Category`, `LocalPath`, `CloneURL`, `IsArchive`, `IsFlat`, `Deps []string`

Owner YAML parsing uses a custom `UnmarshalYAML` with `yaml.Node`:
1. Reads `flat` as bool (if present)
2. Reads `archive` key explicitly, creates entries with `IsArchive: true`
3. Iterates remaining keys in document order as category -> `[]RepoEntry` mappings

### 4.2 `internal/git`

All functions take a repo path and shell out to `git`:
- `Clone(url, path string) error` — runs `git clone {url} {path}`
- `Pull(path string) (PullResult, error)` — records HEAD via `git rev-parse HEAD` before pull, runs `git pull --ff-only`, counts new commits via `git rev-list <old-HEAD>..HEAD --count`. On error, `PullResult` is zero-valued. Returns typed errors: `ErrDiverged` (ff-only failed), `ErrNoUpstream` (no tracking branch), or a generic error (network/other). Callers use `errors.Is()` to distinguish.
- `Status(path string) (RepoStatus, error)` — dirty file count via `git status --porcelain` (counts all lines: modified, staged, untracked), branch name via `git symbolic-ref --short HEAD` (falls back to `git rev-parse --short HEAD` on detached HEAD), ahead/behind via `git rev-list --left-right --count {branch}...{upstream}`. `HasUpstream: false` when no tracking branch exists; `Ahead` and `Behind` are 0 when `HasUpstream` is false.
- `LastCommitDate(path string) (time.Time, error)`
- `IsRepo(path string) bool` — checks for `.git` directory

**`PullResult`** struct: `NewCommits int`, `AlreadyUpToDate bool`

**`RepoStatus`** struct: `Clean bool`, `DirtyFiles int`, `Branch string`, `Ahead int`, `Behind int`, `HasUpstream bool`

### 4.3 `internal/deps`

- `RunDeps(path string, commands []string) error` — runs each command via `sh -c` with `exec.Cmd.Dir` set to `path`. Stops on first failure, returns error with the failing command name and its stderr.
- No auto-detection; commands come from manifest `deps` field

### 4.4 `internal/ui`

- Styled output helpers using lipgloss
- Status symbols: `OK` (green), `!!` (yellow), `XX` (red)
- Summary line formatting
- Table-like aligned output (repo name column + status column)
- Singular/plural: "1 commit" vs "3 commits", "1 file" vs "3 files" (apply to all count strings)

## 5. Worker Pool

- Used by `bootstrap`, `sync`, `deps`
- Configurable concurrency via `--concurrency` flag (default 4)
- Each worker processes one repo at a time
- **Output buffering:** Each worker buffers its result into a struct. After all workers complete, results are printed in manifest order (using `Manifest.Owners()` for owner ordering). This ensures deterministic, non-interleaved output.
- **Progress:** For long-running operations, a progress line on stderr: `[12/42] processing...` (updated as each worker completes). The verb varies by command: "cloning" for bootstrap, "syncing" for sync, "installing" for deps. This line is overwritten in-place (carriage return) and is not part of stdout. On non-TTY stderr, progress lines are suppressed.
- Errors in one worker don't stop other workers

## 6. Error Handling & Exit Codes

| Code | Meaning | Used by |
|------|---------|---------|
| 0 | All operations succeeded | all commands |
| 1 | Repos need attention (dirty, missing, behind, skipped) | sync, status, list |
| 2 | Hard error (manifest parse, git not found, clone failure, command failure) | all commands |

Per-command exit code details:
- `bootstrap`: 0 or 2 only (no "needs attention" state)
- `sync`: 0 = all pulled/up-to-date, 1 = any skipped (dirty/unpushed/diverged/pull-failed), 2 = clone failure or non-git-dir or manifest error
- `status`: 0 = all clean and cloned, 1 = any dirty/missing/ahead/behind
- `list`: 0 = all exist, 1 = any missing (applies regardless of `--missing` flag)
- `deps`: 0 = all succeeded, 2 = command failed or filter not found
- `archive`: always 0 (informational)
- `manifest init`: 0 or 2
- `--dry-run` always exits 0

When multiple exit codes could apply, the highest wins (2 > 1 > 0).

## 7. Testing Strategy

Section 7 covers unit tests for the core internal packages (`manifest`, `git`, `deps`). Command-level integration tests (bootstrap, sync, status, list, archive, deps command, manifest init) are out of scope for the initial spec — they will be added as the CLI layer is built, testing via `cmd.Execute()` with a temp filesystem.

### 7.1 Manifest Tests (`internal/manifest/manifest_test.go`)

| # | Test | Input | Expected |
|---|------|-------|----------|
| 1 | Parse valid manifest | Full YAML with multiple owners, categories, flat and archive | All fields populated correctly |
| 2 | Categorized path resolution | `deligoez` owner, `projects` category, `tp` repo | `{home}/Developer/deligoez/projects/tp` (tilde expanded) |
| 3 | Flat path resolution | `phonyland` owner (flat), `cloud` repo | `{home}/Developer/phonyland/cloud` (tilde expanded) |
| 4 | Archive path (categorized) | `deligoez` owner, archive, `roast` repo | `{home}/Developer/deligoez/archive/roast` (tilde expanded) |
| 5 | Archive path (flat) | `phonyland` owner (flat), archive, `framework` repo | `{home}/Developer/phonyland/archive/framework` (tilde expanded) |
| 6 | Tilde expansion | `base_dir: ~/Developer` | Expands to home dir |
| 7 | Missing base_dir | YAML without `base_dir` | Validation error |
| 8 | Invalid repo format | `repo: invalid` (no slash) | Validation error |
| 9 | Duplicate repos | Same repo in two categories | Validation error |
| 10 | Empty manifest | YAML with no owners | Validation error |
| 11 | Reserved category name `flat` | Category named `flat` | Validation error |
| 12 | Archive entries have IsArchive | Owner with archive containing repos | All archive entries have `IsArchive: true` |
| 13 | Repo with deps | `repo: x/y` with `deps: [npm install]` | `Deps: ["npm install"]` |
| 14 | Repo without deps | `repo: x/y` without deps field | `Deps: nil` (empty) |
| 15 | Cross-owner repo | `repo: acme/tool` under owner `deligoez` | `CloneURL: git@github.com:acme/tool.git`, path under `deligoez/` |
| 16 | Empty category list | Category with `[]` or null | Validation error |
| 17 | Manifest order preserved | Multiple owners and categories | `Repos()` returns in YAML document order |
| 18 | `flat` with non-boolean value | `flat: "yes"` | Validation error |
| 19 | Empty string in deps | `deps: [""]` | Validation error |

### 7.2 Git Tests (`internal/git/git_test.go`)

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | IsRepo on git dir | `git init` in temp dir | `true` |
| 2 | IsRepo on non-git dir | Plain temp dir | `false` |
| 3 | Status clean | Init + commit in temp dir | `Clean: true, Branch: "main"` |
| 4 | Status dirty | Init + commit + modify file | `Clean: false, DirtyFiles: 1` |
| 5 | Clone | Clone a local bare repo | Repo exists at target |
| 6 | LastCommitDate | Init + commit | Returns recent time |
| 7 | Status no upstream | Init + commit, no remote | `HasUpstream: false, Ahead: 0, Behind: 0`, no error |
| 8 | Pull ff-only success | Clone, add commit to origin, pull | `NewCommits: 1, AlreadyUpToDate: false` |
| 9 | Pull already up to date | Clone, pull without new commits | `NewCommits: 0, AlreadyUpToDate: true` |
| 10 | Pull diverged | Clone, add commits to both origin and local | Returns error, `PullResult` is zero-valued |
| 11 | Status ahead | Clone, commit locally without push | `Ahead: 1, Behind: 0, HasUpstream: true` |
| 12 | Status behind | Clone, add commit to origin (not pulled), fetch | `Ahead: 0, Behind: 1, HasUpstream: true` |
| 13 | Status detached HEAD | `git checkout --detach` | `Branch` contains short SHA, no error |

### 7.3 Deps Tests (`internal/deps/deps_test.go`)

| # | Test | Setup | Expected |
|---|------|-------|----------|
| 1 | Run single command | Temp dir, `["echo hello"]` | Success, command executed |
| 2 | Run multiple commands | Temp dir, `["echo a", "echo b"]` | Both run in order |
| 3 | Empty command list | Temp dir, `[]` | No-op, no error |
| 4 | Failing command | Temp dir, `["false"]` | Returns error containing command name |
| 5 | Commands run in repo dir | Temp dir, `["pwd"]` | Output contains repo path |
| 6 | Shell features work | Temp dir, `["echo $HOME | cat"]` | Success, no error (validates sh -c) |
| 7 | First failure stops execution | Temp dir, `["false", "echo after"]` | Returns error, second command not executed |

## 8. Config File Precedence

1. CLI flags (highest priority)
2. Environment variables: `RP_MANIFEST` (string path), `RP_CONCURRENCY` (positive integer; invalid/non-numeric values are ignored, default used), `NO_COLOR` (any value disables color, per no-color.org)
3. Defaults (lowest priority)
