package manifest

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
