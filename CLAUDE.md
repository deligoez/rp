# rp — Repo Manager CLI

Manages a developer's local workspace of git repositories. Go CLI tool.

## Key Concept: AX (Agent Experience)

rp is designed for both human and AI agent users. The `--json` flag enables structured output for agent consumption. All commands support `--json`, `--compact`, and `--filter` flags.

**AX principles applied:**
- Structured JSON output on every command (`--json`)
- Compact mode for minimal token overhead (`--compact`)
- Repo filtering to narrow scope (`--filter`)
- Composite `rp up` command to minimize round-trips
- Actionable error hints in both JSON and human output
- Exit codes: 0=success, 1=attention needed, 2=hard error

## Quick Reference

```bash
# Build
go build .

# Test
go test ./...

# Vet
go vet ./...

# Quality gate (run after every change)
go build ./... && go test ./... && go vet ./...
```

## Commands

```bash
rp bootstrap              # Clone missing repos
rp sync                   # Pull clean repos, skip dirty
rp status                 # Show state of all repos
rp deps [repo]            # Run dep install commands from manifest
rp list                   # List all repos
rp manifest init          # Scan dirs, generate manifest
rp up                     # Bootstrap + sync + deps in one call
rp check                  # Boolean exit code (0=ok, 1=attention, 2=error)
rp diff                   # Show latest commit per repo
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

### Per-Command Flags
### Per-Command Flags
```
bootstrap --dry-run
sync --dry-run
status --dirty --ahead --behind
deps [repo] --dry-run
list --missing
manifest init --dir <path> --output <path> --dry-run
up --dry-run --no-deps
check                             # no flags except --filter
diff --since <Nd|Nh>
```
## Project Structure

```
## Project Structure

```
cmd/                      Cobra commands
  root.go                 Global flags, config precedence, error handler
  bootstrap.go            Clone missing repos (human + JSON paths)
  sync.go                 Pull clean repos (human + JSON paths)
  status.go               Repo state report (human + JSON paths)
  deps.go                 Run dep commands (human + JSON paths)
  list.go                 Repo listing (human + JSON paths)
  manifest_init.go        Dir scan + manifest generation
  up.go                   Composite bootstrap+sync+deps
  check.go                Boolean exit code, zero output
  diff.go                 Latest commit per repo, --since filter
  json_test.go            JSON integration tests (subprocess)
internal/
  manifest/
    manifest.go           YAML parsing via yaml.Node, validation, path resolution
    filter.go             FilterRepos, FilterOwners
    manifest_test.go      Unit tests (parsing, validation, flat/categorized)
    filter_test.go        Filter tests
  git/
    git.go                Clone, Pull, Status, LastCommitDate, IsRepo
    git_test.go           17 unit tests (13 spec + 4 QA regression)
  deps/
    deps.go               RunDeps via sh -c
    deps_test.go          7 unit tests
  output/
    output.go             SuccessResult, ErrorResult, UpResult, HintError, PrintAndExit
    output_test.go        8 unit tests
  ui/
    ui.go                 Lipgloss symbols (OK/!!/XX), Plural, PadRight
  worker/
    worker.go             Generic Pool[T,R] with progress on stderr
spec/                     Specs and task breakdowns, versioned
  v{version}/             One folder per release (e.g. v0.1.0, v0.2.0)
    spec.md               Feature/release spec
    tasks.json            Task breakdown generated from spec
main.go                   Entry point
```

### Spec File Convention
- Specs live in `spec/v{version}/` folders, prefixed with the release version they target
- Each folder contains `spec.md` and `tasks.json` with matching names
- Suffix variants (e.g. `v0.1.0-ax`) are allowed for additive specs within a release
## Conventions

- Exit codes: 0=success, 1=attention (dirty/missing/behind), 2=hard error
- JSON output when `--json` flag or `RP_JSON=1` env
- `--compact` omits `repos` array from JSON (summary only)
- Human output: colored symbols OK (green), !! (yellow), XX (red)
- Progress on stderr: `[n/m] verb...` (TTY only)
- All git operations shell out to `git` binary (no go-git library)
- Worker pool preserves manifest order in output
- Manifest uses yaml.Node for key order preservation
- `os.Exit()` only in human mode; JSON mode uses `output.PrintAndExit`
- Errors wrapped with `output.HintError` for actionable hints

## Manifest Format
## Manifest Format

Location: `~/.config/rp/manifest.yaml`

```yaml
base_dir: ~/Developer

owners:
  acme:                              # mapping → categorized
    services:
      - repo: acme/api
        deps:
          - go mod download
      - repo: acme/web
        deps:
          - npm install
  opensource:                        # sequence → flat
    - repo: opensource/design-system
    - repo: opensource/tools
```

### Path Rules
- Categorized (mapping): `{base_dir}/{owner}/{category}/{repo_name}/`
- Flat (sequence): `{base_dir}/{owner}/{repo_name}/`
- Owner type is inferred from YAML structure: mapping = categorized, sequence = flat
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
{"command": "up", "exit_code": 0, "bootstrap": {...}, "sync": {...}, "deps": {...}}
```

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `RP_MANIFEST` | Override manifest path |
| `RP_CONCURRENCY` | Override concurrency (positive int, invalid ignored) |
| `RP_JSON` | Enable JSON output (any non-empty value) |
| `NO_COLOR` | Disable color output (per no-color.org) |

## Testing
## Testing

Tests across 5 test files:
- `internal/manifest`: parsing/validation + filter tests
- `internal/git`: 17 git operation tests (use temp repos)
- `internal/deps`: 7 command execution tests
- `internal/output`: 8 JSON serialization tests
- `cmd`: end-to-end integration tests (subprocess: JSON output, check, diff, deps dry-run, sync errors, hints, QA regressions)

Git tests create real temp repos with `git init`, commits, and bare repos for clone/pull testing. Integration tests build the binary and run it as a subprocess.
