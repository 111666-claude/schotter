package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	svg "github.com/ajstarks/svgo"
)

func testConfig() Config {
	return Config{
		Columns:     10,
		Rows:        20,
		Size:        40,
		Margin:      20,
		Orderliness: 8,
		Disturbance: 1,
		Seed:        424242,
	}
}

// Equal seeds produce equal target layouts, including exact RNG values.
func TestTargetSquaresDeterministic(t *testing.T) {
	cfg := testConfig()
	a := cfg.TargetSquares()
	b := cfg.TargetSquares()
	if len(a) != cfg.Rows*cfg.Columns || len(a) != len(b) {
		t.Fatalf("unexpected square counts: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("square %d differs between equal seeds: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// The first frame is the perfectly ordered grid, even at maximum disturbance.
func TestFirstFrameIsOrderedGrid(t *testing.T) {
	cfg := testConfig()
	cfg.Disturbance = 10
	first := cfg.FrameSquares(24, 0)

	for i, q := range first {
		x := i % cfg.Columns
		y := i / cfg.Columns
		want := Square{
			X:        cfg.Margin + float64(x)*cfg.Size,
			Y:        cfg.Margin + float64(y)*cfg.Size,
			Rotation: 0,
		}
		if q != want {
			t.Fatalf("first-frame square %d = %+v, want grid position %+v", i, q, want)
		}
	}
}

// The last animation frame is exactly the static layout.
func TestLastFrameEqualsStatic(t *testing.T) {
	cfg := testConfig()
	for _, n := range []int{1, 2, 3, 24, 500} {
		target := cfg.TargetSquares()
		last := cfg.FrameSquares(n, n-1)
		for i := range target {
			if target[i] != last[i] {
				t.Fatalf("n=%d square %d: static %+v != last frame %+v", n, i, target[i], last[i])
			}
		}
	}
}

// Adjacent frames move each square by a constant step, so motion is continuous
// and never jumps (the bug of per-frame independent randomness).
func TestAdjacentFrameDisplacementIsConstant(t *testing.T) {
	cfg := testConfig()
	const n = 32
	prev := cfg.FrameSquares(n, 0)

	for f := 1; f < n; f++ {
		cur := cfg.FrameSquares(n, f)
		if len(cur) != len(prev) {
			t.Fatalf("frame %d changed square count", f)
		}
		for i := range cur {
			dx := cur[i].X - prev[i].X
			dy := cur[i].Y - prev[i].Y
			dr := cur[i].Rotation - prev[i].Rotation

			target := cfg.TargetSquares()[i]
			baseX := cfg.Margin + float64(i%cfg.Columns)*cfg.Size
			baseY := cfg.Margin + float64(i/cfg.Columns)*cfg.Size
			wantX := (target.X - baseX) / float64(n-1)
			wantY := (target.Y - baseY) / float64(n-1)
			wantR := target.Rotation / float64(n-1)

			for _, got := range [][2]float64{{dx, wantX}, {dy, wantY}, {dr, wantR}} {
				if math.Abs(got[0]-got[1]) > 1e-12 {
					t.Fatalf("frame %d square %d step %v, want %v", f, i, got[0], got[1])
				}
			}
		}
		prev = cur
	}
}

// Interpolated positions stay between grid and target, so no frame overshoots.
func TestInterpolationStaysBounded(t *testing.T) {
	cfg := testConfig()
	target := cfg.TargetSquares()
	for f := 0; f < 24; f++ {
		frame := cfg.FrameSquares(24, f)
		for i, q := range frame {
			baseX := cfg.Margin + float64(i%cfg.Columns)*cfg.Size
			baseY := cfg.Margin + float64(i/cfg.Columns)*cfg.Size
			loX, hiX := math.Min(baseX, target[i].X), math.Max(baseX, target[i].X)
			loY, hiY := math.Min(baseY, target[i].Y), math.Max(baseY, target[i].Y)
			if q.X < loX-1e-12 || q.X > hiX+1e-12 || q.Y < loY-1e-12 || q.Y > hiY+1e-12 {
				t.Fatalf("frame %d square %d overshoots: %+v", f, i, q)
			}
			if math.Abs(q.Rotation) > math.Abs(target[i].Rotation)+1e-12 {
				t.Fatalf("frame %d square %d rotation overshoots: %+v", f, i, q)
			}
		}
	}
}

// Disturbance zero collapses any layout onto the ordered grid.
func TestZeroDisturbanceIsGrid(t *testing.T) {
	cfg := testConfig()
	cfg.Disturbance = 0
	for _, q := range cfg.TargetSquares() {
		if q.Rotation != 0 {
			t.Fatalf("zero disturbance rotated a square: %+v", q)
		}
	}
}

// legacySVG reproduces the exact original v1.1.0 generation path.
func legacySVG(cfg Config) []byte {
	var buf bytes.Buffer
	canvas := svg.New(&buf)
	canvas.Start(
		int(2.0*cfg.Margin+float64(cfg.Columns)*cfg.Size),
		int(2.0*cfg.Margin+float64(cfg.Rows)*cfg.Size),
	)

	rng := rand.New(rand.NewSource(cfg.Seed))
	for y := 0; y < cfg.Rows; y++ {
		factor := float64(y*y+y) / cfg.Orderliness
		for x := 0; x < cfg.Columns; x++ {
			offsetX := (rng.Float64()*2 - 1) * factor / 100.0
			offsetY := (rng.Float64()*2 - 1) * factor / 100.0
			rotate := (rng.Float64()*2 - 1) * factor / 2.0
			canvas.TranslateRotate(
				int(cfg.Margin+(float64(x)+offsetX)*cfg.Size),
				int(cfg.Margin+(float64(y)+offsetY)*cfg.Size),
				rotate,
			)
			canvas.Rect(0, 0, int(cfg.Size), int(cfg.Size), "fill:none;stroke:black")
			canvas.Gend()
		}
	}
	canvas.End()
	return buf.Bytes()
}

// The new code path must emit byte-for-byte identical SVG for a fixed seed,
// which locks the RNG order and int-cast behavior of the original program.
func TestStaticSVGMatchesLegacyByteForByte(t *testing.T) {
	cfg := testConfig()
	var buf bytes.Buffer
	cfg.RenderSVG(&buf, cfg.TargetSquares())

	if !bytes.Equal(buf.Bytes(), legacySVG(cfg)) {
		t.Fatalf("SVG output diverged from the legacy implementation")
	}
}

func TestValidation(t *testing.T) {
	bad := []Config{
		{Columns: 0, Rows: 1, Size: 10, Margin: 1, Orderliness: 1, Seed: 1},
		{Columns: 1, Rows: -1, Size: 10, Margin: 1, Orderliness: 1, Seed: 1},
		{Columns: 1, Rows: 1, Size: 0, Margin: 1, Orderliness: 1, Seed: 1},
		{Columns: 1, Rows: 1, Size: 10, Margin: -1, Orderliness: 1, Seed: 1},
		{Columns: 1, Rows: 1, Size: 10, Margin: 1, Orderliness: 0, Seed: 1},
		{Columns: 1, Rows: 1, Size: 10, Margin: 1, Orderliness: 1, Disturbance: -0.1, Seed: 1},
	}
	for i, cfg := range bad {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config #%d was accepted: %+v", i, cfg)
		}
	}
}

// The raster memory cap triggers before any giant allocation.
func TestCanvasPixelCap(t *testing.T) {
	cfg := testConfig()
	cfg.Size = 1e6 // canvas far above maxCanvasPixels
	if err := cfg.Validate(); err == nil {
		t.Fatal("oversized canvas was accepted")
	}
}

func TestFramePathValidation(t *testing.T) {
	if _, err := framePath("out.png", 5, 0); err == nil {
		t.Fatal("pattern without width verb accepted for multiple frames")
	}
	// A bare filename is legal for a single-frame render.
	path, err := framePath("out.png", 1, 0)
	if err != nil {
		t.Fatalf("single-frame bare filename rejected: %v", err)
	}
	if path != "out.png" {
		t.Fatalf("got %q, want out.png", path)
	}
	if _, err := framePath("frame_%d.gif", 4, 0); err == nil {
		t.Fatal("non-PNG extension was accepted")
	}
	path, err = framePath("frame_%04d.png", 12, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(path) != "frame_0007.png" {
		t.Fatalf("unexpected path %q", path)
	}
}

func TestGIFLimits(t *testing.T) {
	cfg := testConfig()
	var buf bytes.Buffer
	if err := cfg.RenderGIF(context.Background(), &buf, maxGIFFrames+1, 10); err == nil {
		t.Fatal("GIF above frame cap was accepted")
	}

	small := cfg
	small.Columns, small.Rows = 200, 200
	small.Size, small.Margin = 4, 0 // 800x800 per frame; 500 frames exceed the pixel cap
	if err := small.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if err := small.RenderGIF(context.Background(), &buf, 500, 10); err == nil {
		t.Fatal("GIF above cumulative pixel cap was accepted")
	}
}

func TestRenderGIFRoundTrip(t *testing.T) {
	cfg := testConfig()
	var buf bytes.Buffer
	if err := cfg.RenderGIF(context.Background(), &buf, 12, 5); err != nil {
		t.Fatalf("RenderGIF failed: %v", err)
	}
	anim, err := gif.DecodeAll(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decoding GIF failed: %v", err)
	}
	if len(anim.Image) != 12 {
		t.Fatalf("got %d frames, want 12", len(anim.Image))
	}
	for _, d := range anim.Delay {
		if d != 5 {
			t.Fatalf("got delay %d, want 5", d)
		}
	}
	// Black outline pixels must exist and the palette stays two entries.
	img := anim.Image[0]
	blackPixels := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.ColorIndexAt(x, y) == 1 {
				blackPixels++
			}
		}
	}
	if blackPixels == 0 {
		t.Fatal("rendered GIF contains no black outline pixels")
	}
}

func TestRenderPNGSequence(t *testing.T) {
	cfg := testConfig()
	dir := t.TempDir()
	pattern := filepath.Join(dir, "frame_%03d.png")
	ctx := context.Background()
	if err := cfg.RenderPNGSequence(ctx, pattern, 5); err != nil {
		t.Fatalf("RenderPNGSequence failed: %v", err)
	}
	for f := 0; f < 5; f++ {
		path := filepath.Join(dir, fmt.Sprintf("frame_%03d.png", f))
		fh, err := os.Open(path)
		if err != nil {
			t.Fatalf("frame %d missing: %v", f, err)
		}
		img, err := png.Decode(fh)
		fh.Close()
		if err != nil {
			t.Fatalf("frame %d is not a valid PNG: %v", f, err)
		}
		w, h := cfg.CanvasSize()
		if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
			t.Fatalf("frame %d size = %v, want %dx%d", f, img.Bounds().Size(), w, h)
		}
	}
}

