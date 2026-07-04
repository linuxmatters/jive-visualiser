// Package yuv holds the shared RGB→YCbCr conversion primitives used by both the
// encoder hot path and the standalone bench-yuv tool.
package yuv

import (
	"runtime"
	"sync"
)

// YCbCr coefficients from Go's color/ycbcr.go (full-range JFIF BT.601).
// Full-range means Y, Cb and Cr span the whole 0-255 byte range, so there is no
// studio-swing headroom or footroom. The values are the real coefficients scaled
// by 65536, letting the conversion run in integer arithmetic and recover the
// result with a single >>16 shift instead of float multiplies on the hot path.
const (
	// Y coefficients (sum = 65536)
	YR = 19595 // 0.299 * 65536
	YG = 38470 // 0.587 * 65536
	YB = 7471  // 0.114 * 65536

	// Cb coefficients (sum = 0)
	CbR = -11056 // -0.16874 * 65536
	CbG = -21712 // -0.33126 * 65536
	CbB = 32768  //  0.50000 * 65536

	// Cr coefficients (sum = 0)
	CrR = 32768  //  0.50000 * 65536
	CrG = -27440 // -0.41869 * 65536
	CrB = -5328  // -0.08131 * 65536
)

// RGBToY converts RGB to Y (luma) component.
func RGBToY(r, g, b int32) uint8 {
	return uint8((YR*r + YG*g + YB*b + 1<<15) >> 16) //nolint:gosec // result is clamped to 0-255
}

// RGBToCb converts RGB to Cb (blue-difference chroma) with a branchless clamp.
//
// The 257<<15 bias centres chroma on 128 and adds the half-LSB rounding term. A
// valid result occupies the low 24 bits, so a set top byte means the value fell
// outside 0-255: ^(cb >> 31) then fills the byte with 0 for a negative overflow
// or 255 for a positive one, dodging a compare-and-branch on the hot path.
func RGBToCb(r, g, b int32) uint8 {
	cb := CbR*r + CbG*g + CbB*b + 257<<15
	if uint32(cb)&0xff000000 == 0 { //nolint:gosec // intentional bit manipulation
		cb >>= 16
	} else {
		cb = ^(cb >> 31)
	}
	return uint8(cb) //nolint:gosec // value is clamped by branch above
}

// RGBToCr converts RGB to Cr (red-difference chroma) with a branchless clamp.
// The clamp works exactly as in RGBToCb.
func RGBToCr(r, g, b int32) uint8 {
	cr := CrR*r + CrG*g + CrB*b + 257<<15
	if uint32(cr)&0xff000000 == 0 { //nolint:gosec // intentional bit manipulation
		cr >>= 16
	} else {
		cr = ^(cr >> 31)
	}
	return uint8(cr) //nolint:gosec // value is clamped by branch above
}

// rowRange is a precomputed row partition reused across every frame.
type rowRange struct {
	startY, endY int
}

// partitionRows splits height rows across one worker per CPU. Each worker takes
// an even slice of rowsPerWorker and the last worker absorbs the remainder, so
// every row is covered exactly once with no gaps or overlap. When there are more
// CPUs than rows, rowsPerWorker floors to 0, so the fallback pins it to one row
// per worker and caps numCPU at height to avoid empty trailing ranges.
// ParallelRows and RowPool share this split, keeping their output identical.
func partitionRows(height int) []rowRange {
	numCPU := runtime.NumCPU()
	rowsPerWorker := height / numCPU
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		numCPU = height
	}

	ranges := make([]rowRange, numCPU)
	for worker := range numCPU {
		startY := worker * rowsPerWorker
		endY := startY + rowsPerWorker
		if worker == numCPU-1 {
			endY = height
		}
		ranges[worker] = rowRange{startY: startY, endY: endY}
	}
	return ranges
}

// ParallelRows executes fn across height rows using all CPU cores.
//
// This spawns goroutines per call. For the per-frame encoder hot path use a
// RowPool (NewRowPool) instead, which reuses long-lived workers.
func ParallelRows(height int, fn func(startY, endY int)) {
	ranges := partitionRows(height)

	var wg sync.WaitGroup
	wg.Add(len(ranges))

	for _, r := range ranges {
		go func(startY, endY int) {
			defer wg.Done()
			fn(startY, endY)
		}(r.startY, r.endY)
	}

	wg.Wait()
}

// rowJob is a unit of work dispatched to a pool worker.
type rowJob struct {
	r  rowRange
	fn func(startY, endY int)
	wg *sync.WaitGroup
}

// RowPool runs row-range work across a fixed set of long-lived worker
// goroutines. The row partition is computed once for the configured height and
// reused for every Run call, avoiding the per-frame goroutine create/destroy
// cost of ParallelRows on the encoder hot path.
type RowPool struct {
	ranges []rowRange
	jobs   chan rowJob
}

// NewRowPool creates a pool with one worker per row range for the given height.
// The workers are daemon goroutines; in a single-shot CLI they are reaped on
// process exit. Call Close to stop them explicitly when reuse is finished.
func NewRowPool(height int) *RowPool {
	ranges := partitionRows(height)
	p := &RowPool{
		ranges: ranges,
		jobs:   make(chan rowJob),
	}
	for range ranges {
		go p.worker()
	}
	return p
}

func (p *RowPool) worker() {
	for job := range p.jobs {
		job.fn(job.r.startY, job.r.endY)
		job.wg.Done()
	}
}

// Run executes fn across the precomputed row partition and blocks until every
// range completes. Output is identical to ParallelRows for the same height.
func (p *RowPool) Run(fn func(startY, endY int)) {
	var wg sync.WaitGroup
	wg.Add(len(p.ranges))
	for _, r := range p.ranges {
		p.jobs <- rowJob{r: r, fn: fn, wg: &wg}
	}
	wg.Wait()
}

// Close stops the pool's worker goroutines. The pool must not be used after.
func (p *RowPool) Close() {
	close(p.jobs)
}
