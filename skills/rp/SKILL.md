---
name: rp
description: Workspace-level git repo manager driven by a YAML manifest. Clones, pulls, reports status, and runs install/update commands across every repo in a developer's workspace, with structured JSON on every command. Use when a task spans more than one repository, when you need a repo's path on disk, before or after cross-repo work, when adopting untracked GitHub repos, or when the user mentions rp, their manifest, their Developer folder, or "all my repos".
---

# rp — Repo Manager Skill

`rp` operates on a **workspace** — the whole set of repos declared in a manifest — not on the repo you happen to be standing in. One command answers a question about 80 repos that would otherwise cost 80 `git -C` calls.

## Activation

Use rp when:

- A task spans **more than one repository** (cross-repo search, refactor, dependency bump, release sweep).
- You need a repo's **path on disk** and it is not the current directory.
- You are **about to** touch several repos and need to know which are dirty first.
- The user asks to **clone / pull / refresh / bootstrap** their workspace, a new machine, or "everything".
- The user wants to know **what changed lately** across repos, or which GitHub repos are **not yet tracked**.
- The user mentions the manifest, `~/.config/rp/manifest.yaml`, `~/Developer`, "my repos", "all projects".

Do **not** use rp for single-repo work — inspecting one repo's history, staging, committing (use `git`, or the `hc` skill for commits). rp has no per-file, per-commit, or per-branch operations.

## Mental model

- **The manifest is the declared truth; the disk is the observation.** Every command compares one to the other.
- **rp reconciles in exactly one direction: remote → local.** It clones and it fast-forward-pulls. It never commits, pushes, stashes, resets, merges, rebases, force-pulls, or deletes anything. A repo that would need any of those is *reported and skipped* — dirty state is the human's call, never yours.
- **The manifest is only written by `rp manifest init`.** Every other command reads it. To add a repo you edit the YAML (or ask the user to), then `rp validate`.
- **Every command speaks JSON.** `--json` (or `RP_JSON=1`) turns any command into a data source with a stable schema. Never parse the human output.
- **Human output is a live log, not a report.** Since v0.8.0 `bootstrap`, `sync`, `install`, and `update` stream one `[n/m] …` line per repo to **stdout as each repo finishes** — so the order is completion order, not manifest order, and slow repos land last. Only `rp up` still uses the old overwriting progress bar on stderr (TTY-gated, invisible in a pipe). `--json` is unaffected and stays in manifest order: it is the only output you should ever parse.

## Token economics — the drill-down ladder

rp is built so an agent can answer a question at the cheapest tier and stop. Climb only when you need to:

1. **`--json --compact`** — summary counts only. Answers "is anything wrong?" for any number of repos in ~10 lines.
2. **`--filter`** — narrow to one owner or one repo, then read per-repo detail.
3. **Full `--json`** — every repo's detail. Reserve this for when you genuinely need all of it.

```bash
rp status --json --compact                    # 86 repos → 6 lines
rp status --json --filter acme/               # only what you care about
rp status --json --dirty                      # only the problems, all owners
```

> **`--compact` removes the `repos` key entirely** — it is absent, not `[]`. Code that reads `repos` must not use `--compact`.

Answering "is `acme/api` dirty?" with a full `rp status --json` is the single most common waste. Use `--filter acme/api`.

## Exit-code contract

| Code | Meaning | What you do |
|------|---------|-------------|
| 0 | Everything succeeded / nothing to report | Proceed |
| 1 | **Attention needed** — dirty, missing, ahead/behind, untracked repos found | Not a failure. Report the specifics to the user; do not "fix" them |
| 2 | **Hard error** — manifest missing/invalid, clone failed, a manifest command failed | Read `error` + `hint` from the JSON and act on the hint |

In JSON mode the same value is in the body as `exit_code`, so one capture gives you both. **Exit 1 is a finding, not a crash** — a tool wrapper that treats non-zero as failure will misreport `rp status` and `rp check`.

