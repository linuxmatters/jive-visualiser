package cli

import (
	"regexp"
	"strings"
	"testing"
)

// ansiPattern matches SGR escape sequences so column offsets can be measured on
// the plain text the user sees.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// helpColumnOffset returns the rune offset of the help text on a rendered row,
// i.e. where the second column begins after the name column.
func helpColumnOffset(t *testing.T, line, help string) int {
	t.Helper()
	plain := stripANSI(line)
	idx := strings.Index(plain, help)
	if idx < 0 {
		t.Fatalf("help text %q not found in line %q", help, plain)
	}
	return idx
}

func TestFlagTableAlignsHelpColumn(t *testing.T) {
	flags := []flag{
		{flags: "-h, --help", help: "Show help."},
		{flags: "--this-is-a-very-long-flag-name=VALUE", help: "Long flag help."},
	}

	out := flagTable(flags)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(flags) {
		t.Fatalf("expected %d rendered rows, got %d:\n%s", len(flags), len(lines), out)
	}

	shortOffset := helpColumnOffset(t, lines[0], "Show help.")
	longOffset := helpColumnOffset(t, lines[1], "Long flag help.")

	if shortOffset != longOffset {
		t.Errorf("help column not aligned: short flag help at %d, long flag help at %d",
			shortOffset, longOffset)
	}
}

func TestFlagTableKeepsDefaultSuffix(t *testing.T) {
	flags := []flag{
		{flags: "--encoder=ENCODER", help: "Pick encoder.", defaultVal: "libx264"},
	}

	plain := stripANSI(flagTable(flags))
	if !strings.Contains(plain, "Pick encoder.") {
		t.Errorf("help text missing from output:\n%s", plain)
	}
	if !strings.Contains(plain, "(default: libx264)") {
		t.Errorf("default suffix missing from output:\n%s", plain)
	}
}

func TestArgumentTableAlignsHelpColumn(t *testing.T) {
	args := []argument{
		{name: "<in>", help: "Input file."},
		{name: "<a-much-longer-argument>", help: "Output file."},
	}

	out := argumentTable(args)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(args) {
		t.Fatalf("expected %d rendered rows, got %d:\n%s", len(args), len(lines), out)
	}

	first := helpColumnOffset(t, lines[0], "Input file.")
	second := helpColumnOffset(t, lines[1], "Output file.")

	if first != second {
		t.Errorf("help column not aligned: first arg help at %d, second arg help at %d",
			first, second)
	}
}
