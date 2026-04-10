# Spec: Remove `owners` wrapper from manifest format

**Version:** v0.5.0
**Type:** Breaking change — manifest format simplification

## Motivation

The `owners:` key in the manifest is a redundant wrapper. Owner names already sit directly under it as mapping keys, so `owners:` adds indentation and nesting without conveying information. Removing it makes the manifest flatter and easier to read.

### Before (v0.4.x)

```yaml
base_dir: ~/Developer

owners:
  deligoez:
    projects:
      - repo: deligoez/tp
      - repo: deligoez/rp
  phonyland:
    - repo: phonyland/cloud
```

### After (v0.5.0)

```yaml
base_dir: ~/Developer

deligoez:
  projects:
    - repo: deligoez/tp
    - repo: deligoez/rp
phonyland:
  - repo: phonyland/cloud
```

## Scope

1. Remove the `owners:` wrapper from manifest parsing
2. Update manifest generation (`manifest init`)
3. Update all tests (unit + integration)
4. Update documentation (README.md, CLAUDE.md)
5. Add QA regression tests

## Design

### 1. Parsing changes (`internal/manifest/manifest.go`)

#### 1.1 rawManifest struct

Remove the `Owners yaml.Node` field and the `rawManifest` struct entirely. The parser must now treat every top-level YAML key **except reserved keys** as an owner name.

**Reserved top-level keys:** `base_dir`, `owners`
- `base_dir` — consumed as config, not an owner
- `owners` — rejected with migration hint (see 1.3)

Any top-level key that is not reserved is treated as an owner name and its value is passed to `parseOwnerNode()`. If `parseOwnerNode()` fails (e.g. the value is a scalar instead of a mapping/sequence), its existing error message is sufficient — no additional "unknown key" mechanism is needed.

