# rp — Repo Manager CLI

Manages a developer's local workspace of git repositories. Go CLI tool.

## Committing in this repo

ALWAYS commit through `hc` — never `git add` / `git commit` / `git commit -a`. Read
`hc diff --json` once, start from `hc plan`, review every entry, then `hc run`. Follow the
granularity rules in the `hc` skill: one file per commit by default; multi-file only for
mechanical sweeps or inseparable changes; `feat`/`fix`/`test`/`docs` never share a commit;
each NEW test is its own commit, separate from the code it covers. Commit at unit-of-work
cadence — after each change plus its passing test — never as one batch at the end.

## Key Concept: AX (Agent Experience)

rp is designed for both human and AI agent users. The `--json` flag enables structured output for agent consumption. All commands support `--json`, `--compact`, and `--filter` flags.

**AX principles applied:**
- Structured JSON output on every command (`--json`)
- Compact mode for minimal token overhead (`--compact`)
- Repo filtering to narrow scope (`--filter`)
- Composite `rp up` command to minimize round-trips
- Actionable error hints in both JSON and human output
- Exit codes: 0=success, 1=attention needed, 2=hard error

## Install

```bash
brew tap deligoez/tap && brew install rp   # Homebrew
go install github.com/deligoez/rp@latest   # or Go
```

## Quick Reference

```bash
# Build
go build .

# Test
go test ./...

# Vet
go vet ./...

# Quality gate (run after every change)
go build ./... && go test ./... && golangci-lint run && ./scripts/deadcode.sh
```

## Tooling beyond the gate

`golangci-lint run` must report **0 issues** and `./scripts/deadcode.sh` must exit 0.
Both run in CI. `golangci-lint` includes `govet`, so a separate `go vet` step is redundant.

- **Pin the linter, and pin it to what you run locally.** CI installs
  `golangci-lint@v2.13.2` with `go install`, not `golangci-lint-action` (the action is
  versioned independently and a v6/config-v2 mismatch reads like a config error). A gate
  whose version floats is a function of the tool, not of the code. Before bumping the pin,
  run that version locally first — and check it supports the Go release `go-version: stable`
  resolves to: v2.12.x panics on Go 1.27's stdlib inside staticcheck.
- **`deadcode` is not redundant with `unused`.** `unused` skips exported identifiers by
  design; `deadcode` builds a call graph and catches the exported helper nothing calls.
  The gate uses `-test` (tests count as entry points), so it reports only genuinely dead
  code. Running it *without* `-test` additionally lists test-only code — a useful review
  signal, not a gate.
- **`issues.uniq-by-line: false` is load-bearing.** `gocognit` and `funlen` both report on
  a function's declaration line, and the default (`true`) shows only one finding per line —
  so every `funlen` violation on a function that also trips `gocognit` is invisible.
  `max-issues-per-linter`/`max-same-issues` are zeroed for the same reason: the defaults
  cap output at 50 and 3 and hide how large a problem actually is.
- **Complexity thresholds are `gocognit` 40, `funlen` 100 lines / 60 statements, `dupl` 150,
  and tests are excluded from all four.** Test files are legitimately long and repetitive;
  measuring them against production thresholds only pushes the tests toward being worse.
- **`hugeParam`/`rangeValCopy` are set to 129 bytes, not disabled.** `manifest.RepoEntry` is
  128 bytes and is passed by value everywhere; for a tool iterating tens of repos, pointers
  would buy nothing measurable and invite aliasing bugs. Anything genuinely larger still gets
  flagged.
- **Do not add a `//nolint` to get the gate green.** Every current finding was refactored
  away rather than suppressed, so the gate is at a true zero and the next violation is
  visible the moment it appears.

## Commands

```bash
rp bootstrap              # Clone missing repos
rp sync                   # Pull clean repos, skip dirty
rp status                 # Show state of all repos
rp install [repo]         # Run install commands from manifest
rp update [repo]          # Run update commands from manifest
rp list                   # List all repos
rp manifest init          # Scan dirs, generate manifest
rp up                     # Bootstrap + sync + install + update in one call
rp check                  # Boolean exit code (0=ok, 1=attention, 2=error)
rp diff                   # Show latest commit per repo
rp discover               # Find GitHub repos not in manifest (requires gh)
```

