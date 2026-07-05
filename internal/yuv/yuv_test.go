package yuv

import (
	"image/color"
	"runtime"
	"sync"
	"testing"
)

// RGBToY/Cb/Cr are hand-rolled fixed-point BT.601 conversions with branchless
// chroma clamping. Pin them against the stdlib color.RGBToYCbCr, which uses the
// same coefficients, so an off-by-one in the clamp or a sign-extension slip in
// ^(c >> 31) cannot tint frames unnoticed.
//
// A ±1 tolerance is allowed because the rounding bias differs slightly: the
// stdlib adds half-LSB to all three components, while RGBToY here rounds with
// 1<<15 and the chroma paths add 257<<15. Both land within one code value of
// each other across the tested range.
const tolerance = 1

func diffWithin(a, b uint8) bool {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	return d <= tolerance
}

func addRangeCounts(t *testing.T, height int, counts []int, startY, endY int) {
	t.Helper()

	if startY < 0 || endY < startY || endY > height {
		t.Errorf("range %d:%d outside height %d", startY, endY, height)
		return
	}
	for y := startY; y < endY; y++ {
		counts[y]++
	}
}

func assertRowsCoveredOnce(t *testing.T, name string, counts []int) {
	t.Helper()

	for row, count := range counts {
		if count != 1 {
			t.Errorf("%s row %d processed %d times, want 1", name, row, count)
		}
	}
}

func rowCoverageHeights() []struct {
	name   string
	height int
} {
	numCPU := runtime.NumCPU()
	smallHeight := 1
	if numCPU > 1 {
		smallHeight = numCPU - 1
	}

	return []struct {
		name   string
		height int
	}{
		{"small", smallHeight},
		{"equal-to-CPU", numCPU},
		{"larger", numCPU*2 + 3},
	}
}

func TestParallelRows_partitionRowsCoverEveryRowOnce(t *testing.T) {
	for _, tc := range rowCoverageHeights() {
		t.Run(tc.name, func(t *testing.T) {
			counts := make([]int, tc.height)
			for _, r := range partitionRows(tc.height) {
				addRangeCounts(t, tc.height, counts, r.startY, r.endY)
			}
			assertRowsCoveredOnce(t, tc.name, counts)
		})
	}
}

func TestParallelRowsProcessesEveryRowOnce(t *testing.T) {
	for _, tc := range rowCoverageHeights() {
		t.Run(tc.name, func(t *testing.T) {
			counts := make([]int, tc.height)
			var mu sync.Mutex

			ParallelRows(tc.height, func(startY, endY int) {
				mu.Lock()
				defer mu.Unlock()

				addRangeCounts(t, tc.height, counts, startY, endY)
			})

			assertRowsCoveredOnce(t, tc.name, counts)
		})
	}
}

func TestRowPoolRunProcessesEveryRowOnce(t *testing.T) {
	for _, tc := range rowCoverageHeights() {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewRowPool(tc.height)
			defer pool.Close()

			counts := make([]int, tc.height)
			var mu sync.Mutex

			pool.Run(func(startY, endY int) {
				mu.Lock()
				defer mu.Unlock()

				addRangeCounts(t, tc.height, counts, startY, endY)
			})

			assertRowsCoveredOnce(t, tc.name, counts)
		})
	}
}

func TestRGBToYCbCr_AgainstStdlib(t *testing.T) {
	cases := []struct {
		name    string
		r, g, b uint8
	}{
		{"black", 0, 0, 0},
		{"white", 255, 255, 255},
		{"red", 255, 0, 0},
		{"green", 0, 255, 0},
		{"blue", 0, 0, 255},
		{"yellow", 255, 255, 0},
		{"cyan", 0, 255, 255},
		{"magenta", 255, 0, 255},
		{"mid-grey", 128, 128, 128},
		{"warm", 200, 100, 50},
		{"cool", 30, 90, 170},
		{"near-black", 1, 2, 3},
		{"near-white", 254, 253, 252},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantY, wantCb, wantCr := color.RGBToYCbCr(c.r, c.g, c.b)

			gotY := RGBToY(int32(c.r), int32(c.g), int32(c.b))
			gotCb := RGBToCb(int32(c.r), int32(c.g), int32(c.b))
			gotCr := RGBToCr(int32(c.r), int32(c.g), int32(c.b))

			if !diffWithin(gotY, wantY) {
				t.Errorf("RGBToY(%d,%d,%d) = %d, want %d (±%d)", c.r, c.g, c.b, gotY, wantY, tolerance)
			}
			if !diffWithin(gotCb, wantCb) {
				t.Errorf("RGBToCb(%d,%d,%d) = %d, want %d (±%d)", c.r, c.g, c.b, gotCb, wantCb, tolerance)
			}
			if !diffWithin(gotCr, wantCr) {
				t.Errorf("RGBToCr(%d,%d,%d) = %d, want %d (±%d)", c.r, c.g, c.b, gotCr, wantCr, tolerance)
			}
		})
	}
}

// TestRGBToYCbCr_ChromaClampBoundaries exercises the inputs that drive chroma to
// its extremes, where the branchless ^(c >> 31) clamp must pin to 0 or 255. Pure
// blue maximises Cb; pure red maximises Cr.
func TestRGBToYCbCr_ChromaClampBoundaries(t *testing.T) {
	if got := RGBToCb(0, 0, 255); got != 255 {
		t.Errorf("RGBToCb(0,0,255) = %d, want 255 (Cb max)", got)
	}
	if got := RGBToCr(255, 0, 0); got != 255 {
		t.Errorf("RGBToCr(255,0,0) = %d, want 255 (Cr max)", got)
	}
	// Components stay inside the valid 0-255 byte range for the colour cube
	// corners, so the clamp never under- or overflows.
	for r := 0; r <= 255; r += 51 {
		for g := 0; g <= 255; g += 51 {
			for b := 0; b <= 255; b += 51 {
				wantY, wantCb, wantCr := color.RGBToYCbCr(uint8(r), uint8(g), uint8(b))
				if !diffWithin(RGBToY(int32(r), int32(g), int32(b)), wantY) {
					t.Errorf("RGBToY(%d,%d,%d) out of tolerance", r, g, b)
				}
				if !diffWithin(RGBToCb(int32(r), int32(g), int32(b)), wantCb) {
					t.Errorf("RGBToCb(%d,%d,%d) out of tolerance", r, g, b)
				}
				if !diffWithin(RGBToCr(int32(r), int32(g), int32(b)), wantCr) {
					t.Errorf("RGBToCr(%d,%d,%d) out of tolerance", r, g, b)
				}
			}
		}
	}
}
