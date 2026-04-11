package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deligoez/rp/internal/output"
	"gopkg.in/yaml.v3"
)

// repoPattern enforces {owner}/{name} with alphanumeric, hyphens, underscores, dots only.
var repoPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expandTilde: could not determine home directory: %w", err)
		}
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

// resolvePath computes the absolute local path for a repo entry.
//
// Rules (from spec section 2.2):
//   - Categorized (isFlat=false, isArchive=false): {baseDir}/{owner}/{category}/{repoName}
//   - Categorized archive (isFlat=false, isArchive=true): {baseDir}/{owner}/archive/{repoName}
//   - Flat (isFlat=true, isArchive=false):              {baseDir}/{owner}/{repoName}
//   - Flat archive (isFlat=true, isArchive=true):       {baseDir}/{owner}/archive/{repoName}
// resolvePath computes the absolute local path for a repo entry.
//
// Rules:
//   - Flat (category == ""):    {baseDir}/{owner}/{repoName}
//   - Categorized:              {baseDir}/{owner}/{category}/{repoName}
func resolvePath(baseDir, owner, category, repoName string) string {
	if category == "" {
		return filepath.Join(baseDir, owner, repoName)
	}
	return filepath.Join(baseDir, owner, category, repoName)
}

type RepoEntry struct {
	Repo      string   // e.g. "deligoez/tp"
	Owner     string   // manifest owner name
	Category  string   // e.g. "projects", empty for flat
	LocalPath string   // resolved absolute path
	CloneURL  string   // git@github.com:{repo}.git
	Install   []string // shell commands for initial setup
	Update    []string // shell commands for dependency updates
}

type OwnerGroup struct {
	Name   string
	IsFlat bool
	Repos  []RepoEntry
}

type Manifest struct {
	BaseDir string
	owners  []OwnerGroup // private, populated by Load
}

// Repos returns a flat list of all repos across all owner groups.
func (m *Manifest) Repos() []RepoEntry {
	var repos []RepoEntry
	for _, owner := range m.owners {
		repos = append(repos, owner.Repos...)
	}
	return repos
}

// Owners returns all owner groups in the manifest.
func (m *Manifest) Owners() []OwnerGroup {
	return m.owners
}

// isValidName checks that a name (owner or category) is a safe directory component:
// non-empty, no forward slash, not "..", and contains no null bytes.
func isValidName(name string) bool {
	return name != "" && name != ".." && !strings.Contains(name, "/") && !strings.Contains(name, "\x00")
}

// validate checks all manifest rules and returns the first error found.
func (m *Manifest) validate() error {
	// Rule 1: base_dir must be present and non-empty.
	if m.BaseDir == "" {
		return output.NewHintError(
			fmt.Errorf("manifest: base_dir must be present and non-empty"),
			"add base_dir to manifest: base_dir: ~/Developer",
		)
	}

	// Rule 5: at least one owner with at least one repo must exist.
	totalRepos := 0
	for _, owner := range m.owners {
		totalRepos += len(owner.Repos)
	}
	if len(m.owners) == 0 || totalRepos == 0 {
		return output.NewHintError(
			fmt.Errorf("manifest: at least one owner with at least one repo is required"),
			"add at least one owner with repos to manifest",
		)
	}

	seen := make(map[string]bool)

	for _, owner := range m.owners {
		// Rule 4: owner names must be valid directory names.
		if !isValidName(owner.Name) {
			return fmt.Errorf("manifest: invalid owner name %q (must be non-empty, no '/', no '..', no null bytes)", owner.Name)
		}

		for _, entry := range owner.Repos {
			// Rule 4: category names must be valid directory names (skip for flat repos).
			if entry.Category != "" && !isValidName(entry.Category) {
				return fmt.Errorf("manifest: owner %q has invalid category name %q (must be non-empty, no '/', no '..', no null bytes)", owner.Name, entry.Category)
			}

			// Rule 2: repo field must match {owner}/{name}.
			if !repoPattern.MatchString(entry.Repo) {
				return output.NewHintError(
					fmt.Errorf("manifest: repo %q does not match required pattern {owner}/{name} (alphanumeric, hyphens, underscores, dots only)", entry.Repo),
					"repo must be owner/name, e.g. deligoez/tp",
				)
			}

			// Rule 3: no duplicate repos across entire manifest.
			if seen[entry.Repo] {
				return output.NewHintError(
					fmt.Errorf("manifest: duplicate repo %q", entry.Repo),
					fmt.Sprintf("remove duplicate entry for %s in manifest", entry.Repo),
				)
			}
			seen[entry.Repo] = true

			// Rule 7: install and update entries must be non-empty strings.
			for i, cmd := range entry.Install {
				if cmd == "" {
					return output.NewHintError(
						fmt.Errorf("manifest: repo %q has empty install entry at index %d", entry.Repo, i),
						"install entries must be non-empty command strings",
					)
				}
			}
			for i, cmd := range entry.Update {
				if cmd == "" {
					return output.NewHintError(
						fmt.Errorf("manifest: repo %q has empty update entry at index %d", entry.Repo, i),
						"update entries must be non-empty command strings",
					)
				}
			}
		}
	}

	return nil
}

