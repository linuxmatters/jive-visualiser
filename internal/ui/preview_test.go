package ui

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestDownsampleFrameOneByOneInput(t *testing.T) {
	want := color.RGBA{R: 12, G: 34, B: 56, A: 255}
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, want)

	preview := DownsampleFrame(frame, PreviewConfig{Width: 3, Height: 2})
	if len(preview) != 2 {
		t.Fatalf("preview height = %d, want 2", len(preview))
	}

	for y, row := range preview {
		if len(row) != 3 {
			t.Fatalf("preview row %d width = %d, want 3", y, len(row))
		}
		for x, got := range row {
			if got != want {
				t.Errorf("preview[%d][%d] = %#v, want %#v", y, x, got, want)
			}
		}
	}
}

func TestDownsampleFrameAveragesColourOutput(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	frame.SetRGBA(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	frame.SetRGBA(1, 0, color.RGBA{R: 20, G: 30, B: 40, A: 255})
	frame.SetRGBA(0, 1, color.RGBA{R: 30, G: 40, B: 50, A: 255})
	frame.SetRGBA(1, 1, color.RGBA{R: 40, G: 50, B: 60, A: 255})

	preview := DownsampleFrame(frame, PreviewConfig{Width: 1, Height: 1})
	if len(preview) != 1 {
		t.Fatalf("preview height = %d, want 1", len(preview))
	}
	if len(preview[0]) != 1 {
		t.Fatalf("preview width = %d, want 1", len(preview[0]))
	}

	want := color.RGBA{R: 25, G: 35, B: 45, A: 255}
	if got := preview[0][0]; got != want {
		t.Errorf("averaged colour = %#v, want %#v", got, want)
	}
}

func TestDownsampleFrameEmptyInputs(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))

	tests := []struct {
		name   string
		frame  *image.RGBA
		config PreviewConfig
	}{
		{name: "nil input", frame: nil, config: PreviewConfig{Width: 1, Height: 1}},
		{name: "zero width", frame: frame, config: PreviewConfig{Width: 0, Height: 1}},
		{name: "zero height", frame: frame, config: PreviewConfig{Width: 1, Height: 0}},
		{name: "negative width", frame: frame, config: PreviewConfig{Width: -1, Height: 1}},
		{name: "negative height", frame: frame, config: PreviewConfig{Width: 1, Height: -1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DownsampleFrame(tc.frame, tc.config); len(got) != 0 {
				t.Errorf("preview length = %d, want 0", len(got))
			}
		})
	}
}

func TestRenderPreviewBorderDimensions(t *testing.T) {
	preview := [][]color.RGBA{
		{
			{R: 10, G: 20, B: 30, A: 255},
			{R: 40, G: 50, B: 60, A: 255},
			{R: 70, G: 80, B: 90, A: 255},
		},
		{
			{R: 90, G: 80, B: 70, A: 255},
			{R: 60, G: 50, B: 40, A: 255},
			{R: 30, G: 20, B: 10, A: 255},
		},
	}

	lines := strings.Split(strings.TrimPrefix(stripStyles(RenderPreview(preview)), "\n"), "\n")
	wantLines := len(preview) + 2
	if len(lines) != wantLines {
		t.Fatalf("line count = %d, want %d", len(lines), wantLines)
	}

	wantWidth := len(preview[0]) + 2
	for i, line := range lines {
		if width := len([]rune(line)); width != wantWidth {
			t.Errorf("line %d width = %d, want %d: %q", i, width, wantWidth, line)
		}
	}
}
