package config

import (
	"testing"
)

// TestParseHexColor_ValidInputs verifies that ParseHexColor correctly parses
// various valid hex colour formats, catching case sensitivity issues,
// prefix handling, and byte ordering bugs.
func TestParseHexColor_ValidInputs(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		wantR uint8
		wantG uint8
		wantB uint8
	}{
		// Uppercase without hash
		{
			name:  "FF0000 (uppercase red, no hash)",
			input: "FF0000",
			wantR: 255,
			wantG: 0,
			wantB: 0,
		},
		// Lowercase without hash
		{
			name:  "ff0000 (lowercase red, no hash)",
			input: "ff0000",
			wantR: 255,
			wantG: 0,
			wantB: 0,
		},
		// Uppercase with hash
		{
			name:  "#FF0000 (uppercase red, with hash)",
			input: "#FF0000",
			wantR: 255,
			wantG: 0,
			wantB: 0,
		},
		// Lowercase with hash
		{
			name:  "#ff0000 (lowercase red, with hash)",
			input: "#ff0000",
			wantR: 255,
			wantG: 0,
			wantB: 0,
		},
		// Mixed case
		{
			name:  "Ff00fF (mixed case magenta)",
			input: "Ff00fF",
			wantR: 255,
			wantG: 0,
			wantB: 255,
		},
		// Pure green
		{
			name:  "00FF00 (green)",
			input: "00FF00",
			wantR: 0,
			wantG: 255,
			wantB: 0,
		},
		// Pure blue
		{
			name:  "0000FF (blue)",
			input: "0000FF",
			wantR: 0,
			wantG: 0,
			wantB: 255,
		},
		// Black
		{
			name:  "000000 (black)",
			input: "000000",
			wantR: 0,
			wantG: 0,
			wantB: 0,
		},
		// White
		{
			name:  "FFFFFF (white)",
			input: "FFFFFF",
			wantR: 255,
			wantG: 255,
			wantB: 255,
		},
		// Gray
		{
			name:  "808080 (gray)",
			input: "808080",
			wantR: 128,
			wantG: 128,
			wantB: 128,
		},
		// Brand yellow from Linux Matters (#F8B31D)
		{
			name:  "F8B31D (brand yellow, no hash)",
			input: "F8B31D",
			wantR: 248,
			wantG: 179,
			wantB: 29,
		},
		// Brand yellow with hash
		{
			name:  "#F8B31D (brand yellow, with hash)",
			input: "#F8B31D",
			wantR: 248,
			wantG: 179,
			wantB: 29,
		},
		// Brand red (#A40000)
		{
			name:  "#A40000 (brand red)",
			input: "#A40000",
			wantR: 164,
			wantG: 0,
			wantB: 0,
		},
		// Low values
		{
			name:  "010203 (low values)",
			input: "010203",
			wantR: 1,
			wantG: 2,
			wantB: 3,
		},
		// High values
		{
			name:  "FDFEFF (high values)",
			input: "FDFEFF",
			wantR: 253,
			wantG: 254,
			wantB: 255,
		},
		// Mix with zeros and Fs
		{
			name:  "F0F0FF (alternating high/zero)",
			input: "F0F0FF",
			wantR: 240,
			wantG: 240,
			wantB: 255,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b, err := ParseHexColor(tc.input)
			if err != nil {
				t.Fatalf("ParseHexColor(%q) returned error: %v", tc.input, err)
			}

			if r != tc.wantR || g != tc.wantG || b != tc.wantB {
				t.Errorf("ParseHexColor(%q) = (%d, %d, %d), want (%d, %d, %d)",
					tc.input, r, g, b, tc.wantR, tc.wantG, tc.wantB)
			}
		})
	}
}

