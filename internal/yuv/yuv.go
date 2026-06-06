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

// ParallelRows executes fn across height rows using all CPU cores.
func ParallelRows(height int, fn func(startY, endY int)) {
	numCPU := runtime.NumCPU()
	rowsPerWorker := height / numCPU
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		numCPU = height
	}

	var wg sync.WaitGroup
	wg.Add(numCPU)

	for worker := range numCPU {
		startY := worker * rowsPerWorker
		endY := startY + rowsPerWorker
		if worker == numCPU-1 {
			endY = height
		}

		go func(startY, endY int) {
			defer wg.Done()
			fn(startY, endY)
		}(startY, endY)
	}

	wg.Wait()
}