### Global Flags
```
--json                    Structured JSON output
--compact                 Summary only (with --json)
--filter <value>          Filter repos (repeatable): owner/ or owner/repo
-m, --manifest <path>     Manifest path (default: ~/.config/rp/manifest.yaml)
-c, --concurrency <n>     Parallel workers (default: 4)
--no-color                Disable colors
```
rp discover               # Find GitHub repos not in manifest (requires gh)
rp validate [path]        # Validate manifest structure (no side effects)
### Per-Command Flags
```
bootstrap --dry-run
sync --dry-run
status --dirty --ahead --behind
install [repo] --dry-run
update [repo] --dry-run
list --missing
manifest init --dir <path> --output <path> --dry-run
up --dry-run --no-install --no-update
check                             # no flags except --filter
diff --since <Nd|Nh>
discover --forks --archived
validate [path]                   # no flags beyond global
```

### Command Behavior

- **bootstrap**: Clone missing repos via SSH (`git@github.com:{repo}.git`). Skip already-cloned. `--dry-run` previews.
- **sync**: Per-repo evaluation order: not cloned → skip, not git → error, dirty → skip, unpushed → skip, clean → `git pull --ff-only`.
- **status**: Reports per-repo: branch, dirty file count, ahead/behind counts, upstream presence. Flags filter output.
- **install**: Runs `install:` commands via `sh -c` in each repo's directory. Skips repos without install commands. Positional arg overrides `--filter`.
- **update**: Runs `update:` commands via `sh -c` in each repo's directory. Skips repos without update commands. Positional arg overrides `--filter`.
- **list**: Shows all repos grouped by owner/category. `--missing` shows only uncloned.
- **manifest init**: Scans a directory tree, discovers git repos with GitHub remotes, infers flat (depth-1) vs categorized (depth-2) layout, generates YAML.
- **up**: Runs bootstrap → sync → install → update in sequence. `--no-install` skips the install phase. `--no-update` skips the update phase. JSON output wraps all four as sub-results.
- **diff**: Shows latest commit (sha, message, date, days_ago) per repo. `--since` filters by recency.
- **discover**: Lists GitHub repos not in manifest. Requires `gh` CLI. Scans personal account + all member orgs. `--forks` includes forks, `--archived` includes archived. Exit 0 = all tracked, exit 1 = untracked found.
- **validate**: Loads the manifest via `manifest.Load()` and reports whether it parses and passes all validation rules. Positional `[path]` overrides `--manifest`. No side effects. Exit 0 = valid, exit 2 = parse/validation error. JSON summary has `valid`, `repos`, `owners`, `categories`, `install_commands`, `update_commands`.

## Project Structure

```
cmd/                      Cobra commands
  root.go                 Global flags, config precedence, error handler
  bootstrap.go            Clone missing repos (human + JSON paths)
  sync.go                 Pull clean repos (human + JSON paths)
  status.go               Repo state report (human + JSON paths)
  install.go              Run install commands (human + JSON paths)
  update.go               Run update commands (human + JSON paths)
  list.go                 Repo listing (human + JSON paths)
  manifest_init.go        Dir scan + manifest generation
  up.go                   Composite bootstrap+sync+install+update
  check.go                Boolean exit code, zero output
  diff.go                 Latest commit per repo, --since filter
  discover.go             Find untracked GitHub repos (requires gh CLI)
  validate.go             Validate manifest structure, zero side effects
  discover_test.go        Unit tests (filterUntracked, matchesDiscoverFilter)
  json_test.go            JSON integration tests (subprocess)
internal/
  manifest/
    manifest.go           YAML parsing via yaml.Node, validation, path resolution
    filter.go             FilterRepos, FilterOwners
    manifest_test.go      Unit tests (parsing, validation, flat/categorized)
    filter_test.go        Filter tests
  git/
    git.go                Clone, Pull, Status, LastCommitDate, IsRepo
    git_test.go           Unit tests (use real temp repos)
  runner/
    runner.go             RunCommands via sh -c
    runner_test.go        Unit tests
  output/
    output.go             SuccessResult, ErrorResult, UpResult, HintError, PrintAndExit
    output_test.go        Unit tests
  ui/
    ui.go                 Lipgloss symbols (OK/!!/XX), Plural, PadRight
  worker/
    worker.go             Generic Pool[T,R] with progress on stderr
skills/rp/
  SKILL.md                Agent skill for Claude Code (activation, workflows, anti-patterns)
  REFERENCE.md            Full JSON schemas, enums, hint table, manifest schema
spec/                     Specs and task breakdowns, versioned
  v{version}/             One folder per release (e.g. v0.1.0, v0.2.0)
    spec.md               Feature/release spec
    tasks.json            Task breakdown generated from spec
main.go                   Entry point
```

