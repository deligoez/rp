package manifest

import "strings"

// FilterRepos filters repos based on the given filter strings.
// Filter syntax:
//   - "deligoez/tp" (contains exactly one / with non-empty parts) = exact repo match
//   - "deligoez/" (trailing /) = owner prefix match
//   - "deligoez" (no /) = owner prefix match (same as "deligoez/")
//
// Multiple filters = union of matches.
// Empty filters slice returns all repos unchanged.
func FilterRepos(repos []RepoEntry, filters []string) []RepoEntry {
	if len(filters) == 0 {
		return repos
	}

	var result []RepoEntry
	for _, repo := range repos {
		if matchesAnyFilter(repo, filters) {
			result = append(result, repo)
		}
	}
	if result == nil {
		result = make([]RepoEntry, 0)
	}
	return result
}

// matchesAnyFilter returns true if repo matches at least one of the given filters.
func matchesAnyFilter(repo RepoEntry, filters []string) bool {
	for _, f := range filters {
		if matchesFilter(repo, f) {
			return true
		}
	}
	return false
}

// matchesFilter checks whether a single filter matches the given RepoEntry.
// A filter with exactly one "/" and non-empty parts on both sides is an exact
// repo match (matched against repo.Repo). Everything else is treated as an
// owner prefix match against repo.Owner.
func matchesFilter(repo RepoEntry, filter string) bool {
	parts := strings.SplitN(filter, "/", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		// Exact repo match: "owner/name"
		return repo.Repo == filter
	}
	// Owner prefix match: "owner" or "owner/"
	owner := strings.TrimSuffix(filter, "/")
	return repo.Owner == owner
}

// FilterOwners filters OwnerGroups, keeping only repos that match the given filters.
// Owner groups with no matching repos are omitted from the result.
// Empty filters slice returns all owners unchanged.
func FilterOwners(owners []OwnerGroup, filters []string) []OwnerGroup {
	if len(filters) == 0 {
		return owners
	}
	var result []OwnerGroup
	for _, og := range owners {
		filtered := FilterRepos(og.Repos, filters)
		if len(filtered) > 0 {
			result = append(result, OwnerGroup{Name: og.Name, IsFlat: og.IsFlat, Repos: filtered})
		}
	}
	return result
}
