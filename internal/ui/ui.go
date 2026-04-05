package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// noColor tracks whether color output is disabled.
var noColor bool

func init() {
	_, noColor = os.LookupEnv("NO_COLOR")
}

// SetNoColor enables or disables colored output at runtime (e.g. via --no-color flag).
func SetNoColor(v bool) {
	noColor = v
}

// styled returns s wrapped in the given lipgloss style, unless noColor is set.
func styled(s string, style lipgloss.Style) string {
	if noColor {
		return s
	}
	return style.Render(s)
}

var (
	styleOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // yellow
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // red
)

// Status symbols.
var (
	SymbolOK    = func() string { return styled("OK", styleOK) }
	SymbolWarn  = func() string { return styled("!!", styleWarn) }
	SymbolError = func() string { return styled("XX", styleErr) }
)

// Plural returns "<count> <word>" with a trailing "s" when count != 1.
// Examples: Plural(1, "commit") → "1 commit", Plural(3, "commit") → "3 commits".
func Plural(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

// SummaryLine returns a formatted summary block with a header followed by the
// provided parts joined by newlines.
func SummaryLine(parts ...string) string {
	return "-- Summary --\n" + strings.Join(parts, "\n")
}

// PadRight pads s with trailing spaces until it reaches width characters.
// If s is already at least width characters long it is returned unchanged.
func PadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
