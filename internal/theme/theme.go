package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"
)

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

// SpectrumStops is the number of colour stops in the fire spectrum ramp. Sixteen
// stops give a smoother gradient than the four base colours alone while keeping
// the per-cell colour-index mapping cheap.
const SpectrumStops = 16

// FireSpectrum is the precomputed fire colour ramp, crimson (cold) → yellow
// (hot), blended in CIE Lab space from the four base theme colours. theme is the
// single source of truth for this ramp; the TUI spectrum references it directly.
// Built once at package load so the render hot loop never blends colours.
var FireSpectrum = buildFireSpectrum(SpectrumStops)

// buildFireSpectrum generates an n-stop fire colour ramp by interpolating in CIE
// Lab space across the four base theme colours (FireCrimson → FireRed →
// FireOrange → FireYellow). Lab blending keeps perceived brightness even across
// the ramp, avoiding the muddy midpoints of naive RGB interpolation. The result
// starts at crimson and ends at yellow.
func buildFireSpectrum(n int) []color.Color {
	bases := []color.Color{FireCrimson, FireRed, FireOrange, FireYellow}

	stops := make([]colorful.Color, len(bases))
	for i, c := range bases {
		// lipgloss hex colours are opaque, so MakeColor always succeeds; fall back
		// to black on the impossible transparent case to keep the ramp total.
		cf, ok := colorful.MakeColor(c)
		if !ok {
			cf = colorful.Color{}
		}
		stops[i] = cf
	}

	if n < 1 {
		n = 1
	}

	ramp := make([]color.Color, n)
	segments := len(stops) - 1
	for i := range ramp {
		// Map this stop's position (0..1) onto the multi-segment base ramp, then
		// Lab-blend between the two bracketing base colours.
		pos := 0.0
		if n > 1 {
			pos = float64(i) / float64(n-1)
		}
		scaled := pos * float64(segments)
		seg := int(scaled)
		if seg >= segments {
			seg = segments - 1
		}
		t := scaled - float64(seg)
		ramp[i] = stops[seg].BlendLab(stops[seg+1], t).Clamped()
	}
	return ramp
}
