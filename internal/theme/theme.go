package theme

import "github.com/charmbracelet/lipgloss"

// Fire colour palette
// Shared fire theme colours for consistent branding across CLI and TUI.
var (
	// Core fire colours (dark to bright)
	FireYellow  = lipgloss.Color("#FFD700") // Bright yellow
	FireOrange  = lipgloss.Color("#FF8C00") // Deep orange
	FireRed     = lipgloss.Color("#FF4500") // Orange-red
	FireCrimson = lipgloss.Color("#DC143C") // Deep crimson

	// Accent colours
	WarmGray = lipgloss.Color("#B8860B") // Dark goldenrod for subtle text

	// CLI output colours
	JivefireRed = lipgloss.Color("#A40000") // Deep Jivefire red for titles and errors
	GoldOrange  = lipgloss.Color("#FFA500") // Orange-gold for section headers
	NeonYellow  = lipgloss.Color("#FFFF00") // Bright yellow for highlighted values
	NeutralGray = lipgloss.Color("#888888") // Neutral grey for keys and labels
	BrightWhite = lipgloss.Color("#FFFFFF") // White for emphasised values
)