On an error, branch on `exit_code` and read `error` + `hint`. Since v0.9.0 every command stamps its own name in `command`, so routing on it is safe; only a global-flag error (e.g. `-c 0`, rejected before any subcommand runs) reports `"rp"`.

Per-command specifics: `rp check` is 0/1/2 with *zero output*. `rp discover` exits 1 when untracked repos exist. `rp bootstrap` never exits 1 — clone failures are 2. `rp install`/`update` exit 1 on skips, 2 on any command failure.

## Workflow A: Orient in an unfamiliar workspace

Three compact calls, in order, before anything else:

```bash
rp validate --json --compact      # does the manifest even parse?  (exit 2 = stop here)
rp list --json --compact          # how big is this, how many missing?
rp status --json --compact        # is it healthy?
```

`validate` first is deliberate: it has **zero side effects** and no git calls, so a broken manifest costs one call instead of surfacing as a confusing error inside `status`. Its summary (`repos`, `owners`, `categories`, `install_commands`, `update_commands`) also tells you the workspace's shape for free.

## Workflow B: Find where a repo lives

```bash
rp list --json --filter acme/api      # → repos[0].local_path
```

**Never reconstruct the path yourself.** `base_dir` + owner + name is only correct for *flat* owners; a categorized owner inserts a category segment (`{base_dir}/{owner}/{category}/{name}`), and the layout is inferred per-owner from YAML structure. `local_path` is the answer; guessing is how you end up `cd`-ing into a directory that does not exist.

`rp status --json` also carries `local_path`, so if you already ran status you have it.

## Workflow C: Pre-flight before cross-repo work

Before editing, branching, or running anything across several repos:

```bash
rp status --json --compact --filter acme/
```

- `attention: 0` and `not_cloned: 0` → safe to proceed.
- Otherwise drill down: `rp status --json --dirty --filter acme/` (and `--ahead` / `--behind`).

**Report what you found and let the user decide.** rp deliberately refuses to auto-stash or auto-commit; you should too. A dirty repo means someone has work in progress — touching it is destructive in a way no tool can undo for them.

Afterwards, the same call shows exactly which repos you left changed — a cheap post-flight check before you hand back.

## Workflow D: Refresh or onboard a workspace

```bash
rp up --dry-run --json        # preview all four phases — ALWAYS first
rp up --json                  # bootstrap → sync → install → update
```

`rp up` is the composite: clone missing → pull clean → run `install:` → run `update:`. It exists to collapse four round-trips into one, which is exactly what an agent wants — but:

> **`install`/`update` execute arbitrary shell from the manifest via `sh -c`.** On a manifest you have not read, confirm with the user before the first non-dry run, or start with `rp up --no-install --no-update` (git operations only) and add the command phases once you have seen what they are.

`--no-install` / `--no-update` skip a phase entirely — the corresponding JSON key comes back **`null`**, not a zeroed object, so null-check before reading it. The JSON is an `UpResult`: `bootstrap`, `sync`, `install`, `update` sub-objects instead of a top-level `summary`/`repos` — see REFERENCE.md.

For a git-only refresh, `rp sync` alone is often the right call; `rp bootstrap` alone for a fresh machine.

## Workflow E: Adopt untracked GitHub repos

```bash
rp discover --json                    # requires gh CLI, authenticated
# → edit the manifest YAML (add the repo under its owner)
rp validate --json                    # confirm it still parses
rp bootstrap --dry-run --json         # confirm only the new repo would clone
rp bootstrap --json
```

`discover` scans the authenticated user's personal account **and every org they are a member of**, excluding forks and archived repos unless `--forks` / `--archived`. Exit 1 means untracked repos were found — that is the normal, useful outcome, not an error.

Never skip the `validate` → `bootstrap --dry-run` pair after a manifest edit: a typo'd owner name silently creates a *new owner* rather than an error, and dry-run is where you notice.

