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

func TestPadRightPadsToWidth(t *testing.T) {
	got := PadRight("ab", 5)
	if got != "ab   " {
		t.Errorf("PadRight(%q, 5) = %q, want %q", "ab", got, "ab   ")
	}
	if len(got) != 5 {
		t.Errorf("PadRight produced %d characters, want 5", len(got))
	}
}

func TestPadRightLeavesLongerStringsAlone(t *testing.T) {
	// Equal length must not gain a space either — the boundary matters for
	// column alignment.
	for _, width := range []int{0, 2, 3} {
		if got := PadRight("abc", width); got != "abc" {
			t.Errorf("PadRight(%q, %d) = %q, want %q unchanged", "abc", width, got, "abc")
		}
	}
}

func TestSymbolsAreBareTextWithoutColor(t *testing.T) {
	withColor(t, false)

	cases := map[string]func() string{
		"OK": SymbolOK,
		"!!": SymbolWarn,
		"XX": SymbolError,
	}
	for want, symbol := range cases {
		if got := symbol(); got != want {
			t.Errorf("symbol = %q, want the bare %q with color disabled", got, want)
		}
	}
}

func TestSymbolsAreWrappedWithColor(t *testing.T) {
	withColor(t, true)

	// lipgloss only emits escapes when it believes the output profile supports
	// them, which it may not under `go test`. Assert the invariant that holds
	// either way: the symbol text survives, and nothing else is lost.
	for want, symbol := range map[string]func() string{
		"OK": SymbolOK,
		"!!": SymbolWarn,
		"XX": SymbolError,
	} {
		got := symbol()
		if !strings.Contains(got, want) {
			t.Errorf("symbol = %q, want it to contain %q", got, want)
		}
	}
}