All top-level key matching is **case-sensitive** (consistent with YAML's default behavior).

Current struct (removed):
```go
type rawManifest struct {
    BaseDir string    `yaml:"base_dir"`
    Owners  yaml.Node `yaml:"owners"`
}
```

New approach:
- Decode the full YAML document as a `yaml.Node` via `yaml.NewDecoder().Decode()` (only the first document is used; multi-document YAML is not rejected but subsequent documents are ignored — this matches existing behavior)
- Unwrap the `DocumentNode` to get the root node (`Content[0]`). If the document is empty (no content), return error: `"manifest is empty"` with hint `"run rp manifest init to generate one"`
- Validate root node is a MappingNode (error if not: `"manifest must be a YAML mapping at the top level"`)
- Validate all top-level keys are scalar strings (reject non-scalar keys with error)
- Reject duplicate top-level keys with error: `duplicate key "<name>"`
- Scan for reserved key `owners` — if found, short-circuit (see 1.2 step 3)
- Extract `base_dir` value (must be a scalar string)
- Iterate remaining top-level keys **in document order** as owner names
- Each owner's value is parsed by the existing `parseOwnerNode()` (unchanged)

#### 1.2 Load function

Replace the current flow:
1. ~~Unmarshal into rawManifest~~ → Decode into raw `yaml.Node`
2. Unwrap `DocumentNode` → `Content[0]`. If empty document, return error with hint.
3. Validate root is a MappingNode; if not, return `HintError` (error: `"manifest must be a YAML mapping at the top level"`, hint: `"ensure manifest starts with key: value pairs, not a list"`)
4. Scan top-level keys for `owners` — if found, **short-circuit** immediately with `HintError` (do not extract `base_dir` or run any further validation). This ensures Q3 works: even if `base_dir` is missing, only the migration error is returned.
5. Extract `base_dir` from root mapping (must be scalar string; error if missing, duplicate, or non-scalar). Expand tilde.
6. Iterate all other non-reserved top-level keys as owner names, in document order
7. Call `parseOwnerNode()` for each (this function is unchanged)
8. Resolve paths and validate (unchanged)

The intermediate `parseOwners()` function is **removed**. Its logic (iterating owner keys and calling `parseOwnerNode()`) is absorbed into the new `Load` flow, since the iteration now happens at the root level instead of inside an `owners` node.

#### 1.3 Validation rules

All existing validation rules remain. Adjustments:

| Rule | Change |
|------|--------|
| R1: base_dir required | Unchanged (now extracted manually from yaml.Node, after `owners` short-circuit check) |
| R2: repo format | Unchanged |
| R3: no duplicate repos | Unchanged |
| R4: valid dir names | Now applies to top-level keys (owner names). Reserved keys (`base_dir`, `owners`) are excluded. |
| R5: at least one owner | Check that root mapping has non-reserved keys |
| R6: non-empty categories | Unchanged |
| R7: non-empty deps | Unchanged |
| R8 (new): no duplicate keys | Reject duplicate top-level keys with error |

**New validation — `owners` key rejection (short-circuit):**

If a top-level key named `owners` is found (case-sensitive, exact match), immediately return an `output.HintError` with:
- **Error:** `"owners" is no longer a valid manifest key`
- **Hint:** `Remove the "owners:" line and dedent owner blocks by one level.`

This check runs in step 3 of Load, **before** `base_dir` extraction and all other validations. It short-circuits — no further errors are reported.

### 2. Manifest generation (`cmd/manifest_init.go`)

#### 2.1 generateYAML function

Remove the `owners` MappingNode wrapper. Specific code changes:
1. Delete the `ownersKey` and `ownersVal` variable declarations (currently ~L330-332)
2. Change all `ownersVal.Content = append(ownersVal.Content, ownerKey, ownerVal)` calls to `mapping.Content = append(mapping.Content, ownerKey, ownerVal)` — owner key/value pairs are appended directly to the root `mapping` node
3. Remove the final `mapping.Content = append(mapping.Content, ownersKey, ownersVal)` line

Current structure:
```
root (Mapping)
  ├── base_dir: ...
  └── owners (Mapping)
       ├── owner1: ...
       └── owner2: ...
```

New structure:
```
root (Mapping)
  ├── base_dir: ...
  ├── owner1: ...
  └── owner2: ...
```

A blank line between `base_dir` and the first owner key is preserved in the output (matching the "After" example format).


### 3. Test updates

#### 3.1 Unit tests (`internal/manifest/manifest_test.go`)

All YAML snippets in test cases must have the `owners:` line removed and owner blocks dedented by one level. This is a mechanical transformation — no behavioral assertion changes.

**All test functions containing `owners:` YAML need updating.** This includes (but verify against actual file):

1. TestParseValidManifest
2. TestCategorizedPath
3. TestFlatPath
4. TestTildeExpansion
5. TestMissingBaseDir
6. TestInvalidRepoFormat
7. TestDuplicateRepos
8. TestEmptyManifest
9. TestRepoWithDeps
10. TestRepoWithoutDeps
11. TestCrossOwnerRepo
12. TestEmptyCategoryList
13. TestManifestOrderPreserved
14. TestEmptyStringInDeps
15. TestQA_NoOwners
16. TestQA_EmptyCategoryList
17. TestQA_CrossOwnerPath
18. TestFlatOwnerAsSequence
19. TestCategorizedOwnerAsMapping
20. TestOwnerValueScalarError
21. TestMixedOwnerTypes

Note: TestQA_EmptyManifestFile writes an empty string — no `owners:` to remove, no changes needed.

#### 3.2 Integration tests (`cmd/json_test.go`)

**All test functions and helpers in `cmd/json_test.go` that contain `owners:` YAML must be updated.** Rather than enumerating (the file has 40+ test functions), grep for `owners:` in the file and update every occurrence. This includes discover-related tests.

#### 3.3 Filter tests (`internal/manifest/filter_test.go`)

No YAML parsing — tests operate on in-memory structs. No changes needed.

### 4. New QA tests

Add the following regression tests to `internal/manifest/manifest_test.go` (Q1-Q3) and `cmd/json_test.go` (Q4):

| # | Test name | What it verifies | Expected |
|---|-----------|-----------------|----------|
| Q1 | TestQA_OwnersKeyRejectsWithHint | Manifest with `owners:` key returns `HintError` | Error contains `"owners" is no longer a valid manifest key`; hint contains `dedent owner blocks by one level` |
| Q2 | TestQA_RootNotMapping | YAML root is a sequence (e.g. `- item`) | Error contains `"manifest must be a YAML mapping at the top level"` |
| Q3 | TestQA_OwnersKeyShortCircuits | Manifest has both `owners:` and missing `base_dir` | Only the `owners` migration error is returned, not the `base_dir` error |
| Q4 | TestQA_IntegrationOldFormatRejectsJSON | `rp status --json` with old-format manifest | Exit code 2; JSON has `error` and `hint` fields with migration text |

### 5. Documentation updates

#### 5.1 README.md

- Update manifest format example (remove `owners:` wrapper, dedent owners)

#### 5.2 CLAUDE.md

Update the following specific sections:
- **Manifest Format** example YAML — remove `owners:` wrapper
- **Manifest Validation Rules** — update R5 wording, add R8 (duplicate keys)
- **Key Data Structures** — no changes (`OwnerGroup`, `Owners()` refer to the domain concept, not the YAML key)
- **Project Structure** — `manifest.go` description: remove mention of `parseOwners` if present

## Migration path

Users must manually remove the `owners:` line and dedent owner blocks by one level. The error message when `owners:` is detected provides this instruction via `HintError`.

Note: users with shared manifests (e.g. dotfiles repos) should update the `rp` binary on all machines before changing the manifest format.

## Files to modify

1. `internal/manifest/manifest.go` — parsing (remove `rawManifest`, `parseOwners`; rewrite `Load`) + validation (R8)
2. `internal/manifest/manifest_test.go` — all YAML snippets + new QA tests (Q1-Q3)
3. `cmd/manifest_init.go` — YAML generation (remove `owners` wrapper node)
4. `cmd/json_test.go` — all integration test YAML snippets (grep for `owners:`) + Q4
5. `README.md` — manifest format example
6. `CLAUDE.md` — manifest format example + validation rules + structural references

Note: `internal/manifest/filter.go` and `internal/manifest/filter_test.go` are unaffected (operate on in-memory structs, no YAML parsing).

## Out of scope

- Automatic migration tool (manual edit is trivial)
- Backward compatibility / dual-format support (clean break)