## Workflow F: What changed recently

```bash
rp diff --since 7d --json     # repos with commits in the last 7 days
rp diff --since 24h --json
```

Per repo: `sha`, `message`, `date` (RFC3339 UTC), `days_ago`. Summary carries `total` (all repos) and `shown` (after `--since`), so the ratio tells you how much the filter cut. This is the cheapest way to answer "what has been active lately" without walking any git log yourself.

## Workflow G: Scripts and CI

```bash
rp check && echo ok           # zero output, exit 0/1/2
rp check --filter acme/
```

`check` is the boolean form of `status` — no output at all, so it costs nothing to run in a loop or a hook. Use it when you only need the verdict; use `status --json --compact` when you need the counts.

## Reading `status` correctly

A repo needs attention when **it is not clean**, or **it has an upstream and is ahead or behind**. Consequences worth knowing:

- A clean repo with **no upstream** (never pushed, detached, local-only branch) is **OK**, not attention. `has_upstream: false` is the tell — check it before concluding "in sync".
- `not_cloned` is counted **separately** from `attention`. A workspace can be `attention: 0` and still be missing 30 repos. Always read both.
- Per-repo state fields (`clean`, `dirty_files`, `ahead`, `behind`, `has_upstream`) are **present for every cloned repo — including as `0` / `false` — and absent for uncloned ones.** So an absent `dirty_files` means "not cloned", never "clean". Branch on `cloned` first.

`--dirty`, `--ahead`, `--behind` filter the *output*; they do not change the summary counts or the exit code.

## Filtering — exact rules, and the traps

| Filter | Matches |
|--------|---------|
| `acme/api` | that one repo, exactly |
| `acme/` | every repo whose **owner is exactly** `acme` |
| `acme` | identical to `acme/` |

Repeat `--filter` for a union: `--filter acme/ --filter vendor/`.

Traps:

- **No globs, no substrings, no regex.** `--filter acme*` matches nothing. Owner comparison is `==`, despite "prefix" wording in older docs.
- **You cannot filter by category.** `--filter acme/services` is read as an exact repo named `services` under `acme` → zero matches. Filter the owner and select categories from the JSON.
- **A no-match filter is not an error** — you get an empty `repos` array and exit 0. Silence here means "your filter was wrong", not "everything is fine". Check `summary.total`.
- **`rp discover` has its own, different filter syntax** — case-insensitive, and an owner match *requires* the trailing slash. `--filter acme` matches nothing in `discover` while working everywhere else. Always write `acme/` there.
- **For `install` / `update`, a positional repo argument overrides `--filter`** and warns on stderr. `rp install acme/api --filter vendor/` installs `acme/api` only. An unknown positional exits 2 with a hint; an unknown `--filter` is silently empty.

## Editing the manifest

Location: `~/.config/rp/manifest.yaml` (`-m` / `RP_MANIFEST` to override).

**Layout is inferred from YAML node type — this is the one rule to internalise:**

```yaml
base_dir: ~/Developer

acme:                      # MAPPING → categorized → ~/Developer/acme/services/api/
  services:
    - repo: acme/api
      install: [go mod download]
      update:  [go mod download]

opensource:                # SEQUENCE → flat → ~/Developer/opensource/tools/
  - repo: opensource/tools
```

A mapping under an owner means categories; a sequence means flat. Switching one to the other **moves every path under that owner** — rp will then see the repos as not-cloned and offer to re-clone them into new directories. Never restructure an owner casually.

Validation rules (all are hard errors, exit 2, each with a hint):

1. `base_dir` present and non-empty.
2. `repo` matches `{owner}/{name}` — alphanumerics, `-`, `_`, `.` only.
3. No duplicate repo anywhere in the manifest.
4. Owner and category names must be valid directory names (no `/`, `..`, null bytes).
5. At least one owner with at least one repo.
6. A category must hold a non-empty repo list.
7. `install`/`update` entries must be non-empty strings.
8. No duplicate top-level keys.

