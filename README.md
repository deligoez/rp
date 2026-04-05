# rp

Repo manager CLI — organize, sync, and bootstrap your Developer workspace.

`rp` reads a YAML manifest that declares which git repos should exist where, then can bootstrap (clone all), sync (pull all), report status, manage dependencies, and more.

## Install

```bash
go install github.com/deligoez/rp@latest
```

Or build from source:

```bash
git clone git@github.com:deligoez/rp.git
cd rp
go build .
```

## Quick Start

1. Generate a manifest from your existing workspace:

```bash
rp manifest init --dir ~/Developer --output ~/.config/rp/manifest.yaml
```

2. See what you have:

```bash
rp list
rp status
```

3. Clone missing repos and pull updates:

```bash
rp up
```

## Manifest

The manifest lives at `~/.config/rp/manifest.yaml` (override with `-m` or `RP_MANIFEST` env).

```yaml
base_dir: ~/Developer

owners:
  deligoez:
    projects:
      - repo: deligoez/tp
        deps:
          - go mod download
      - repo: deligoez/blog
        deps:
          - composer install
          - npm install
    packages:
      - repo: deligoez/laravel-model-hashids
    archive:
      - repo: deligoez/roast

  phonyland:
    flat: true
    repos:
      - repo: phonyland/cloud
    archive:
      - repo: phonyland/framework

  tarfin-labs:
    flat: true
    repos:
      - repo: tarfin-labs/backend
        deps:
          - composer install
          - npm install
```

### Directory mapping

| Mode | Category | Path |
|------|----------|------|
| Categorized | regular | `~/Developer/deligoez/projects/tp/` |
| Categorized | archive | `~/Developer/deligoez/archive/roast/` |
| Flat (`flat: true`) | regular | `~/Developer/phonyland/cloud/` |
| Flat | archive | `~/Developer/phonyland/archive/framework/` |

Repos are cloned via SSH: `git@github.com:{owner}/{name}.git`

## Commands

### rp bootstrap

Clone every repo that doesn't exist locally.

```bash
rp bootstrap              # clone all missing
rp bootstrap --dry-run    # preview what would be cloned
```

### rp sync

Pull all clean repos, skip dirty or unpushed ones.

```bash
rp sync                   # pull clean repos, skip dirty
rp sync --dry-run         # preview
```

Evaluation order per repo:
1. Not cloned — clone it
2. Not a git repo — report error
3. Dirty — skip (takes precedence over unpushed)
4. Unpushed commits — skip
5. Clean — `git pull --ff-only`

### rp status

Show the state of every repo.

```
deligoez
  projects/tp              OK main
  projects/blog            !! main +2 ahead
  projects/forum           !! main ~3 dirty
  archive/roast            OK main

phonyland
  cloud                    OK main
  archive/framework        XX not cloned

-- Summary --
39 OK, 2 need attention, 1 not cloned
```

```bash
rp status                 # all repos
rp status --dirty         # only dirty repos
rp status --ahead         # only repos with unpushed commits
rp status --behind        # only repos behind remote
```

### rp deps

Run dependency install commands defined in the manifest.

```bash
rp deps                          # all repos with deps
rp deps tarfin-labs/backend      # specific repo
```

Commands are defined per repo in the manifest (`deps:` field) and run via `sh -c`.

### rp archive

Report repos that haven't been committed to in a while.

```bash
rp archive                       # repos with no commit in 365+ days
rp archive --threshold 180       # custom threshold
```

### rp list

List all repos in the manifest.

```bash
rp list                   # all repos
rp list --missing         # only repos not cloned locally
```

### rp manifest init

Scan a directory tree and generate a manifest.

```bash
rp manifest init --dir ~/Developer
rp manifest init --dir ~/Developer --output ~/.config/rp/manifest.yaml
rp manifest init --dry-run       # preview discovered repos
```

### rp up

Bootstrap + sync + deps in one command.

```bash
rp up                     # clone, pull, install deps
rp up --dry-run           # preview bootstrap + sync
rp up --no-deps           # skip dep installation
```

## Global Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--manifest` | `-m` | `~/.config/rp/manifest.yaml` | Path to manifest file |
| `--concurrency` | `-c` | `4` | Max parallel operations |
| `--no-color` | | `false` | Disable colored output |
| `--json` | | `false` | Output structured JSON |
| `--compact` | | `false` | Summary only (with `--json`) |
| `--filter` | | | Filter repos (repeatable) |

### Filtering

```bash
rp status --filter deligoez/tp          # exact repo
rp status --filter deligoez/            # all repos under owner
rp status --filter deligoez             # same as above
rp sync --filter deligoez/ --filter phonyland/cloud   # multiple
```

## JSON Output

All commands support `--json` for structured output. Also enabled with `RP_JSON=1` env var.

```bash
rp status --json
rp status --json --compact    # summary only, no per-repo details
rp list --json --filter deligoez/
```

Example:

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
      "cloned": true,
      "branch": "main",
      "clean": true,
      "dirty_files": 0,
      "ahead": 0,
      "behind": 0,
      "has_upstream": true
    }
  ]
}
```

Errors in JSON mode include actionable hints:

```json
{
  "command": "status",
  "exit_code": 2,
  "error": "reading manifest: open /bad/path: no such file",
  "hint": "create manifest with: rp manifest init --dir ~/Developer"
}
```

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `RP_MANIFEST` | Override manifest path |
| `RP_CONCURRENCY` | Override concurrency |
| `RP_JSON` | Enable JSON output |
| `NO_COLOR` | Disable colors (per [no-color.org](https://no-color.org)) |

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All operations succeeded |
| 1 | Some repos need attention (dirty, missing, behind) |
| 2 | Hard error (manifest parse, clone failure, command failure) |

## Design Principles

- **Report only** — never auto-commit, auto-push, or auto-stash
- **Shell out to git** — uses the `git` binary, not a Go git library
- **Parallel** — worker pool with configurable concurrency
- **Deterministic output** — results printed in manifest order
- **Agent-friendly** — structured JSON on every command, compact mode, filtering

## Tech Stack

- Go 1.24+
- [cobra](https://github.com/spf13/cobra) for CLI
- [lipgloss](https://github.com/charmbracelet/lipgloss) for styled terminal output
- [yaml.v3](https://gopkg.in/yaml.v3) for manifest parsing (with `yaml.Node` for key order)

## License

[MIT](LICENSE)