// TestParseHexColor_InvalidInputs verifies that ParseHexColor correctly
// rejects malformed input with appropriate errors.
func TestParseHexColor_InvalidInputs(t *testing.T) {
	testCases := []struct {
		name       string
		input      string
		shouldFail bool
	}{
		// Too short
		{
			name:       "FFF (too short, 3 chars)",
			input:      "FFF",
			shouldFail: true,
		},
		// Too short with hash
		{
			name:       "#FFF (too short with hash)",
			input:      "#FFF",
			shouldFail: true,
		},
		// Too long
		{
			name:       "FFFFFFF (too long)",
			input:      "FFFFFFF",
			shouldFail: true,
		},
		// Too long with hash
		{
			name:       "#FFFFFFF (too long with hash)",
			input:      "#FFFFFFF",
			shouldFail: true,
		},
		// Invalid hex characters
		{
			name:       "GGGGGG (invalid hex)",
			input:      "GGGGGG",
			shouldFail: true,
		},
		// Invalid hex with hash
		{
			name:       "#GGGGGG (invalid hex with hash)",
			input:      "#GGGGGG",
			shouldFail: true,
		},
		// Mixed valid and invalid
		{
			name:       "FF00GG (mixed valid/invalid)",
			input:      "FF00GG",
			shouldFail: true,
		},
		// Empty string
		{
			name:       "Empty string",
			input:      "",
			shouldFail: true,
		},
		// Just hash
		{
			name:       "# (just hash)",
			input:      "#",
			shouldFail: true,
		},
		// Spaces
		{
			name:       "FF 000 (spaces)",
			input:      "FF 000",
			shouldFail: true,
		},
		// Hash in middle
		{
			name:       "FF#000 (hash in middle)",
			input:      "FF#000",
			shouldFail: true,
		},
		// Double hash
		{
			name:       "##FF0000 (double hash)",
			input:      "##FF0000",
			shouldFail: true,
		},
		// Newline
		{
			name:       "FF0000\\n (with newline)",
			input:      "FF0000\n",
			shouldFail: true,
		},
		// Zero-length after hash
		{
			name:       "#FFFFFFFFFFFFFF (too long, multiple hashes become one)",
			input:      "#FFFFFFFFFFFFFF",
			shouldFail: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := ParseHexColor(tc.input)
			if tc.shouldFail {
				if err == nil {
					t.Errorf("ParseHexColor(%q) expected error, got nil", tc.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseHexColor(%q) returned unexpected error: %v", tc.input, err)
				}
			}
		})
	}
}

// TestParseHexColor_ByteOrder verifies correct byte ordering (R, G, B).
// This catches swaps like (B, G, R) or (G, R, B).
func TestParseHexColor_ByteOrder(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		// Each should have distinct values to catch any reordering
		wantR, wantG, wantB uint8
	}{
		{
			name:  "010203 (1, 2, 3)",
			input: "010203",
			wantR: 1,
			wantG: 2,
			wantB: 3,
		},
		{
			name:  "AABBCC (170, 187, 204)",
			input: "AABBCC",
			wantR: 0xAA,
			wantG: 0xBB,
			wantB: 0xCC,
		},
		{
			name:  "112233 (17, 34, 51)",
			input: "112233",
			wantR: 0x11,
			wantG: 0x22,
			wantB: 0x33,
		},
		{
			name:  "DDEEFF (222, 238, 255)",
			input: "DDEEFF",
			wantR: 0xDD,
			wantG: 0xEE,
			wantB: 0xFF,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b, err := ParseHexColor(tc.input)
			if err != nil {
				t.Fatalf("ParseHexColor(%q) returned error: %v", tc.input, err)
			}

			// Check each component individually to catch reorderings
			if r != tc.wantR {
				t.Errorf("Red channel: got %d (0x%02X), want %d (0x%02X)",
					r, r, tc.wantR, tc.wantR)
			}
			if g != tc.wantG {
				t.Errorf("Green channel: got %d (0x%02X), want %d (0x%02X)",
					g, g, tc.wantG, tc.wantG)
			}
			if b != tc.wantB {
				t.Errorf("Blue channel: got %d (0x%02X), want %d (0x%02X)",
					b, b, tc.wantB, tc.wantB)
			}
		})
	}
}

