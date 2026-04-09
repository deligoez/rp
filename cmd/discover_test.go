package cmd

import "testing"

func TestFilterUntracked(t *testing.T) {
	// Helper to build ghRepo slices.
	repo := func(name string, fork, archived bool) ghRepo {
		return ghRepo{NameWithOwner: name, IsFork: fork, IsArchived: archived}
	}
	names := func(repos []ghRepo) []string {
		var out []string
		for _, r := range repos {
			out = append(out, r.NameWithOwner)
		}
		return out
	}

	t.Run("basic subtraction", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", false, false),
			repo("acme/b", false, false),
			repo("acme/c", false, false),
			repo("acme/d", false, false),
			repo("acme/e", false, false),
		}
		manifest := []string{"acme/a", "acme/b", "acme/c"}
		result := filterUntracked(remote, manifest, false, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 untracked, got %d: %v", len(result), names(result))
		}
	})

	t.Run("all tracked", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", false, false),
			repo("acme/b", false, false),
		}
		manifest := []string{"acme/a", "acme/b"}
		result := filterUntracked(remote, manifest, false, false)
		if len(result) != 0 {
			t.Fatalf("expected 0 untracked, got %d: %v", len(result), names(result))
		}
	})

	t.Run("fork exclusion", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", true, false),
			repo("acme/b", false, false),
		}
		result := filterUntracked(remote, nil, false, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 untracked, got %d: %v", len(result), names(result))
		}
		if result[0].NameWithOwner != "acme/b" {
			t.Fatalf("expected acme/b, got %s", result[0].NameWithOwner)
		}
	})

	t.Run("fork inclusion", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", true, false),
			repo("acme/b", false, false),
		}
		result := filterUntracked(remote, nil, true, false)
		if len(result) != 2 {
			t.Fatalf("expected 2 untracked, got %d: %v", len(result), names(result))
		}
	})

	t.Run("archived exclusion", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", false, true),
			repo("acme/b", false, false),
		}
		result := filterUntracked(remote, nil, false, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 untracked, got %d: %v", len(result), names(result))
		}
		if result[0].NameWithOwner != "acme/b" {
			t.Fatalf("expected acme/b, got %s", result[0].NameWithOwner)
		}
	})

	t.Run("archived inclusion", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", false, true),
			repo("acme/b", false, false),
		}
		result := filterUntracked(remote, nil, false, true)
		if len(result) != 2 {
			t.Fatalf("expected 2 untracked, got %d: %v", len(result), names(result))
		}
	})

	t.Run("case insensitive match", func(t *testing.T) {
		remote := []ghRepo{
			repo("Acme/Repo", false, false),
		}
		manifest := []string{"acme/repo"}
		result := filterUntracked(remote, manifest, false, false)
		if len(result) != 0 {
			t.Fatalf("expected 0 untracked (case-insensitive), got %d: %v", len(result), names(result))
		}
	})

	t.Run("deduplication", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", false, false),
			repo("acme/a", false, false),
			repo("Acme/A", false, false),
		}
		result := filterUntracked(remote, nil, false, false)
		if len(result) != 1 {
			t.Fatalf("expected 1 untracked (deduped), got %d: %v", len(result), names(result))
		}
	})

	t.Run("fork AND archived", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/both", true, true),
		}
		// Only --forks set, not --archived — should be excluded.
		result := filterUntracked(remote, nil, true, false)
		if len(result) != 0 {
			t.Fatalf("expected 0 (fork+archived with only --forks), got %d", len(result))
		}
		// Only --archived set, not --forks — should be excluded.
		result = filterUntracked(remote, nil, false, true)
		if len(result) != 0 {
			t.Fatalf("expected 0 (fork+archived with only --archived), got %d", len(result))
		}
		// Both set — should be included.
		result = filterUntracked(remote, nil, true, true)
		if len(result) != 1 {
			t.Fatalf("expected 1 (fork+archived with both flags), got %d", len(result))
		}
	})

	t.Run("empty manifest", func(t *testing.T) {
		remote := []ghRepo{
			repo("acme/a", false, false),
			repo("acme/b", false, false),
			repo("acme/c", false, false),
		}
		result := filterUntracked(remote, nil, false, false)
		if len(result) != 3 {
			t.Fatalf("expected 3 untracked (empty manifest), got %d: %v", len(result), names(result))
		}
	})
}

func TestMatchesDiscoverFilter(t *testing.T) {
	t.Run("owner prefix", func(t *testing.T) {
		if !matchesDiscoverFilter("acme/repo", []string{"acme/"}) {
			t.Fatal("expected acme/repo to match acme/ prefix")
		}
	})

	t.Run("exact match", func(t *testing.T) {
		if !matchesDiscoverFilter("acme/repo", []string{"acme/repo"}) {
			t.Fatal("expected exact match")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		if !matchesDiscoverFilter("Acme/Repo", []string{"acme/repo"}) {
			t.Fatal("expected case-insensitive match")
		}
	})

	t.Run("no match", func(t *testing.T) {
		if matchesDiscoverFilter("acme/repo", []string{"other/"}) {
			t.Fatal("expected no match")
		}
	})
}
