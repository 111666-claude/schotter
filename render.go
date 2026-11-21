package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	svg "github.com/ajstarks/svgo"
)

// blackOnWhite is the fixed palette used for animated GIFs.
var blackOnWhite = color.Palette{
	color.RGBA{0xff, 0xff, 0xff, 0xff},
	color.RGBA{0x00, 0x00, 0x00, 0xff},
}

// RenderSVG writes a single Schotter layout as SVG, using the same calls as
// the original implementation so output stays byte-for-byte compatible.
func (c Config) RenderSVG(w io.Writer, squares []Square) {
	width, height := c.CanvasSize()
	canvas := svg.New(w)
	canvas.Start(width, height)
	defer canvas.End()

	for _, q := range squares {
		canvas.TranslateRotate(int(q.X), int(q.Y), q.Rotation)
		canvas.Rect(0, 0, int(c.Size), int(c.Size), "fill:none;stroke:black")
		canvas.Gend()
	}
}

// rasterFrame paints one layout onto a freshly allocated white RGBA image.
// Only one frame is ever resident for PNG sequences; GIF renders share the
// same per-frame memory cost.
func (c Config) rasterFrame(squares []Square) *image.RGBA {
	width, height := c.CanvasSize()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0xff, 0xff, 0xff, 0xff}}, image.Point{}, draw.Src)

	black := color.RGBA{0, 0, 0, 0xff}
	half := c.Size / 2
	cos := make([]float64, len(squares))
	sin := make([]float64, len(squares))
	for i, q := range squares {
		rad := q.Rotation * math.Pi / 180
		cos[i], sin[i] = math.Cos(rad), math.Sin(rad)
	}

	// Four corners of every square relative to its pivot (top-left), rotated
	// about the pivot just like the SVG transform.
	corners := [4][2]float64{
		{0, 0},
		{c.Size, 0},
		{c.Size, c.Size},
		{0, c.Size},
	}

	for i, q := range squares {
		var pts [4][2]float64
		for j, p := range corners {
			// Rotate about the square center so rounding is symmetric.
			dx, dy := p[0]-half, p[1]-half
			pts[j][0] = q.X + half + dx*cos[i] - dy*sin[i]
			pts[j][1] = q.Y + half + dx*sin[i] + dy*cos[i]
		}
		for j := 0; j < 4; j++ {
			drawLine(img, pts[j][0], pts[j][1], pts[(j+1)%4][0], pts[(j+1)%4][1], black)
		}
	}
	return img
}

// drawLine rasterizes a one-pixel black line with the integer Bresenham
// algorithm. Endpoints are rounded to pixels first, so the loop runs at most
// |dx|+|dy|+1 times and terminates on exact coordinate equality. Pixels
// outside the canvas are skipped, so strongly disturbed squares are clipped
// instead of panicking.
func drawLine(img *image.RGBA, x0f, y0f, x1f, y1f float64, col color.RGBA) {
	x0, y0 := int(math.Round(x0f)), int(math.Round(y0f))
	x1, y1 := int(math.Round(x1f)), int(math.Round(y1f))

	dx := absInt(x1 - x0)
	dy := -absInt(y1 - y0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	bounds := img.Bounds()

	for {
		if x0 >= bounds.Min.X && x0 < bounds.Max.X && y0 >= bounds.Min.Y && y0 < bounds.Max.Y {
			img.SetRGBA(x0, y0, col)
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// absInt returns the absolute value of an int.
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// RenderPNGSequence streams frames as individual PNG files named by pattern,
// which must contain a fmt-compatible width verb such as "frame_%04d.png".
// Files are written one at a time via a temp file plus atomic rename, so
// memory is bounded by a single frame and a cancelled run leaves no partial
// files behind.
func (c Config) RenderPNGSequence(ctx context.Context, pattern string, frameCount int) error {
	if frameCount > maxPNGFrames {
		return fmt.Errorf("PNG sequence is limited to %d frames", maxPNGFrames)
	}
	if _, err := framePath(pattern, frameCount, 0); err != nil {
		return err
	}

	for f := 0; f < frameCount; f++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		path, err := framePath(pattern, frameCount, f)
		if err != nil {
			return err
		}

		img := c.rasterFrame(c.FrameSquares(frameCount, f))
		if err := writeAtomic(path, func(w io.Writer) error {
			return png.Encode(w, img)
		}); err != nil {
			return err
		}
		fmt.Printf("Wrote %s (%d/%d)\n", path, f+1, frameCount)
	}
	return nil
}

// RenderGIF builds an animated GIF in memory. The cumulative size is bounded
// by maxGIFPixels / maxGIFFrames, checked before any frame is allocated.
func (c Config) RenderGIF(ctx context.Context, w io.Writer, frameCount, delayCentiseconds int) error {
	if frameCount > maxGIFFrames {
		return fmt.Errorf("GIF is limited to %d frames; export a PNG sequence instead", maxGIFFrames)
	}
	width, height := c.CanvasSize()
	totalPixels := int64(frameCount) * int64(width) * int64(height)
	if totalPixels > maxGIFPixels {
		return fmt.Errorf(
			"GIF of %d frames at %dx%d needs %d pixels and exceeds the limit of %d; export a PNG sequence instead",
			frameCount, width, height, totalPixels, maxGIFPixels,
		)
	}

	anim := gif.GIF{LoopCount: 0}
	for f := 0; f < frameCount; f++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		img := c.rasterFrame(c.FrameSquares(frameCount, f))
		paletted := image.NewPaletted(img.Bounds(), blackOnWhite)
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if img.RGBAAt(x, y).R < 0x80 {
					paletted.SetColorIndex(x, y, 1)
				}
			}
		}
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, delayCentiseconds)
	}
	return gif.EncodeAll(w, &anim)
}

// framePath validates the naming pattern and renders one concrete path. A
// pattern without a width verb (e.g. "out.png") is legal for a single frame,
// but rejected for several frames because every frame would overwrite the
// same file.
func framePath(pattern string, frameCount, index int) (string, error) {
	if !strings.Contains(pattern, "%") {
		if frameCount > 1 {
			return "", fmt.Errorf("PNG pattern %q must contain a width verb for multiple frames, e.g. frame_%%04d.png", pattern)
		}
		path := pattern
		if filepath.Ext(path) != ".png" {
			return "", fmt.Errorf("PNG output must use the .png extension, got %q", filepath.Ext(path))
		}
		return path, nil
	}

	path := fmt.Sprintf(pattern, index)
	if strings.Contains(path, "%") {
		return "", fmt.Errorf("invalid PNG pattern %q: only one %%d-style width verb is supported", pattern)
	}
	if filepath.Ext(path) != ".png" {
		return "", fmt.Errorf("PNG sequence frames must use the .png extension, got %q", filepath.Ext(path))
	}
	return path, nil
}

// writeAtomic writes to a sibling temp file and renames it into place on
// success, so failures and cancellations never leave a half-written target.
func writeAtomic(path string, encode func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".schotter-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if err := encode(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