// TestRuntimeConfig_GetBarColor verifies that GetBarColor returns default
// values when optional fields are nil.
func TestRuntimeConfig_GetBarColor(t *testing.T) {
	testCases := []struct {
		name   string
		config *RuntimeConfig
		wantR  uint8
		wantG  uint8
		wantB  uint8
	}{
		{
			name:   "Nil config fields (use defaults)",
			config: &RuntimeConfig{},
			wantR:  BarColorR,
			wantG:  BarColorG,
			wantB:  BarColorB,
		},
		{
			name: "Unset colour (use defaults)",
			config: &RuntimeConfig{
				BarColor: OptionalColor{R: 100},
			},
			// Should use default since the colour is not marked Set
			wantR: BarColorR,
			wantG: BarColorG,
			wantB: BarColorB,
		},
		{
			name: "All custom values",
			config: &RuntimeConfig{
				BarColor: OptionalColor{R: 255, G: 128, B: 64, Set: true},
			},
			wantR: 255,
			wantG: 128,
			wantB: 64,
		},
		{
			name: "Custom with zeros",
			config: &RuntimeConfig{
				BarColor: OptionalColor{R: 0, G: 0, B: 255, Set: true},
			},
			wantR: 0,
			wantG: 0,
			wantB: 255,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b := tc.config.GetBarColor()
			if r != tc.wantR || g != tc.wantG || b != tc.wantB {
				t.Errorf("GetBarColor() = (%d, %d, %d), want (%d, %d, %d)",
					r, g, b, tc.wantR, tc.wantG, tc.wantB)
			}
		})
	}
}

// TestRuntimeConfig_GetTextColor verifies that GetTextColor returns default
// values when optional fields are nil.
func TestRuntimeConfig_GetTextColor(t *testing.T) {
	testCases := []struct {
		name   string
		config *RuntimeConfig
		wantR  uint8
		wantG  uint8
		wantB  uint8
	}{
		{
			name:   "Nil config fields (use defaults)",
			config: &RuntimeConfig{},
			wantR:  TextColorR,
			wantG:  TextColorG,
			wantB:  TextColorB,
		},
		{
			name: "All custom values",
			config: &RuntimeConfig{
				TextColor: OptionalColor{R: 200, G: 100, B: 50, Set: true},
			},
			wantR: 200,
			wantG: 100,
			wantB: 50,
		},
		{
			name: "Custom black",
			config: &RuntimeConfig{
				TextColor: OptionalColor{R: 0, G: 0, B: 0, Set: true},
			},
			wantR: 0,
			wantG: 0,
			wantB: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b := tc.config.GetTextColor()
			if r != tc.wantR || g != tc.wantG || b != tc.wantB {
				t.Errorf("GetTextColor() = (%d, %d, %d), want (%d, %d, %d)",
					r, g, b, tc.wantR, tc.wantG, tc.wantB)
			}
		})
	}
}

