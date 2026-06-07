package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Video settings
const (
	Width  = 1280
	Height = 720
	FPS    = 30
)

// Audio settings
const (
	SampleRate = 44100
	FFTSize    = 2048
)

// Visualization settings
const (
	NumBars      = 64   // Number of bars
	BarWidth     = 12   // Width of each bar
	BarGap       = 8    // Gap between bars
	CenterGap    = 100  // Gap between top and bottom bar sections
	MaxBarHeight = 0.50 // Maximum bar height as fraction of available space
)

// Bar dynamics constants
const (
	Framerate = float64(FPS)

	// Auto-sensitivity adjustment constants
	// These control dynamic gain adjustment based on peak detection
	SensitivityDecay   = 0.985 // Multiplier when overshoot detected (1.5% reduction per frame)
	SensitivityGrowth  = 1.002 // Multiplier when no overshoot (0.2% increase per frame)
	SensitivityMin     = 0.05  // Minimum sensitivity floor
	SensitivityMax     = 2.0   // Maximum sensitivity ceiling
	OvershootThreshold = 1.0   // Threshold for soft knee compression
)

// Appearance - Visual styling configuration.
// Embedded assets live in internal/renderer/assets/. Runtime overrides for
// colours and image paths are applied via RuntimeConfig.
const (
	// Bar colors (RGB values for visualization bars)
	BarColorR = 164
	BarColorG = 0
	BarColorB = 0

	// Text/UI colors (RGB values for title overlay and framing lines)
	// Brand yellow #F8B31D - used for title text, framing lines, and thumbnail text
	TextColorR = 248
	TextColorG = 179
	TextColorB = 29

	// Embedded asset paths (relative to internal/renderer/assets/)
	// Background image: bg.png - scaled to video resolution (1280x720)
	// Thumbnail image: thumb.png - used as base for thumbnail generation
	BackgroundImageAsset = "assets/bg.png"
	ThumbnailImageAsset  = "assets/thumb.png"

	// Embedded font paths (relative to internal/renderer/assets/)
	// Video title font: Poppins-Regular.ttf - used for video overlay text
	// Thumbnail font: Poppins-Bold.ttf - used for thumbnail generation
	VideoTitleFontAsset = "assets/Poppins-Regular.ttf"
	ThumbnailFontAsset  = "assets/Poppins-Bold.ttf"

	// Thumbnail layout
	ThumbnailMargin              = 30  // Margin in pixels from edges for thumbnail text
	ThumbnailTextRotationDegrees = 3.0 // Rotation angle for thumbnail text (degrees, clockwise)

	// Video overlay
	FramingLineHeight = 4 // Height in pixels of framing lines above/below center gap
)

// OptionalColor is an RGB colour that records whether it was explicitly set.
// When Set is false the colour is treated as absent and defaults apply.
type OptionalColor struct {
	R, G, B uint8
	Set     bool
}

// RuntimeConfig holds optional runtime overrides for customization
// When fields are unset/empty, the defaults from constants above are used
type RuntimeConfig struct {
	// Optional colour overrides (apply only when Set is true)
	BarColor  OptionalColor
	TextColor OptionalColor

	// Optional image path overrides
	BackgroundImagePath string
	ThumbnailImagePath  string
}

// GetBarColor returns the bar color RGB values (uses override or default)
func (c *RuntimeConfig) GetBarColor() (r, g, b uint8) {
	if c.BarColor.Set {
		return c.BarColor.R, c.BarColor.G, c.BarColor.B
	}
	return BarColorR, BarColorG, BarColorB
}

// GetTextColor returns the text color RGB values (uses override or default)
func (c *RuntimeConfig) GetTextColor() (r, g, b uint8) {
	if c.TextColor.Set {
		return c.TextColor.R, c.TextColor.G, c.TextColor.B
	}
	return TextColorR, TextColorG, TextColorB
}

// GetBackgroundImagePath returns the background image path and whether it is a
// custom filesystem path (true) or the default embedded asset (false).
func (c *RuntimeConfig) GetBackgroundImagePath() (path string, isCustom bool) {
	if c.BackgroundImagePath != "" {
		return c.BackgroundImagePath, true
	}
	return BackgroundImageAsset, false
}

// GetThumbnailImagePath returns the thumbnail image path and whether it is a
// custom filesystem path (true) or the default embedded asset (false).
func (c *RuntimeConfig) GetThumbnailImagePath() (path string, isCustom bool) {
	if c.ThumbnailImagePath != "" {
		return c.ThumbnailImagePath, true
	}
	return ThumbnailImageAsset, false
}

// ParseHexColor parses a hex color string (#RRGGBB or RRGGBB) and returns RGB values
func ParseHexColor(hex string) (r, g, b uint8, err error) {
	// Remove leading # if present
	hex = strings.TrimPrefix(hex, "#")

	// Validate length
	if len(hex) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex color format: must be 6 characters (RRGGBB)")
	}

	// Parse RGB components
	var rgb uint64
	rgb, err = strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid hex color: %w", err)
	}

	r = uint8((rgb >> 16) & 0xFF)
	g = uint8((rgb >> 8) & 0xFF)
	b = uint8(rgb & 0xFF)

	return r, g, b, nil
}
