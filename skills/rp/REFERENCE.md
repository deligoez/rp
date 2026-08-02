# rp — Reference

Command surface, JSON schemas, enums, and error hints. For workflows and judgment, see [SKILL.md](SKILL.md).

## Command surface

| Command | Positional | Own flags | `--filter` | Writes anything? |
|---------|-----------|-----------|:---:|---|
| `rp bootstrap` | — | `--dry-run` | yes | clones missing repos |
| `rp sync` | — | `--dry-run` | yes | `git pull --ff-only` on clean repos |
| `rp status` | — | `--dirty` `--ahead` `--behind` | yes | no |
| `rp install` | `[repo]` | `--dry-run` | yes | runs manifest shell commands |
| `rp update` | `[repo]` | `--dry-run` | yes | runs manifest shell commands |
| `rp list` | — | `--missing` | yes | no |
| `rp manifest init` | — | `--dir` `--output` `--dry-run` | no | writes the manifest file |
| `rp up` | — | `--dry-run` `--no-install` `--no-update` | yes | all of the above |
| `rp check` | — | — | yes | no |
| `rp diff` | — | `--since <Nd\|Nh>` | yes | no |
| `rp discover` | — | `--forks` `--archived` | yes (own syntax) | no |
| `rp validate` | `[path]` | — | **no** | no |

## Global flags

| Flag | Short | Default | Env | Notes |
|------|-------|---------|-----|-------|
| `--manifest` | `-m` | `~/.config/rp/manifest.yaml` | `RP_MANIFEST` | `~/` expanded at startup |
| `--concurrency` | `-c` | `4` | `RP_CONCURRENCY` | must be ≥ 1; an invalid env value is silently ignored, an invalid flag is exit 2 |
| `--json` | | `false` | `RP_JSON` (any non-empty value) | forces `--no-color` on |
| `--compact` | | `false` | | drops the `repos` key from JSON |
| `--no-color` | | `false` | `NO_COLOR` | |
| `--filter` | | | | repeatable; union of matches |

Precedence for every one of these: **CLI flag > env var > default**.

## Result envelopes

### SuccessResult — every command except `up`

```json
{
  "command": "status",
  "exit_code": 0,
  "dry_run": true,
  "summary": { },
  "repos": [ ]
}
```

`dry_run` is omitted when false. `summary` and `repos` are always present (`repos` is `[]`, never `null`) — **except under `--compact`, where the `repos` key is removed from the object entirely.**

### ErrorResult — hard errors

```json
{
  "command": "status",
  "exit_code": 2,
  "error": "reading manifest: open /bad/path: no such file",
  "hint": "create manifest with: rp manifest init --dir ~/Developer"
}
```

No `summary`, no `repos`. `hint` is present only when the error was wrapped in a `HintError` (see the hint table below). Human mode prints the same two lines to **stderr** as `error: …` / `hint:  …`.

`command` carries the failing subcommand's own name on every command (v0.9.0; `status`, `list`, `bootstrap`, `diff`, and `check` previously reported `"rp"`). A **global-flag** error — rejected before any subcommand runs, e.g. `-c 0` — is still stamped `"rp"`, which is accurate: no subcommand was reached.

### UpResult — `rp up` only

```json
{
  "command": "up",
  "exit_code": 1,
  "dry_run": false,
  "bootstrap": { "summary": {}, "repos": [] },
  "sync":      { "summary": {}, "repos": [] },
  "install":   { "summary": {}, "repos": [] },
  "update":    { "summary": {}, "repos": [] }
}
```

No top-level `summary`/`repos`. All four keys are present in JSON mode; **`install` / `update` serialize as `null` when skipped via `--no-install` / `--no-update`** (not as a zeroed object — null-check before reading). Under `--compact` each sub-object keeps `summary` and drops `repos`. `exit_code` is the highest across phases.

## Per-command JSON

### `status`

`summary`: `ok`, `attention`, `not_cloned`, `total`.

`repos[]`:

| Field | Type | Present |
|-------|------|---------|
| `repo` | string | always (`owner/name`) |
| `owner` | string | always |
| `category` | string | always (`""` for flat owners) |
| `local_path` | string | always |
| `cloned` | bool | always |
| `branch` | string | when cloned |
| `clean` | bool | when cloned |
| `dirty_files` | int | when cloned |
| `ahead` | int | when cloned |
| `behind` | int | when cloned |
| `has_upstream` | bool | when cloned |

The last five are `omitempty` **pointers** — `omitempty` drops a nil pointer, not a pointer to zero, so a cloned repo reports `"ahead": 0` / `"clean": false` / `"has_upstream": false` explicitly. **A missing key means "not cloned", never "zero".** Branch on `cloned` first; `branch` is a plain string and is `""`-omitted.

Attention rule: `!clean || (has_upstream && (ahead > 0 || behind > 0))`. Exit 1 when `attention > 0 || not_cloned > 0`.

`--dirty` / `--ahead` / `--behind` filter which repos are printed; they do not change `summary` or the exit code.

### `sync`

`summary`: `pulled`, `up_to_date`, `cloned`, `skipped`, `failed`, `total`.