// TestRuntimeConfig_NilFields verifies that Get*() methods return defaults
// when optional fields are nil. This catches nil pointer dereferences in
// config access that could panic during rendering.
func TestRuntimeConfig_NilFields(t *testing.T) {
	tests := []struct {
		name     string
		config   *RuntimeConfig
		validate func(t *testing.T, c *RuntimeConfig)
	}{
		{
			name:   "Completely nil config",
			config: &RuntimeConfig{
				// All fields nil/empty
			},
			validate: func(t *testing.T, c *RuntimeConfig) {
				// GetBarColor should return defaults
				r, g, b := c.GetBarColor()
				if r != BarColorR || g != BarColorG || b != BarColorB {
					t.Errorf("GetBarColor() = (%d, %d, %d), want defaults (%d, %d, %d)",
						r, g, b, BarColorR, BarColorG, BarColorB)
				}

				// GetTextColor should return defaults
				r, g, b = c.GetTextColor()
				if r != TextColorR || g != TextColorG || b != TextColorB {
					t.Errorf("GetTextColor() = (%d, %d, %d), want defaults (%d, %d, %d)",
						r, g, b, TextColorR, TextColorG, TextColorB)
				}

				// GetBackgroundImagePath should return default asset
				path, isCustom := c.GetBackgroundImagePath()
				if path != BackgroundImageAsset || isCustom {
					t.Errorf("GetBackgroundImagePath() = (%q, %t), want (%q, false)", path, isCustom, BackgroundImageAsset)
				}

				// GetThumbnailImagePath should return default asset
				path, isCustom = c.GetThumbnailImagePath()
				if path != ThumbnailImageAsset || isCustom {
					t.Errorf("GetThumbnailImagePath() = (%q, %t), want (%q, false)", path, isCustom, ThumbnailImageAsset)
				}
			},
		},
		{
			name: "Unset bar color",
			config: &RuntimeConfig{
				BarColor: OptionalColor{R: 100, G: 50},
				// Set is false - colour should be treated as absent
			},
			validate: func(t *testing.T, c *RuntimeConfig) {
				// Should return defaults because the colour is not marked Set
				r, g, b := c.GetBarColor()
				if r != BarColorR || g != BarColorG || b != BarColorB {
					t.Errorf("Unset bar color = (%d, %d, %d), want defaults (%d, %d, %d)",
						r, g, b, BarColorR, BarColorG, BarColorB)
				}
			},
		},
		{
			name: "Unset text color",
			config: &RuntimeConfig{
				TextColor: OptionalColor{R: 200, B: 100},
				// Set is false - colour should be treated as absent
			},
			validate: func(t *testing.T, c *RuntimeConfig) {
				// Should return defaults because the colour is not marked Set
				r, g, b := c.GetTextColor()
				if r != TextColorR || g != TextColorG || b != TextColorB {
					t.Errorf("Unset text color = (%d, %d, %d), want defaults (%d, %d, %d)",
						r, g, b, TextColorR, TextColorG, TextColorB)
				}
			},
		},
		{
			name: "Empty image paths",
			config: &RuntimeConfig{
				BackgroundImagePath: "",
				ThumbnailImagePath:  "",
			},
			validate: func(t *testing.T, c *RuntimeConfig) {
				// Empty strings should trigger defaults
				bgPath, bgCustom := c.GetBackgroundImagePath()
				if bgPath != BackgroundImageAsset || bgCustom {
					t.Errorf("Empty background path = (%q, %t), want (%q, false)", bgPath, bgCustom, BackgroundImageAsset)
				}

				thumbPath, thumbCustom := c.GetThumbnailImagePath()
				if thumbPath != ThumbnailImageAsset || thumbCustom {
					t.Errorf("Empty thumbnail path = (%q, %t), want (%q, false)", thumbPath, thumbCustom, ThumbnailImageAsset)
				}
			},
		},
		{
			name: "All fields set - should use overrides",
			config: &RuntimeConfig{
				BarColor:            OptionalColor{R: 10, G: 20, B: 30, Set: true},
				TextColor:           OptionalColor{R: 40, G: 50, B: 60, Set: true},
				BackgroundImagePath: "/custom/bg.png",
				ThumbnailImagePath:  "/custom/thumb.png",
			},
			validate: func(t *testing.T, c *RuntimeConfig) {
				// GetBarColor should return overrides
				r, g, b := c.GetBarColor()
				if r != 10 || g != 20 || b != 30 {
					t.Errorf("GetBarColor() = (%d, %d, %d), want (10, 20, 30)", r, g, b)
				}

				// GetTextColor should return overrides
				r, g, b = c.GetTextColor()
				if r != 40 || g != 50 || b != 60 {
					t.Errorf("GetTextColor() = (%d, %d, %d), want (40, 50, 60)", r, g, b)
				}

				// GetBackgroundImagePath should return override
				path, isCustom := c.GetBackgroundImagePath()
				if path != "/custom/bg.png" || !isCustom {
					t.Errorf("GetBackgroundImagePath() = (%q, %t), want (/custom/bg.png, true)", path, isCustom)
				}

				// GetThumbnailImagePath should return override
				path, isCustom = c.GetThumbnailImagePath()
				if path != "/custom/thumb.png" || !isCustom {
					t.Errorf("GetThumbnailImagePath() = (%q, %t), want (/custom/thumb.png, true)", path, isCustom)
				}
			},
		},
		{
			name: "Mixed - only bar color set",
			config: &RuntimeConfig{
				BarColor: OptionalColor{R: 111, G: 222, B: 233, Set: true},
				// Text colour remains unset
			},
			validate: func(t *testing.T, c *RuntimeConfig) {
				// Bar color set should use overrides
				r, g, b := c.GetBarColor()
				if r != 111 || g != 222 || b != 233 {
					t.Errorf("GetBarColor() = (%d, %d, %d), want (111, 222, 233)", r, g, b)
				}

				// Text color nil should use defaults
				r, g, b = c.GetTextColor()
				if r != TextColorR || g != TextColorG || b != TextColorB {
					t.Errorf("GetTextColor() = (%d, %d, %d), want defaults (%d, %d, %d)",
						r, g, b, TextColorR, TextColorG, TextColorB)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config == nil {
				t.Fatal("test config is nil")
			}
			tt.validate(t, tt.config)
		})
	}
}