// rawRepo represents a single repo entry as found in YAML.
type rawRepo struct {
	Repo    string   `yaml:"repo"`
	Install []string `yaml:"install"`
	Update  []string `yaml:"update"`
}

// Load reads a manifest YAML file from the given path and returns a parsed Manifest.
// Path resolution and validation are handled separately.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, output.NewHintError(
				fmt.Errorf("reading manifest: %w", err),
				"create manifest with: rp manifest init --dir ~/Developer",
			)
		}
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	// Step 1: Decode into raw yaml.Node.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, output.NewHintError(
			fmt.Errorf("parsing manifest YAML: %w", err),
			fmt.Sprintf("check manifest syntax at %s", path),
		)
	}

	// Step 2: Unwrap DocumentNode → Content[0].
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, output.NewHintError(
			fmt.Errorf("manifest is empty"),
			"run rp manifest init to generate one",
		)
	}
	root := doc.Content[0]

	// Step 3: Validate root is a MappingNode.
	if root.Kind != yaml.MappingNode {
		return nil, output.NewHintError(
			fmt.Errorf("manifest must be a YAML mapping at the top level"),
			"ensure manifest starts with key: value pairs, not a list",
		)
	}

	// Scan top-level keys: check for duplicates, non-scalar keys, and reserved 'owners' key.
	seenKeys := make(map[string]bool)
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("manifest: top-level key at line %d must be a string", keyNode.Line)
		}
		if seenKeys[keyNode.Value] {
			return nil, fmt.Errorf("manifest: duplicate key %q", keyNode.Value)
		}
		seenKeys[keyNode.Value] = true
	}

	// Step 4: Check for legacy 'owners' key — short-circuit with migration hint.
	if seenKeys["owners"] {
		return nil, output.NewHintError(
			fmt.Errorf("\"owners\" is no longer a valid manifest key"),
			"Remove the \"owners:\" line and dedent owner blocks by one level.",
		)
	}

	// Step 5: Extract base_dir.
	var baseDir string
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "base_dir" {
			baseDir = root.Content[i+1].Value
			break
		}
	}

	expandedBaseDir, err := expandTilde(baseDir)
	if err != nil {
		return nil, fmt.Errorf("expanding base_dir: %w", err)
	}

	m := &Manifest{
		BaseDir: expandedBaseDir,
	}

	// Steps 6-7: Iterate non-reserved top-level keys as owner names, in document order.
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]
		ownerName := keyNode.Value

		// Skip reserved keys.
		if ownerName == "base_dir" {
			continue
		}

		group, err := parseOwnerNode(ownerName, valNode)
		if err != nil {
			return nil, fmt.Errorf("owner %q: %w", ownerName, err)
		}
		m.owners = append(m.owners, group)
	}

	// Resolve local paths for every repo entry now that BaseDir is expanded.
	for i := range m.owners {
		owner := &m.owners[i]
		for j := range owner.Repos {
			entry := &owner.Repos[j]
			// repo field is "{github_owner}/{repo_name}"; we need just the repo_name part.
			repoName := entry.Repo
			if idx := strings.LastIndex(entry.Repo, "/"); idx >= 0 {
				repoName = entry.Repo[idx+1:]
			}
			entry.LocalPath = resolvePath(m.BaseDir, owner.Name, entry.Category, repoName)
		}
	}

	// Step 8: Validate.
	if err := m.validate(); err != nil {
		return nil, err
	}

	return m, nil
}

