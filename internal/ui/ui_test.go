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

func TestPluralAddsSuffixForEveryOtherCount(t *testing.T) {
	cases := map[int]string{
		0:  "0 commits",
		2:  "2 commits",
		42: "42 commits",
		-1: "-1 commits",
	}
	for count, want := range cases {
		if got := Plural(count, "commit"); got != want {
			t.Errorf("Plural(%d, %q) = %q, want %q", count, "commit", got, want)
		}
	}
}

func TestSummaryLineJoinsPartsUnderHeader(t *testing.T) {
	got := SummaryLine("3 OK", "1 failed")
	want := "-- Summary --\n3 OK\n1 failed"
	if got != want {
		t.Errorf("SummaryLine = %q, want %q", got, want)
	}
}

func TestSummaryLineWithNoParts(t *testing.T) {
	if got, want := SummaryLine(), "-- Summary --\n"; got != want {
		t.Errorf("SummaryLine() = %q, want %q", got, want)
	}
}

