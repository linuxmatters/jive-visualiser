package ui

import (
	"strings"
	"testing"
)

// TestSparklineEmpty verifies an empty series renders nothing rather than
// panicking.
func TestSparklineEmpty(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("sparkline(nil) = %q, want empty", got)
	}
	if got := sparkline([]float64{}); got != "" {
		t.Errorf("sparkline([]) = %q, want empty", got)
	}
}

// TestSparklineMonotoneAscends verifies a strictly increasing series maps to
// non-decreasing block runes ending at the tallest block, proving the local
// min/max scaling spreads the trace across the ramp.
func TestSparklineMonotoneAscends(t *testing.T) {
	out := sparkline([]float64{1, 2, 3, 4, 5, 6, 7, 8})
	runes := []rune(out)
	if len(runes) != 8 {
		t.Fatalf("sparkline rune count = %d, want 8", len(runes))
	}

	idx := func(r rune) int {
		for i, b := range sparklineBlocks {
			if b == r {
				return i
			}
		}
		t.Fatalf("rune %q not a sparkline block", r)
		return -1
	}

	for i := 1; i < len(runes); i++ {
		if idx(runes[i]) < idx(runes[i-1]) {
			t.Errorf("sparkline not ascending at %d: %q then %q", i, runes[i-1], runes[i])
		}
	}
	if runes[0] != sparklineBlocks[0] {
		t.Errorf("first rune = %q, want lowest block %q", runes[0], sparklineBlocks[0])
	}
	if runes[len(runes)-1] != sparklineBlocks[len(sparklineBlocks)-1] {
		t.Errorf("last rune = %q, want highest block %q", runes[len(runes)-1], sparklineBlocks[len(sparklineBlocks)-1])
	}
}

// TestSparklineFlat verifies a flat series renders a uniform row without
// panicking or collapsing to blanks.
func TestSparklineFlat(t *testing.T) {
	out := sparkline([]float64{5, 5, 5, 5})
	runes := []rune(out)
	if len(runes) != 4 {
		t.Fatalf("flat sparkline rune count = %d, want 4", len(runes))
	}
	for i := 1; i < len(runes); i++ {
		if runes[i] != runes[0] {
			t.Errorf("flat sparkline not uniform: %q then %q", runes[0], runes[i])
		}
	}
	if strings.TrimSpace(out) == "" {
		t.Error("flat sparkline collapsed to blanks")
	}
}

// TestMeterFillBounds verifies the meter fills no cells at 0%, all cells at 100%,
// and a partial count in between, with a stable rendered width across fractions.
func TestMeterFillBounds(t *testing.T) {
	const barWidth = 10
	count := func(frac float64) (full, empty, width int) {
		out := stripStyles(meter("L", frac, barWidth, 4, "v"))
		full = strings.Count(out, "█")
		empty = strings.Count(out, "░")
		width = len([]rune(out))
		return
	}

	full0, empty0, w0 := count(0)
	if full0 != 0 || empty0 != barWidth {
		t.Errorf("0%% meter: full=%d empty=%d, want full=0 empty=%d", full0, empty0, barWidth)
	}

	full1, empty1, w1 := count(1)
	if full1 != barWidth || empty1 != 0 {
		t.Errorf("100%% meter: full=%d empty=%d, want full=%d empty=0", full1, empty1, barWidth)
	}

	fullM, emptyM, wM := count(0.5)
	if fullM <= 0 || fullM >= barWidth {
		t.Errorf("50%% meter: full=%d, want partial (0 < full < %d)", fullM, barWidth)
	}
	if fullM+emptyM != barWidth {
		t.Errorf("50%% meter: full+empty=%d, want %d", fullM+emptyM, barWidth)
	}

	if w0 != w1 || w0 != wM {
		t.Errorf("meter width unstable across fractions: %d, %d, %d", w0, w1, wM)
	}
}

// TestMeterClampsOutOfRange verifies fractions outside [0,1] clamp to empty/full
// rather than over- or under-filling.
func TestMeterClampsOutOfRange(t *testing.T) {
	const barWidth = 8
	lo := stripStyles(meter("L", -0.5, barWidth, 4, "v"))
	hi := stripStyles(meter("L", 1.5, barWidth, 4, "v"))
	if strings.Count(lo, "█") != 0 {
		t.Errorf("negative fraction filled cells: %q", lo)
	}
	if strings.Count(hi, "█") != barWidth {
		t.Errorf("over-unit fraction did not fill: %q", hi)
	}
}

// TestGaugeCardStableWidth verifies a card's rendered width is driven by its
// inner width and stays stable whether the value is short or overflows (it is
// truncated, not wrapped).
func TestGaugeCardStableWidth(t *testing.T) {
	short := gaugeCard("⏱", "Time", "1s", 12)
	long := gaugeCard("⏱", "Time", "this value is far too long to fit the card", 12)
	if ws, wl := maxLineWidth(short), maxLineWidth(long); ws != wl {
		t.Errorf("card width unstable: short=%d long=%d", ws, wl)
	}
	// A bordered card is four lines: top border, header, value, bottom border
	// (3 newlines). Truncating the value must not add a wrapped line.
	if lines := strings.Count(long, "\n"); lines != 3 {
		t.Errorf("card has %d newlines, want 3 (border+header+value+border, no wrap)", lines)
	}
}