### Skill Maintenance
`skills/rp/` is a user-facing contract, not documentation-by-courtesy. Any change to a
command's flags, JSON keys, exit codes, filter semantics, or error hints MUST update
`SKILL.md` (if it changes what an agent should *do*) and `REFERENCE.md` (always). Treat a
stale schema in REFERENCE.md as a bug of the same severity as a wrong `--help` string.

### Spec File Convention
- Specs live in `spec/` as flat files: `v{version}.md` and `v{version}.json`
- Suffix variants (e.g. `v0.1.0-ax`) are allowed for additive specs within a release

## Conventions

- Exit codes: 0=success, 1=attention (dirty/missing/behind), 2=hard error
- JSON output when `--json` flag or `RP_JSON=1` env
- `--compact` omits `repos` array from JSON (summary only)
- Human output: colored symbols OK (green), !! (yellow), XX (red)
- Human progress: `bootstrap`/`sync`/`install`/`update` stream `[n/m] ...` per-repo lines to stdout in completion order (`PoolWithLiveLog`); `up` keeps the overwriting `[n/m] verb...` bar on stderr (TTY only)
- All git operations shell out to `git` binary (no go-git library)
- Worker pool preserves manifest order in JSON output (human live-log output is completion order)
- Manifest uses yaml.Node for key order preservation
- `os.Exit()` only in human mode; JSON mode uses `output.PrintAndExit`
- Errors wrapped with `output.HintError` for actionable hints
- Config precedence: flag > env var > default value
- Clone URL: `git@github.com:{repo}.git` (SSH)

## Manifest Format

Location: `~/.config/rp/manifest.yaml`

```yaml
base_dir: ~/Developer

acme:                              # mapping → categorized
  services:
    - repo: acme/api
      install:
        - go mod download
      update:
        - go mod download
    - repo: acme/web
      install:
        - npm install
      update:
        - npm update
opensource:                        # sequence → flat
  - repo: opensource/design-system
  - repo: opensource/tools
```

### Path Rules
- Categorized (mapping): `{base_dir}/{owner}/{category}/{repo_name}/`
- Flat (sequence): `{base_dir}/{owner}/{repo_name}/`
- Owner type is inferred from YAML structure: mapping = categorized, sequence = flat

### Manifest Validation Rules
1. `base_dir` must be present and non-empty
2. `repo` must match `{owner}/{name}` (alphanumeric, hyphens, underscores, dots)
3. No duplicate repos across entire manifest
4. Owner and category names must be valid directory names (no `/`, `..`, null bytes)
5. At least one owner with at least one repo (top-level keys beyond `base_dir`)
6. Categories must contain a non-empty repo list
7. `install` and `update` entries must be non-empty strings
8. No duplicate top-level keys

### Key Data Structures
- **RepoEntry**: Repo, Owner, Category (empty for flat), LocalPath, CloneURL, Install, Update
- **OwnerGroup**: Name, IsFlat (derived from YAML node type), Repos
- **Manifest**: BaseDir, owners (private, accessed via Repos()/Owners())

## JSON Output

Every command supports `--json`. Two result types:

**Success:**
```json
{"command": "status", "exit_code": 0, "summary": {...}, "repos": [...]}
```

**Error:**
```json
{"command": "status", "exit_code": 2, "error": "...", "hint": "..."}
```

**Composite (rp up):**
```json
{"command": "up", "exit_code": 0, "bootstrap": {...}, "sync": {...}, "install": {...}, "update": {...}}
```

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `RP_MANIFEST` | Override manifest path |
| `RP_CONCURRENCY` | Override concurrency (positive int, invalid ignored) |
| `RP_JSON` | Enable JSON output (any non-empty value) |
| `NO_COLOR` | Disable color output (per no-color.org) |

## Testing

Tests across 6 test files:
- `internal/manifest`: parsing/validation + filter tests
- `internal/git`: git operation tests (use temp repos)
- `internal/runner`: command execution tests
- `internal/output`: JSON serialization tests
- `cmd/json_test.go`: end-to-end integration tests (subprocess: JSON output, check, diff, install dry-run, update dry-run, sync errors, hints, discover, validate, QA regressions)
- `cmd/discover_test.go`: unit tests for filterUntracked and matchesDiscoverFilter

Git tests create real temp repos with `git init`, commits, and bare repos for clone/pull testing. Integration tests build the binary and run it as a subprocess.
