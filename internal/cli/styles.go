package cli

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jive-visualiser/internal/theme"
)

// Shared Lipgloss styles for CLI output.
var (
	// TitleStyle renders the bold red banner heading.
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.JiveRed).
			MarginBottom(1)

	// HeaderStyle renders a section header.
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.GoldOrange).
			MarginTop(1).
			MarginBottom(1)

	// ErrorStyle renders an error message.
	ErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.JiveRed)

	// WarningStyle renders a warning message.
	WarningStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.GoldOrange)

	// HighlightStyle emphasises important values.
	HighlightStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.NeonYellow)

	// KeyStyle renders the key of a key-value pair.
	KeyStyle = lipgloss.NewStyle().
			Foreground(theme.NeutralGray)

	// ValueStyle renders the value of a key-value pair.
	ValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.BrightWhite)
)

// PrintVersion prints version information
func PrintVersion(version string) {
	fmt.Println(TitleStyle.Render("Jive Visualiser 🔥"))
	fmt.Printf("%s %s\n", KeyStyle.Render("Version:"), ValueStyle.Render(version))
}

// EncoderInfo holds one hardware encoder's details for the probe display.
type EncoderInfo struct {
	Name        string
	Description string
	Available   bool
}

// PrintHardwareProbe prints a styled hardware encoder probe result
func PrintHardwareProbe(encoders []EncoderInfo) {
	fmt.Println(TitleStyle.Render("Jive Visualiser 🔥"))
	fmt.Println(HeaderStyle.Render("Hardware Encoder Probe"))

	for _, enc := range encoders {
		var status string
		if enc.Available {
			status = HighlightStyle.Render("✓ available")
		} else {
			status = ErrorStyle.Render("✗ not available")
		}
		fmt.Printf("  %s (%s): %s\n",
			ValueStyle.Render(enc.Description),
			KeyStyle.Render(enc.Name),
			status)
	}
	fmt.Println()
}

// PrintError prints an error message
func PrintError(message string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", ErrorStyle.Render("Error:"), message)
}

// PrintWarning prints a warning message
func PrintWarning(message string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", WarningStyle.Render("Warning:"), message)
}
