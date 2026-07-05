package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/f64"
)

func getThumbnailTextColor(runtimeConfig *config.RuntimeConfig) color.RGBA {
	r, g, b := runtimeConfig.GetTextColor()
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

// GenerateThumbnail creates a YouTube thumbnail with the title text overlaid, at
// the same resolution as the video (1280x720).
func GenerateThumbnail(outputPath string, meta PodcastMeta, runtimeConfig *config.RuntimeConfig) error {
	thumbImg, err := loadThumbnailBackground(runtimeConfig)
	if err != nil {
		return fmt.Errorf("failed to load thumbnail background: %w", err)
	}

	fontData, err := embeddedAssets.ReadFile(config.ThumbnailFontAsset)
	if err != nil {
		return fmt.Errorf("failed to load bold font: %w", err)
	}

	parsedFont, err := truetype.Parse(fontData)
	if err != nil {
		return fmt.Errorf("failed to parse font: %w", err)
	}

	line1, line2 := splitTitle(meta.Title)
	fontSize := findOptimalFontSize(parsedFont, line1, line2)

	face := truetype.NewFace(parsedFont, &truetype.Options{
		Size: fontSize,
		DPI:  72,
	})
	defer face.Close()

	drawThumbnailText(thumbImg, face, line1, line2, runtimeConfig)

	if err := saveThumbnail(thumbImg, outputPath); err != nil {
		return fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return nil
}

func loadThumbnailBackground(runtimeConfig *config.RuntimeConfig) (*image.RGBA, error) {
	data, err := loadImageData(runtimeConfig.GetThumbnailImagePath())
	if err != nil {
		return nil, err
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	// Skip scaling when the source already matches the target resolution.
	bounds := img.Bounds()
	if bounds.Dx() == config.Width && bounds.Dy() == config.Height {
		rgba := image.NewRGBA(bounds)
		draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
		return rgba, nil
	}

	dst := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Src, nil)
	return dst, nil
}

func splitTitle(title string) (string, string) {
	words := strings.Fields(title)
	if len(words) == 0 {
		return "", ""
	}
	if len(words) == 1 {
		return words[0], ""
	}

	mid := len(words) / 2
	line1 := strings.Join(words[:mid], " ")
	line2 := strings.Join(words[mid:], " ")

	return line1, line2
}

// findOptimalFontSize finds the largest font size that fits within these constraints:
// - ThumbnailMargin from left and right edges
// - Line 1 starts at the top margin (ThumbnailMargin)
// - Line 2 bottom edge must not extend below the vertical centre line (Height/2)
func findOptimalFontSize(parsedFont *truetype.Font, line1, line2 string) float64 {
	centerY := config.Height / 2
	maxWidth := config.Width - (2 * config.ThumbnailMargin)

	for size := 150.0; size > 10.0; size -= 2.0 {
		face := truetype.NewFace(parsedFont, &truetype.Options{
			Size: size,
			DPI:  72,
		})

		width1, bounds1 := measureTextBounds(face, line1)
		width2, bounds2 := measureTextBounds(face, line2)

		face.Close()

		if width1 > maxWidth || width2 > maxWidth {
			continue
		}

		lineSpacing := int(size * 0.5)

		height1 := (bounds1.Max.Y - bounds1.Min.Y).Ceil()
		height2 := (bounds2.Max.Y - bounds2.Min.Y).Ceil()

		line2Bottom := config.ThumbnailMargin + height1 + lineSpacing + height2

		if line2Bottom <= centerY {
			return size
		}
	}

	return 10.0
}

// drawThumbnailText keeps the rotated text inside the top half of the thumbnail.
func drawThumbnailText(img *image.RGBA, face font.Face, line1, line2 string, runtimeConfig *config.RuntimeConfig) {
	// bounds.Min.Y is negative for ascent, and bounds.Max.Y is positive for descent.
	width1, bounds1 := measureTextBounds(face, line1)
	width2, bounds2 := measureTextBounds(face, line2)

	metrics := face.Metrics()
	fontSize := float64(metrics.Height) / 64.0 // Convert from fixed.Int26_6 to float64
	lineSpacing := int(fontSize * 0.5)

	height1 := (bounds1.Max.Y - bounds1.Min.Y).Ceil()
	height2 := (bounds2.Max.Y - bounds2.Min.Y).Ceil()

	maxWidth := width1
	maxWidth = max(maxWidth, width2)
	totalHeight := height1 + lineSpacing + height2

	tempSize := int(float64(maxWidth+totalHeight) * 1.5)
	tempImg := image.NewRGBA(image.Rect(0, 0, tempSize, tempSize))

	// The baseline is where DrawString draws. The visual top is baseline + bounds.Min.Y (Min.Y is negative).
	tempCenterY := tempSize / 2

	// Since visual top = baseline + bounds1.Min.Y, we get: baseline = visualTop - bounds1.Min.Y
	line1VisualTop := tempCenterY - totalHeight/2
	line1BaselineY := line1VisualTop - bounds1.Min.Y.Ceil()

	line2VisualTop := line1VisualTop + height1 + lineSpacing
	line2BaselineY := line2VisualTop - bounds2.Min.Y.Ceil()

	drawCenteredLineOnTemp(tempImg, face, line1, tempSize, line1BaselineY, runtimeConfig)
	drawCenteredLineOnTemp(tempImg, face, line2, tempSize, line2BaselineY, runtimeConfig)

	angle := -config.ThumbnailTextRotationDegrees * math.Pi / 180.0 // Negative for clockwise
	cos := math.Cos(angle)
	sin := math.Sin(angle)

	cx := float64(tempSize) / 2.0
	cy := float64(tempSize) / 2.0

	m := f64.Aff3{
		cos, -sin, cx - cos*cx + sin*cy,
		sin, cos, cy - sin*cx - cos*cy,
	}

	rotatedImg := image.NewRGBA(tempImg.Bounds())

	draw.BiLinear.Transform(rotatedImg, m, tempImg, tempImg.Bounds(), draw.Over, nil)

	// For a clockwise rotation, the highest point will be the top-right corner of line 1
	line1Top := float64(line1VisualTop)
	line1Right := cx + float64(width1)/2.0

	topRightX := line1Right - cx
	topRightY := line1Top - cy

	// For clockwise rotation: y' = x*sin + y*cos
	rotatedTopY := sin*topRightX + cos*topRightY

	highestPointY := rotatedTopY + cy

	destX := (config.Width - tempSize) / 2

	// highestPointY is the highest point of the rotated text in tempImg coordinates
	// We want this highest point to align with config.ThumbnailMargin in the final image
	// So: destY + highestPointY = config.ThumbnailMargin
	// Therefore: destY = config.ThumbnailMargin - highestPointY
	destY := int(float64(config.ThumbnailMargin) - highestPointY)

	destRect := image.Rect(destX, destY, destX+tempSize, destY+tempSize)
	draw.Draw(img, destRect, rotatedImg, image.Point{}, draw.Over)
}

func drawCenteredLineOnTemp(img *image.RGBA, face font.Face, text string, imgWidth, baselineY int, runtimeConfig *config.RuntimeConfig) {
	if text == "" {
		return
	}

	d := newTextDrawer(img, face, getThumbnailTextColor(runtimeConfig))
	bounds, _ := d.BoundString(text)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()

	x := (imgWidth - textWidth) / 2

	d.Dot = freetype.Pt(x, baselineY)
	d.DrawString(text)
}

func saveThumbnail(img *image.RGBA, outputPath string) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	return png.Encode(outFile, img)
}