// parseOwnerNode parses a single owner's value node into an OwnerGroup.
// A SequenceNode means flat (repos listed directly), a MappingNode means categorized.
func parseOwnerNode(ownerName string, node *yaml.Node) (OwnerGroup, error) {
	group := OwnerGroup{Name: ownerName}

	switch node.Kind {
	case yaml.SequenceNode:
		// Flat owner: repos listed directly as a sequence.
		group.IsFlat = true
		// Check for legacy deps: key at yaml.Node level before decoding.
		for _, item := range node.Content {
			if item.Kind == yaml.MappingNode {
				if err := checkForDepsKey(item); err != nil {
					return group, err
				}
			}
		}
		var rawRepos []rawRepo
		if err := node.Decode(&rawRepos); err != nil {
			return group, fmt.Errorf("flat owner must be a list of repo entries: %w", err)
		}
		if len(rawRepos) == 0 {
			return group, output.NewHintError(
				fmt.Errorf("owner %q has an empty repo list", ownerName),
				"add at least one repo entry, or remove the owner",
			)
		}
		for _, r := range rawRepos {
			entry := RepoEntry{
				Repo:     r.Repo,
				Owner:    ownerName,
				Category: "",
				CloneURL: "git@github.com:" + r.Repo + ".git",
				Install:  r.Install,
				Update:   r.Update,
			}
			group.Repos = append(group.Repos, entry)
		}

	case yaml.MappingNode:
		// Categorized owner: keys are category names.
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			key := keyNode.Value

			// Check for legacy deps: key at yaml.Node level.
			if valNode.Kind == yaml.SequenceNode {
				for _, item := range valNode.Content {
					if item.Kind == yaml.MappingNode {
						if err := checkForDepsKey(item); err != nil {
							return group, err
						}
					}
				}
			}

			var rawRepos []rawRepo
			if err := valNode.Decode(&rawRepos); err != nil {
				return group, fmt.Errorf("category %q must be a list of repo entries: %w", key, err)
			}
			if len(rawRepos) == 0 {
				return group, output.NewHintError(
					fmt.Errorf("category %q has an empty repo list", key),
					"add at least one repo to category, or remove it",
				)
			}
			for _, r := range rawRepos {
				entry := RepoEntry{
					Repo:     r.Repo,
					Owner:    ownerName,
					Category: key,
					CloneURL: "git@github.com:" + r.Repo + ".git",
					Install:  r.Install,
					Update:   r.Update,
				}
				group.Repos = append(group.Repos, entry)
			}
		}

	default:
		return group, output.NewHintError(
			fmt.Errorf("owner %q must be a mapping (categorized) or sequence (flat), got YAML kind %d", ownerName, node.Kind),
			"use a mapping for categorized owners or a sequence for flat owners",
		)
	}

	return group, nil
}

// checkForDepsKey scans a repo mapping node for the removed "deps" key.
func checkForDepsKey(repoNode *yaml.Node) error {
	for i := 0; i+1 < len(repoNode.Content); i += 2 {
		if repoNode.Content[i].Value == "deps" {
			// Find repo name for error message.
			repoName := "unknown"
			for j := 0; j+1 < len(repoNode.Content); j += 2 {
				if repoNode.Content[j].Value == "repo" {
					repoName = repoNode.Content[j+1].Value
					break
				}
			}
			return output.NewHintError(
				fmt.Errorf("repo %q uses removed key \"deps\"", repoName),
				"Rename \"deps:\" to \"install:\" (and optionally add \"update:\").",
			)
		}
	}
	return nil
}