// Cancelling a PNG export aborts deterministically and leaves no partial file
// for the frame that was in flight.
func TestRenderPNGSequenceCancellation(t *testing.T) {
	cfg := testConfig()
	cfg.Columns, cfg.Rows = 4, 4
	dir := t.TempDir()
	pattern := filepath.Join(dir, "frame_%04d.png")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cfg.RenderPNGSequence(ctx, pattern, 100)
	if err == nil {
		t.Fatal("cancelled export reported success")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".schotter-*"))
	if len(matches) != 0 {
		t.Fatalf("cancelled export left temp files: %v", matches)
	}
}

func TestWriteAtomicRemovesTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.bin")
	err := writeAtomic(target, func(w io.Writer) error {
		return os.ErrPermission
	})
	if err == nil {
		t.Fatal("failing encoder was reported as success")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("target file appeared despite encoder failure")
	}
}

func TestEnsureWritableCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.svg")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritable(path, false); err == nil {
		t.Fatal("existing file was accepted without --force")
	}
	if err := ensureWritable(path, true); err != nil {
		t.Fatalf("--force should allow overwrite, got %v", err)
	}
}

// --- End-to-end CLI tests -------------------------------------------------

func runCLI(t *testing.T, args ...string) int {
	t.Helper()
	return run(append(args, "--seed", "12345"))
}

