package theme

import (
	"image/color"
	"testing"

	colorful "github.com/lucasb-eyer/go-colorful"
)

// hexOf converts a color.Color to its go-colorful hex form for comparison.
func hexOf(t *testing.T, c color.Color) string {
	t.Helper()
	cf, ok := colorful.MakeColor(c)
	if !ok {
		t.Fatalf("MakeColor failed for %v", c)
	}
	return cf.Hex()
}

// dist returns the CIE Lab distance between two colours, a perceptual measure of
// how far apart they look.
func dist(t *testing.T, a, b color.Color) float64 {
	t.Helper()
	ca, _ := colorful.MakeColor(a)
	cb, _ := colorful.MakeColor(b)
	return ca.DistanceLab(cb)
}

func TestFireSpectrumLength(t *testing.T) {
	if got := len(FireSpectrum); got != SpectrumStops {
		t.Errorf("FireSpectrum length = %d, want %d", got, SpectrumStops)
	}
	if SpectrumStops < 8 {
		t.Errorf("SpectrumStops = %d, want at least 8 (match or exceed the old ramp)", SpectrumStops)
	}
}

// TestFireSpectrumEndpoints asserts the ramp starts at the crimson end and ends
// at the yellow end. A Lab blend reproduces its endpoint base colours exactly, so
// the first and last stops match FireCrimson and FireYellow within a tight
// tolerance.
func TestFireSpectrumEndpoints(t *testing.T) {
	const tol = 1e-6

	first := FireSpectrum[0]
	last := FireSpectrum[len(FireSpectrum)-1]

	if d := dist(t, first, FireCrimson); d > tol {
		t.Errorf("first stop %s not crimson %s (Lab distance %g)", hexOf(t, first), hexOf(t, FireCrimson), d)
	}
	if d := dist(t, last, FireYellow); d > tol {
		t.Errorf("last stop %s not yellow %s (Lab distance %g)", hexOf(t, last), hexOf(t, FireYellow), d)
	}
}

// TestFireSpectrumIsRamp checks the ramp brightens monotonically from crimson to
// yellow, confirming the Lab blend ascends the four base colours in order rather
// than collapsing to a single hue.
func TestFireSpectrumIsRamp(t *testing.T) {
	lum := func(c color.Color) float64 {
		cf, _ := colorful.MakeColor(c)
		l, _, _ := cf.Lab()
		return l
	}
	prev := lum(FireSpectrum[0])
	for i := 1; i < len(FireSpectrum); i++ {
		cur := lum(FireSpectrum[i])
		if cur < prev-1e-9 {
			t.Errorf("luminance dropped at stop %d: %g < %g (ramp should brighten crimson→yellow)", i, cur, prev)
		}
		prev = cur
	}
}