`repos[]`: `repo`, `action`, plus `new_commits`, `reason`, `dirty_files`, `ahead`, `branch`, `error` (all `omitempty`).

`action` ∈ `pulled` · `up_to_date` · `cloned` · `skipped` · `failed` · `would_pull` · `would_skip` · `would_clone`

`reason` (on `skipped` / `would_skip`) ∈ `dirty` · `unpushed` · `diverged` · `no_upstream` · `not_a_repo`

Extra fields by reason: `dirty` → `dirty_files`; `unpushed` → `ahead` + `branch`.

Per-repo evaluation order (first match wins): not cloned → clone · not a git repo → error · dirty → skip · unpushed → skip · clean → `git pull --ff-only`. **Dirty takes precedence over unpushed**, so a repo that is both reports `dirty`.

Exit code is the highest per-repo code; `--dry-run` always exits 0.

Under `--dry-run` the counters carry the *would-be* outcome (v0.9.0): `would_pull` → `pulled`, `would_skip` → `skipped`, `would_clone` → `cloned` — same convention as `bootstrap --dry-run`, so `sync --dry-run --compact` is a usable preview. `repos[].action` still carries the `would_*` name and `repos[].reason` the skip cause.

### `bootstrap`

`summary`: `cloned`, `already_existed`, `failed`, `total`.

`repos[]`: `repo`, `action`, `local_path`, `error` (omitempty).

`action` ∈ `cloned` · `already_exists` · `failed` · `would_clone` · `would_skip`

**Dry-run reuses the same summary keys**: `cloned` = would clone, `already_existed` = would skip, `failed` = 0. `dry_run: true` is the discriminator.

Clone URL is always SSH: `git@github.com:{owner}/{name}.git`. Exit 2 when `failed > 0`; bootstrap never exits 1.

### `install` / `update`

`summary` (normal): `succeeded`, `failed`, `skipped`, `total`, `commands`.
`summary` (`--dry-run`): `repos`, `commands`, `skipped`.

`repos[]`: `repo`, `status`, `reason` (omitempty), `commands[]` (omitempty).
`commands[]`: `{command, status, error?}`.

Repo `status` ∈ `ok` · `failed` · `skipped`. Repo `reason`: `not_on_disk`.
Command `status` ∈ `ok` · `failed` · `would_run`.

Commands run through `sh -c` with the repo's `local_path` as the working directory, in manifest order. Exit 0 clean, 1 when anything was skipped, 2 when any command failed.

Within a repo, **the first failing command aborts that repo's remaining commands**; other repos still run. Command **stdout is discarded**; stderr is captured and surfaces only in the failing command's `error` field. So a command whose useful output goes to stdout tells you nothing here — run it yourself if you need to read it.

Repo selection: a positional `[repo]` wins over `--filter` (with a stderr warning if both are given); an unknown positional is exit 2 + hint; a known repo with no commands prints "no install commands configured for …" and exits 0. Without a positional, the set is *repos that define commands*, then `--filter` applied.

### `list`

`summary`: `total`, `missing`. `repos[]`: `repo`, `owner`, `category`, `local_path`, `exists`.

Exit 1 when `missing > 0`. `--missing` restricts the output to uncloned repos.

### `diff`

`summary`: `total` (all repos considered), `shown` (after `--since`).

`repos[]`: `repo`, `sha`, `message`, `date` (RFC3339, UTC), `days_ago`.

`--since` accepts `Nd` (days) or `Nh` (hours). Always exits 0.

### `discover`

`summary`: `untracked`, `owners_scanned`, `total_remote`, `total_manifest`.

`repos[]`: `repo` (`owner/name`), `owner`, `fork`, `archived`.

Requires the `gh` CLI, authenticated. Scans the personal account plus every org the user is a member of. Forks and archived repos are excluded unless `--forks` / `--archived`. Exit 1 when `untracked > 0`.

`discover` accepts the same three filter forms as every other command (v0.9.0 — a bare `acme` previously matched nothing). The one remaining difference: matching here is **case-insensitive**, because GitHub owner and repo names are.

### `validate`

`summary`: `valid` (always `true` — a failure is an `ErrorResult`), `repos`, `owners`, `categories`, `install_commands`, `update_commands`. `repos` is always `[]`.

`categories` counts distinct categories across categorized owners only. `install_commands`/`update_commands` count individual command strings, not repos.

Positional `[path]` overrides `-m`/`RP_MANIFEST`. **Ignores `--filter`.** Exit 0 valid, 2 on any parse or validation error.

### `manifest init`

`command` is `"manifest_init"`. `summary`: `discovered`, `skipped`.

`repos[]`: `repo`, `local_path`, `inferred_owner`, `inferred_layout`, `inferred_category`.

Scans `--dir` (default `.`), finds git repos with GitHub remotes, and infers flat (depth-1) vs categorized (depth-2) per owner. `--output` defaults to `stdout`; a real path that already exists is exit 2 + hint (no overwrite). `init` knows nothing about `install:`/`update:` — regenerating over a hand-maintained manifest loses them.

### `check`