func TestCLIStaticSVG(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.svg")
	if code := runCLI(t, out); code != 0 {
		t.Fatalf("static SVG exit code = %d", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("<?xml")) {
		t.Fatal("output is not an SVG/XML document")
	}

	// Re-running without --force must fail deterministically with code 1.
	if code := runCLI(t, out); code != 1 {
		t.Fatalf("collision exit code = %d, want 1", code)
	}
	// --force overwrites successfully.
	if code := runCLI(t, out, "--force"); code != 0 {
		t.Fatalf("force overwrite exit code = %d", code)
	}
}

func TestCLIReproducibleOutputs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.svg")
	b := filepath.Join(dir, "b.svg")
	if code := runCLI(t, a); code != 0 || runCLI(t, b) != 0 {
		t.Fatal("static generation failed")
	}
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if !bytes.Equal(da, db) {
		t.Fatal("equal seeds produced different SVG files")
	}
}

func TestCLIGIFAndPNGSequence(t *testing.T) {
	dir := t.TempDir()

	gifPath := filepath.Join(dir, "anim.gif")
	if code := runCLI(t, gifPath, "--frames", "8"); code != 0 {
		t.Fatalf("GIF export exit code = %d", code)
	}
	fh, err := os.Open(gifPath)
	if err != nil {
		t.Fatal(err)
	}
	anim, err := gif.DecodeAll(fh)
	fh.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(anim.Image) != 8 {
		t.Fatalf("GIF has %d frames, want 8", len(anim.Image))
	}

	pngPattern := filepath.Join(dir, "f_%02d.png")
	if code := runCLI(t, pngPattern, "--frames", "4"); code != 0 {
		t.Fatalf("PNG export exit code = %d", code)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "f_*.png")); len(entries) != 4 {
		t.Fatalf("got %d PNG files, want 4", len(entries))
	}
}

