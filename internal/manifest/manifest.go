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
func resolvePath(baseDir, owner, category, repoName string, isFlat, isArchive bool) string {
	if isArchive {
		return filepath.Join(baseDir, owner, "archive", repoName)
	}
	if isFlat {
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
	IsArchive bool
	IsFlat    bool
	Deps      []string // shell commands from manifest
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
		return fmt.Errorf("manifest: at least one owner with at least one repo is required")
	}

	seen := make(map[string]bool)

	for _, owner := range m.owners {
		// Rule 4: owner names must be valid directory names.
		if !isValidName(owner.Name) {
			return fmt.Errorf("manifest: invalid owner name %q (must be non-empty, no '/', no '..', no null bytes)", owner.Name)
		}

		// Track category names seen for this owner to check rule 8.
		categoryRepos := make(map[string]int)
		for _, entry := range owner.Repos {
			if !entry.IsArchive {
				categoryRepos[entry.Category]++
			}
		}

		// Rule 8: categories must contain non-empty repo lists.
		for cat, count := range categoryRepos {
			if count == 0 {
				return fmt.Errorf("manifest: owner %q category %q has an empty repo list", owner.Name, cat)
			}
		}

		for _, entry := range owner.Repos {
			// Rule 4: category names must be valid directory names.
			// "archive" is allowed as a category (it is a reserved key that sets IsArchive).
			if !entry.IsArchive {
				if !isValidName(entry.Category) {
					return fmt.Errorf("manifest: owner %q has invalid category name %q (must be non-empty, no '/', no '..', no null bytes)", owner.Name, entry.Category)
				}
				// Rule 6: "flat" cannot be used as a category name.
				if entry.Category == "flat" {
					return fmt.Errorf("manifest: owner %q uses reserved key %q as a category name", owner.Name, entry.Category)
				}
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

			// Rule 7: deps values must be non-empty strings.
			for i, dep := range entry.Deps {
				if dep == "" {
					return fmt.Errorf("manifest: repo %q has an empty string at deps[%d]", entry.Repo, i)
				}
			}
		}
	}

	return nil
}

// rawManifest is used for initial YAML decoding of the top-level structure.
type rawManifest struct {
	BaseDir string    `yaml:"base_dir"`
	Owners  yaml.Node `yaml:"owners"`
}

// rawRepo represents a single repo entry as found in YAML.
type rawRepo struct {
	Repo string   `yaml:"repo"`
	Deps []string `yaml:"deps"`
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

	var raw rawManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, output.NewHintError(
			fmt.Errorf("parsing manifest YAML: %w", err),
			fmt.Sprintf("check manifest syntax at %s", path),
		)
	}

	// Expand tilde in base_dir before path resolution.
	expandedBaseDir, err := expandTilde(raw.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("expanding base_dir: %w", err)
	}

	m := &Manifest{
		BaseDir: expandedBaseDir,
	}

	if raw.Owners.Kind != 0 {
		owners, err := parseOwners(&raw.Owners)
		if err != nil {
			return nil, fmt.Errorf("parsing owners: %w", err)
		}
		m.owners = owners
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
			entry.LocalPath = resolvePath(m.BaseDir, owner.Name, entry.Category, repoName, entry.IsFlat, entry.IsArchive)
		}
	}

	if err := m.validate(); err != nil {
		return nil, err
	}

	return m, nil
}

// parseOwners iterates the yaml.Node for the "owners" mapping, preserving key order.
func parseOwners(node *yaml.Node) ([]OwnerGroup, error) {
	// Unwrap document node if present.
	n := node
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, nil
		}
		n = n.Content[0]
	}

	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("owners must be a mapping, got kind %d", n.Kind)
	}

	var groups []OwnerGroup

	// MappingNode Content is [key, value, key, value, ...]
	for i := 0; i+1 < len(n.Content); i += 2 {
		keyNode := n.Content[i]
		valNode := n.Content[i+1]

		ownerName := keyNode.Value

		group, err := parseOwnerNode(ownerName, valNode)
		if err != nil {
			return nil, fmt.Errorf("owner %q: %w", ownerName, err)
		}

		groups = append(groups, group)
	}

	return groups, nil
}

// parseOwnerNode parses a single owner's value node into an OwnerGroup.
// Keys are iterated in document order via yaml.Node to preserve YAML key order.
func parseOwnerNode(ownerName string, node *yaml.Node) (OwnerGroup, error) {
	group := OwnerGroup{Name: ownerName}

	if node.Kind != yaml.MappingNode {
		return group, fmt.Errorf("owner value must be a mapping, got kind %d", node.Kind)
	}

	// First pass: read "flat" so IsFlat is known when building repo entries.
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "flat" {
			valNode := node.Content[i+1]
			// Rule 6: flat must be a boolean. Check the YAML tag explicitly
			// because yaml.v3 coerces strings like "yes"/"no" to bool silently.
			if valNode.Kind != yaml.ScalarNode || (valNode.Tag != "!!bool" && valNode.Tag != "tag:yaml.org,2002:bool") {
				return group, fmt.Errorf("flat must be a boolean, got %q (tag: %s)", valNode.Value, valNode.Tag)
			}
			var flat bool
			if err := valNode.Decode(&flat); err != nil {
				return group, fmt.Errorf("flat must be a boolean: %w", err)
			}
			group.IsFlat = flat
			break
		}
	}

	// Second pass: iterate all keys in order to build repo entries.
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		key := keyNode.Value

		switch key {
		case "flat":
			// Already handled in first pass.
			continue

		case "archive":
			var rawRepos []rawRepo
			if err := valNode.Decode(&rawRepos); err != nil {
				return group, fmt.Errorf("archive must be a list of repo entries: %w", err)
			}
			for _, r := range rawRepos {
				entry := RepoEntry{
					Repo:      r.Repo,
					Owner:     ownerName,
					Category:  "archive",
					CloneURL:  "git@github.com:" + r.Repo + ".git",
					IsArchive: true,
					IsFlat:    group.IsFlat,
					Deps:      r.Deps,
					LocalPath: "",
				}
				group.Repos = append(group.Repos, entry)
			}

		default:
			// Treat as a category name.
			var rawRepos []rawRepo
			if err := valNode.Decode(&rawRepos); err != nil {
				return group, fmt.Errorf("category %q must be a list of repo entries: %w", key, err)
			}
			// Rule 8: categories must contain a non-empty list.
			if len(rawRepos) == 0 {
				return group, fmt.Errorf("category %q has an empty repo list", key)
			}
			for _, r := range rawRepos {
				entry := RepoEntry{
					Repo:      r.Repo,
					Owner:     ownerName,
					Category:  key,
					CloneURL:  "git@github.com:" + r.Repo + ".git",
					IsArchive: false,
					IsFlat:    group.IsFlat,
					Deps:      r.Deps,
					LocalPath: "",
				}
				group.Repos = append(group.Repos, entry)
			}
		}
	}

	return group, nil
}
