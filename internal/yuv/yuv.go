// Package yuv holds the shared RGB→YCbCr conversion primitives used by both the
// encoder hot path and the standalone bench-yuv tool.
package yuv

import (
	"runtime"
	"sync"
)

// YCbCr coefficients from Go's color/ycbcr.go (BT.601 standard).
// These are fixed-point values scaled by 65536 for integer arithmetic.
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
//
//go:inline
func RGBToY(r, g, b int32) uint8 {
	return uint8((YR*r + YG*g + YB*b + 1<<15) >> 16) //nolint:gosec // result is clamped to 0-255
}

// RGBToCb converts RGB to Cb (blue-difference chroma) with branchless clamping.
//
//go:inline
func RGBToCb(r, g, b int32) uint8 {
	cb := CbR*r + CbG*g + CbB*b + 257<<15
	if uint32(cb)&0xff000000 == 0 { //nolint:gosec // intentional bit manipulation
		cb >>= 16
	} else {
		cb = ^(cb >> 31)
	}
	return uint8(cb) //nolint:gosec // value is clamped by branch above
}

// RGBToCr converts RGB to Cr (red-difference chroma) with branchless clamping.
//
//go:inline
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

// partitionRows splits height rows across numCPU workers using the same rules
// ParallelRows uses: even slices of rowsPerWorker, a < 1 fallback that gives
// one row per worker, and the last worker absorbing the remainder.
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
