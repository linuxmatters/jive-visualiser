package renderer

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
)

//go:embed assets/bg.png
//go:embed assets/thumb.png
//go:embed assets/Poppins-Regular.ttf
//go:embed assets/Poppins-Bold.ttf
var embeddedAssets embed.FS

// loadImageData reads image bytes from the filesystem when isCustom is true,
// otherwise from the embedded assets. In both cases path is the already-resolved
// location from RuntimeConfig.Get*ImagePath.
func loadImageData(path string, isCustom bool) ([]byte, error) {
	if isCustom {
		return os.ReadFile(path)
	}
	return embeddedAssets.ReadFile(path)
}

// LoadBackgroundImage loads and scales the background image (from custom path or embedded asset)
func LoadBackgroundImage(runtimeConfig *config.RuntimeConfig) (*image.RGBA, error) {
	data, err := loadImageData(runtimeConfig.GetBackgroundImagePath())
	if err != nil {
		return nil, err
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	// Convert to RGBA with proper scaling
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))

	// Use the fastest pure Go implementation according to speedtest-resize benchmarks
	draw.ApproxBiLinear.Scale(rgba, rgba.Bounds(), img, img.Bounds(), draw.Over, nil)

	return rgba, nil
}

// LoadFont loads the embedded TrueType font for video title overlay
func LoadFont(size float64) (font.Face, error) {
	fontBytes, err := embeddedAssets.ReadFile(config.VideoTitleFontAsset)
	if err != nil {
		return nil, err
	}

	f, err := truetype.Parse(fontBytes)
	if err != nil {
		return nil, err
	}

	face := truetype.NewFace(f, &truetype.Options{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})

	return face, nil
}

// DrawCenterText draws text centered horizontally at the specified Y position
func DrawCenterText(img *image.RGBA, face font.Face, text string, centerY int, textColor color.RGBA) {
	d := newTextDrawer(img, face, textColor)

	// Measure text dimensions
	bounds, _ := d.BoundString(text)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Calculate centered position (both horizontally and vertically)
	// Font baseline positioning means the Y coordinate is where the baseline sits.
	// To visually center text, we position baseline slightly below center.
	x := (config.Width - textWidth) / 2
	y := centerY + (textHeight / 3)

	d.Dot = freetype.Pt(x, y)
	d.DrawString(text)
}

// DrawEpisodeNumber draws the episode number in the top right corner
func DrawEpisodeNumber(img *image.RGBA, face font.Face, episodeNum string, textColor color.RGBA) {
	d := newTextDrawer(img, face, textColor)

	// Measure text dimensions
	bounds, _ := d.BoundString(episodeNum)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Position in top right corner, inset 30px from the edges
	offset := 30
	x := config.Width - textWidth - offset
	y := textHeight + offset

	d.Dot = freetype.Pt(x, y)
	d.DrawString(episodeNum)
}
