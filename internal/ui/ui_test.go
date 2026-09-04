package ui

import (
	"strings"
	"testing"
)

// withColor turns styling on for one test and restores the previous setting
// afterwards, so tests do not leak state into each other.
func withColor(t *testing.T, enabled bool) {
	t.Helper()
	prev := noColor
	SetNoColor(!enabled)
	t.Cleanup(func() { noColor = prev })
}

func TestPluralUsesSingularForOne(t *testing.T) {
	if got, want := Plural(1, "commit"), "1 commit"; got != want {
		t.Errorf("Plural(1, %q) = %q, want %q", "commit", got, want)
	}
}

