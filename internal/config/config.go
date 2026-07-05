package config

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	Width  = 1280
	Height = 720
	FPS    = 30
)

const (
	// SampleRate is the reference/default audio sample rate in Hz. The encoder
	// and per-frame sample budget use the input file's actual rate
	// (reader.SampleRate()); this constant is a documented default used for
	// synthetic test signals and as a fallback reference.
	SampleRate = 44100
	FFTSize    = 2048 // Power of two for a fast transform.
)

const (
	NumBars      = 64
	BarWidth     = 12
	BarGap       = 8
	CenterGap    = 100
	MaxBarHeight = 0.50
)

const (
	SensitivityDecay   = 0.985 // Multiplier when overshoot detected (1.5% reduction per frame)
	SensitivityGrowth  = 1.002 // Multiplier when no overshoot (0.2% increase per frame)
	SensitivityMin     = 0.05
	SensitivityMax     = 2.0
	OvershootThreshold = 1.0 // Threshold for soft knee compression
)

// Embedded assets live in internal/renderer/assets/. Runtime overrides for
// colours and image paths are applied via RuntimeConfig.
const (
	// Bar colour, brand red #A40000 (RGB values for visualisation bars)
	BarColorR = 164
	BarColorG = 0
	BarColorB = 0

	// Text/UI colour, brand yellow #F8B31D. Used for title text, framing lines,
	// and thumbnail text.
	TextColorR = 248
	TextColorG = 179
	TextColorB = 29

	// Embedded asset paths are relative to internal/renderer/assets/.
	BackgroundImageAsset = "assets/bg.png"
	ThumbnailImageAsset  = "assets/thumb.png"

	VideoTitleFontAsset = "assets/Poppins-Regular.ttf"
	ThumbnailFontAsset  = "assets/Poppins-Bold.ttf"

	ThumbnailMargin              = 30
	ThumbnailTextRotationDegrees = 3.0

	FramingLineHeight = 4
)

// OptionalColor is an RGB colour that records whether it was explicitly set.
// When Set is false the colour is treated as absent and defaults apply.
type OptionalColor struct {
	R, G, B uint8
	Set     bool
}

// RuntimeConfig holds optional runtime overrides for customisation.
// When fields are unset or empty, the defaults from the constants above are used.
type RuntimeConfig struct {
	BarColor  OptionalColor
	TextColor OptionalColor

	BackgroundImagePath string
	ThumbnailImagePath  string
}

// GetBarColor returns the bar colour RGB values, using the override when set or the default otherwise.
func (c *RuntimeConfig) GetBarColor() (r, g, b uint8) {
	if c.BarColor.Set {
		return c.BarColor.R, c.BarColor.G, c.BarColor.B
	}
	return BarColorR, BarColorG, BarColorB
}

// GetTextColor returns the text colour RGB values, using the override when set or the default otherwise.
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

// ParseHexColor parses a hex colour string (#RRGGBB or RRGGBB) and returns RGB values.
func ParseHexColor(hex string) (r, g, b uint8, err error) {
	hex = strings.TrimPrefix(hex, "#")

	if len(hex) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex color format: must be 6 characters (RRGGBB)")
	}

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