func TestCLIEdgeCases(t *testing.T) {
	dir := t.TempDir()

	// Negative disturbance and negative frames are usage errors.
	if code := runCLI(t, filepath.Join(dir, "x.svg"), "--disturbance", "-1"); code != 2 {
		t.Fatalf("negative disturbance exit code = %d, want 2", code)
	}
	if code := runCLI(t, filepath.Join(dir, "x.gif"), "--frames", "-1"); code != 2 {
		t.Fatalf("negative frames exit code = %d, want 2", code)
	}

	// Excessive GIF frame count fails at runtime without writing a file.
	gifPath := filepath.Join(dir, "huge.gif")
	if code := runCLI(t, gifPath, "--frames", "999999"); code != 1 {
		t.Fatalf("oversized GIF exit code = %d, want 1", code)
	}
	if _, err := os.Stat(gifPath); !os.IsNotExist(err) {
		t.Fatal("failed GIF export left an output file behind")
	}

	// Static mode refuses raster suffixes; SVG animation is unsupported.
	if code := runCLI(t, filepath.Join(dir, "x.png")); code != 2 {
		t.Fatalf("static PNG exit code = %d, want 2", code)
	}
	if code := runCLI(t, filepath.Join(dir, "x.svg"), "--frames", "4"); code != 2 {
		t.Fatalf("SVG animation exit code = %d, want 2", code)
	}

	// Missing positional argument and unknown flags are usage errors.
	if code := run([]string{"--seed", "1"}); code != 2 {
		t.Fatalf("missing output exit code = %d, want 2", code)
	}
	if code := runCLI(t, filepath.Join(dir, "x.svg"), "--no-such-flag"); code != 2 {
		t.Fatalf("unknown flag exit code = %d, want 2", code)
	}

	// Bad output directory fails cleanly rather than panicking.
	if code := runCLI(t, filepath.Join(dir, "nodir", "x.svg")); code != 1 {
		t.Fatalf("bad directory exit code = %d, want 1", code)
	}
}

// A time-based seed path must still produce a valid run.
func TestCLITimeSeed(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "t.svg")
	if code := run([]string{out, "--force"}); code != 0 {
		t.Fatalf("time-seeded run exit code = %d", code)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Fatal("time-seeded run produced no output")
	}
}

// Raster output uses the expected white background and black squares.
func TestRasterColors(t *testing.T) {
	cfg := testConfig()
	img := cfg.rasterFrame(cfg.FrameSquares(1, 0))
	white, black := 0, 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, g, b, a := img.At(x, y).RGBA()
			c := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
			switch c {
			case color.RGBA{0xff, 0xff, 0xff, 0xff}:
				white++
			case color.RGBA{0, 0, 0, 0xff}:
				black++
			default:
				t.Fatalf("unexpected pixel color %v at %d,%d", c, x, y)
			}
		}
	}
	if white == 0 || black == 0 {
		t.Fatalf("raster missing colors: white=%d black=%d", white, black)
	}
}

// The grid first frame and scattered last frame genuinely differ on screen.
func TestRasterFramesVisuallyDiffer(t *testing.T) {
	cfg := testConfig()
	first := cfg.rasterFrame(cfg.FrameSquares(24, 0))
	last := cfg.rasterFrame(cfg.FrameSquares(24, 23))
	diff := 0
	for i := range first.Pix {
		if first.Pix[i] != last.Pix[i] {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("first and last raster frames are identical")
	}
}

// Regression test: drawLine must always terminate, including for reversed,
// steep and degenerate segments, and must stay within its iteration budget.
func TestDrawLineTerminates(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	black := color.RGBA{0, 0, 0, 0xff}
	for i := 0; i < 2000; i++ {
		x0 := rng.Float64()*80 - 15
		y0 := rng.Float64()*80 - 15
		x1 := rng.Float64()*80 - 15
		y1 := rng.Float64()*80 - 15
		done := make(chan struct{})
		go func() {
			drawLine(img, x0, y0, x1, y1, black)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("drawLine hung for endpoints (%v,%v)->(%v,%v)", x0, y0, x1, y1)
		}
	}
}

// Regression test: rasterizing arbitrary valid configurations must terminate
// even when disturbance pushes squares far outside the canvas.
func TestRasterFrameTerminatesForExtremeDisturbance(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 25; i++ {
		cfg := testConfig()
		cfg.Seed = rng.Int63()
		cfg.Disturbance = rng.Float64() * 100
		done := make(chan struct{})
		go func() {
			_ = cfg.rasterFrame(cfg.FrameSquares(1+rng.Intn(5), rng.Intn(5)))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("rasterFrame hung for disturbance %.2f seed %d", cfg.Disturbance, cfg.Seed)
		}
	}
}
