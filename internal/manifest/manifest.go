package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	var raw rawManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing manifest YAML: %w", err)
	}

	m := &Manifest{
		BaseDir: raw.BaseDir,
	}

	if raw.Owners.Kind != 0 {
		owners, err := parseOwners(&raw.Owners)
		if err != nil {
			return nil, fmt.Errorf("parsing owners: %w", err)
		}
		m.owners = owners
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
			var flat bool
			if err := node.Content[i+1].Decode(&flat); err != nil {
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
