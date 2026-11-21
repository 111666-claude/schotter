package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
)

// Resource limits. They keep memory usage bounded regardless of the values a
// user passes on the command line, and every violation fails with a
// deterministic error before any large allocation happens.
const (
	// maxCanvasPixels bounds the size of a single rasterized frame. An RGBA
	// frame therefore never exceeds roughly 256 MiB.
	maxCanvasPixels = 64_000_000
	// maxGIFFrames bounds the number of frames held in memory for one GIF.
	maxGIFFrames = 500
	// maxGIFPixels bounds the cumulative pixels (and thus paletted memory) of
	// a single animated GIF.
	maxGIFPixels = 256 << 20
	// maxPNGFrames bounds the number of files in a PNG sequence. Frames are
	// streamed one at a time, so memory stays O(one frame).
	maxPNGFrames = 100_000
)

// Square describes one Schotter square in canvas coordinates. X and Y are the
// top-left corner, which is also the pivot of Rotation (measured in degrees),
// matching the original SVG output.
type Square struct {
	X, Y, Rotation float64
}

// Config holds all parameters of a Schotter rendering.
type Config struct {
	Columns     int     // Number of squares along the x axis
	Rows        int     // Number of squares along the y axis
	Size        float64 // Edge length of each square
	Margin      float64 // Margin around the whole grid
	Orderliness float64 // Original order parameter: higher means calmer
	Disturbance float64 // Scale of all random transforms; 0 yields a perfect grid
	Seed        int64   // RNG seed; equal seeds yield equal layouts
}

// Validate checks the configuration and enforces the raster memory cap.
func (c Config) Validate() error {
	if c.Columns <= 0 {
		return errors.New("number of columns must be positive")
	}
	if c.Rows <= 0 {
		return errors.New("number of rows must be positive")
	}
	if c.Size <= 0 || math.IsNaN(c.Size) || math.IsInf(c.Size, 0) {
		return errors.New("square size must be a positive, finite number")
	}
	if c.Margin < 0 || math.IsNaN(c.Margin) || math.IsInf(c.Margin, 0) {
		return errors.New("margin must be a non-negative, finite number")
	}
	if c.Orderliness <= 0 || math.IsNaN(c.Orderliness) || math.IsInf(c.Orderliness, 0) {
		return errors.New("orderliness must be a positive, finite number")
	}
	if c.Disturbance < 0 || math.IsNaN(c.Disturbance) || math.IsInf(c.Disturbance, 0) {
		return errors.New("disturbance must be a non-negative, finite number")
	}
	width, height := c.CanvasSize()
	if width <= 0 || height <= 0 {
		return errors.New("computed canvas size must be positive")
	}
	if int64(width)*int64(height) > maxCanvasPixels {
		return fmt.Errorf(
			"canvas of %dx%d exceeds the limit of %d pixels; reduce size or margin",
			width, height, maxCanvasPixels,
		)
	}
	return nil
}

// CanvasSize returns the raster/SVG dimensions in pixels.
func (c Config) CanvasSize() (int, int) {
	width := int(2.0*c.Margin + float64(c.Columns)*c.Size)
	height := int(2.0*c.Margin + float64(c.Rows)*c.Size)
	return width, height
}

// TargetSquares returns the fully disturbed layout. The RNG is consumed in the
// exact order of the original implementation (y outer loop, x inner loop,
// three draws per square), so a given seed reproduces the classic static
// output byte-for-byte when Disturbance is 1.
func (c Config) TargetSquares() []Square {
	rng := rand.New(rand.NewSource(c.Seed))
	squares := make([]Square, 0, c.Rows*c.Columns)

	for y := 0; y < c.Rows; y++ {
		factor := float64(y*y+y) / c.Orderliness

		for x := 0; x < c.Columns; x++ {
			offsetX := (rng.Float64()*2 - 1) * factor / 100.0 * c.Disturbance
			offsetY := (rng.Float64()*2 - 1) * factor / 100.0 * c.Disturbance
			rotation := (rng.Float64()*2 - 1) * factor / 2.0 * c.Disturbance

			squares = append(squares, Square{
				X:        c.Margin + (float64(x)+offsetX)*c.Size,
				Y:        c.Margin + (float64(y)+offsetY)*c.Size,
				Rotation: rotation,
			})
		}
	}
	return squares
}

// FrameSquares returns the layout of frame frameIndex out of frameCount. The
// random target layout is generated once and every square travels on a
// straight line from its grid position to its target position, so adjacent
// frames always differ by a constant, small displacement.
//
// frameCount 1 renders the final (fully disturbed) state, identical to a
// static generation with the same parameters.
func (c Config) FrameSquares(frameCount, frameIndex int) []Square {
	target := c.TargetSquares()
	progress := frameProgress(frameCount, frameIndex)

	if progress == 1 {
		return target
	}

	squares := make([]Square, len(target))
	for i, q := range target {
		x := i % c.Columns
		y := i / c.Columns
		baseX := c.Margin + float64(x)*c.Size
		baseY := c.Margin + float64(y)*c.Size

		squares[i] = Square{
			X:        baseX + (q.X-baseX)*progress,
			Y:        baseY + (q.Y-baseY)*progress,
			Rotation: q.Rotation * progress,
		}
	}
	return squares
}

// frameProgress returns the interpolation position of a frame: 0 for the
// perfectly ordered first frame, 1 for the fully disturbed last frame.
func frameProgress(frameCount, frameIndex int) float64 {
	if frameCount <= 1 {
		return 1
	}
	return float64(frameIndex) / float64(frameCount-1)
}