Removed keys still worth recognising, because their error is specific: a top-level `owners:` wrapper (delete it, dedent one level) and per-repo `deps:` (rename to `install:`).

`rp manifest init --output <path>` **refuses to overwrite an existing file** (exit 2 with a hint) — that is a feature. To regenerate, print to stdout, diff against the current manifest, and merge by hand; `init` discovers repos but knows nothing about your `install:`/`update:` commands and will drop them.

## Anti-patterns — do NOT do these

- **Do NOT loop `git -C <path> status` over repos.** That is the entire problem rp exists to solve: one `rp status --json` is one call, parallel, in manifest order.
- **Do NOT call `rp status` once per repo in a loop.** Every command already takes repeatable `--filter`; one call with several filters beats N calls.
- **Do NOT auto-resolve dirty repos.** No stashing, no committing "to make sync work", no `git checkout .`. Report and stop. rp itself refuses to; you have no better claim.
- **Do NOT run `rp up` to "just have a look".** It clones, pulls, and executes manifest shell commands. `rp status` looks; `rp up --dry-run` previews; `rp up` acts.
- **Do NOT parse human output.** The symbols (`OK`/`!!`/`XX`), padding, and grouping are for terminals and are not a contract. `--json` is.
- **Do NOT reconstruct `local_path` from `base_dir`.** Flat vs categorized changes the depth. Read `local_path`.
- **Do NOT use `--compact` and then read `repos`.** The key is absent in compact mode.
- **Do NOT read dry-run counts as a different schema.** `bootstrap --dry-run` and `sync --dry-run` reuse the normal counters to mean *would* clone / *would* pull / *would* skip; `dry_run: true` in the body is the discriminator, and `repos[].action` carries the `would_*` detail.
- **Do NOT treat exit 1 as failure.** It is rp's "you should look at this" channel and is expected from `status`, `list --missing`, `check`, `discover`, and `install`/`update` with skips.
- **Do NOT assume `install`/`update` are idempotent or safe.** They run whatever the manifest author wrote, in the repo directory, via `sh -c`.

## Key commands

| Command | Purpose |
|---------|---------|
| `rp validate --json --compact` | Manifest parses & passes all rules. Zero side effects — the cheapest first call |
| `rp status --json --compact` | Workspace health in one summary (`ok`/`attention`/`not_cloned`/`total`) |
| `rp status --json --dirty` | Only the repos with uncommitted work |
| `rp list --json --filter o/r` | Resolve a repo's `local_path` |
| `rp list --json --missing` | Declared but not cloned |
| `rp check` | Boolean verdict, no output — for scripts and hooks |
| `rp sync --dry-run --json` | Preview what would be pulled / skipped and why |
| `rp sync --json` | Fast-forward-pull every clean repo |
| `rp bootstrap --json` | Clone every missing repo over SSH |
| `rp up --dry-run --json` | Preview all four phases |
| `rp up --json` | bootstrap + sync + install + update in one call |
| `rp up --no-install --no-update --json` | Git-only refresh (no manifest shell commands) |
| `rp install [repo] --dry-run --json` | Show the install commands without running them |
| `rp diff --since 7d --json` | Latest commit per repo, recency-filtered |
| `rp discover --json` | GitHub repos not in the manifest (needs `gh`) |
| `rp manifest init --dir ~/Developer --dry-run` | Discover repos on disk and preview the generated YAML |

## Installation

```bash
# Install the binary
brew install deligoez/tap/rp     # alias for the deligoez-rp formula; installs the 'rp' binary
go install github.com/deligoez/rp@latest

# Install this skill for Claude Code
npx skills add -g deligoez/rp
```

## Reference

For every command's full JSON schema, summary fields, action/status/reason enums, the error-hint table, and the manifest data structures: see [REFERENCE.md](REFERENCE.md).