No output on exit 0 or 1, in any mode including `--json`. A manifest error (exit 2) does print an `ErrorResult` — that is the only output `check` ever produces. Exit 0 all clean and cloned · 1 anything uncloned, unreadable, or needing attention · 2 manifest error.

## Error hints

Every one of these arrives as `{error, hint}` in JSON and `error:` / `hint:` on stderr in human mode. The hint is the action.

| Error | Hint |
|-------|------|
| `reading manifest: …` | `create manifest with: rp manifest init --dir ~/Developer` |
| `parsing manifest YAML: …` | `check manifest syntax at <path>` |
| `manifest is empty` | `run rp manifest init to generate one` |
| `manifest must be a YAML mapping at the top level` | `ensure manifest starts with key: value pairs, not a list` |
| `manifest: base_dir must be present and non-empty` | `add base_dir to manifest: base_dir: ~/Developer` |
| `manifest: at least one owner with at least one repo is required` | `add at least one owner with repos to manifest` |
| `manifest: repo "x" does not match required pattern …` | `repo must be owner/name, e.g. deligoez/tp` |
| `manifest: duplicate repo "x"` | `remove duplicate entry for x in manifest` |
| `manifest: repo "x" has empty install/update entry at index N` | `install entries must be non-empty command strings` |
| `owner "x" has an empty repo list` | `add at least one repo entry, or remove the owner` |
| `category "x" has an empty repo list` | `add at least one repo to category, or remove it` |
| `owner "x" must be a mapping (categorized) or sequence (flat)` | `use a mapping for categorized owners or a sequence for flat owners` |
| `"owners" is no longer a valid manifest key` | `Remove the "owners:" line and dedent owner blocks by one level.` |
| `repo "x" uses removed key "deps"` | `Rename "deps:" to "install:" (and optionally add "update:").` |
| `repo "x" not found in manifest` (install/update) | `check repo name, available: rp list --json` |
| `output file "x" already exists; delete it first` | `delete x first, or use stdout` |
| `gh CLI not found` | `install gh from https://cli.github.com and run 'gh auth login'` |
| `gh is not authenticated` | `run 'gh auth login' to authenticate` |

## Manifest schema

```yaml
base_dir: ~/Developer

<owner>:                       # mapping → categorized
  <category>:
    - repo: <owner>/<name>
      install: [<sh command>, …]
      update:  [<sh command>, …]

<owner>:                       # sequence → flat
  - repo: <owner>/<name>
```

| Layout | Node type under owner | Local path |
|--------|----------------------|------------|
| Categorized | mapping | `{base_dir}/{owner}/{category}/{name}/` |
| Flat | sequence | `{base_dir}/{owner}/{name}/` |

Parsed via `yaml.Node` so key order is preserved — all output is in manifest order regardless of concurrency.

### Data structures

- **`RepoEntry`** — `Repo`, `Owner`, `Category` (`""` when flat), `LocalPath`, `CloneURL`, `Install []string`, `Update []string`
- **`OwnerGroup`** — `Name`, `IsFlat` (derived from the YAML node kind), `Repos []RepoEntry`
- **`Manifest`** — `BaseDir`, private owner list reached via `Repos()` (flat list) and `Owners()` (grouped)

### Filter semantics (`manifest.FilterRepos`)

```
"owner/name"  → repo.Repo == "owner/name"      (exact)
"owner/"      → repo.Owner == "owner"          (exact owner, not a prefix)
"owner"       → repo.Owner == "owner"          (same as above)
```

Case-sensitive, no globs, no substrings, no category matching. Multiple filters union. An empty filter list means "all repos". A no-match filter yields an empty list and exit 0 — not an error. `rp discover` applies the same three forms case-insensitively.

## Internals worth knowing

- **All git operations shell out to the `git` binary** — no Go git library. `git pull --ff-only` is the only mutation; there is no merge, rebase, or force path anywhere in the codebase.
- **Worker pool** (`internal/worker`) runs `--concurrency` operations in parallel. `PoolWithProgress` re-orders results into manifest order before printing; `PoolWithLiveLog` (v0.8.0) emits each result as it completes.
- **Human progress output differs by command.** `bootstrap` / `sync` / `install` / `update` stream `[n/m] <label> <outcome>` lines to **stdout in completion order**, always (not TTY-gated) — they are the human result, and the old owner-grouped listing is gone. `rp up` kept the overwriting `[n/m] verb…` bar on **stderr**, TTY-gated, so it is empty when piped.
- **`--json` is unchanged by all of this**: one JSON document on stdout, `repos` in manifest order, nothing on stderr.
- **`os.Exit()` is only used on the human path**; JSON mode always goes through `output.PrintAndExit`, which encodes the body *and then* exits with `exit_code`.
- **`--json` implies `--no-color`.**

## Environment variables

| Variable | Effect |
|----------|--------|
| `RP_MANIFEST` | Manifest path |
| `RP_CONCURRENCY` | Worker count; must parse as an integer ≥ 1, otherwise silently ignored |
| `RP_JSON` | Any non-empty value enables JSON output |
| `NO_COLOR` | Disables color ([no-color.org](https://no-color.org)) |
