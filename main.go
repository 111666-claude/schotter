package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/pflag"
)

// errCancelled is returned when the user interrupts an export (Ctrl-C).
var errCancelled = errors.New("export cancelled")

// usageError marks mistakes in the user's invocation, which must exit with
// code 2 like flag and validation errors rather than the runtime code 1.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

// run wires flags, validation and rendering together. Exit codes follow the
// original program: 2 for usage/validation errors, 1 for runtime errors and
// 130 for an explicit user cancellation.
func run(args []string) int {
	// A local flag set with ContinueOnError keeps all exits inside run() so
	// the documented exit codes are deterministic and testable.
	flags := pflag.NewFlagSet("schotter", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var (
		columns     = flags.IntP("columns", "c", 10, "Number of columns")
		rows        = flags.IntP("rows", "r", 20, "Number of rows")
		size        = flags.Float64P("size", "s", 100.0, "Size of each square")
		margin      = flags.Float64P("margin", "m", 100.0, "Margin around all squares")
		orderliness = flags.Float64P("orderliness", "o", 8.0, "Amount of orderliness, higher is more ordered")
		disturbance = flags.Float64P("disturbance", "d", 1.0, "Scale of the random disturbance, 0 is a perfect grid")
		seed        = flags.Int64("seed", -1, "Random seed; -1 picks a time-based seed that is printed for reuse")
		frames      = flags.Int("frames", 0, "Number of animation frames; 0 keeps the original single SVG mode")
		delay       = flags.Int("delay", 10, "GIF frame delay in hundredths of a second")
		force       = flags.Bool("force", false, "Overwrite existing output files")
		help        = flags.BoolP("help", "h", false, "Help for schotter")
		version     = flags.Bool("version", false, "Version of schotter")
	)

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			printBanner()
			fmt.Println("Generate digital line art, inspired by Georg Nees' \"Schotter\".")
			fmt.Println("")
			printHelp(flags)
			return 0
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	if *help {
		printBanner()
		fmt.Println("Generate digital line art, inspired by Georg Nees' \"Schotter\".")
		fmt.Println("")
		printHelp(flags)
		return 0
	}
	if *version {
		fmt.Println("schotter version 1.2.0")
		return 0
	}

	if flags.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Error: Output filename missing.")
		return 2
	}
	output := flags.Arg(0)

	if *seed == -1 {
		*seed = time.Now().UnixNano()
	}

	cfg := Config{
		// Preserve the historical (swapped) flag semantics of the original
		// program: -c drives the canvas height loop, -r the width.
		Columns:     *rows,
		Rows:        *columns,
		Size:        *size,
		Margin:      *margin,
		Orderliness: *orderliness,
		Disturbance: *disturbance,
		Seed:        *seed,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	}
	if *frames < 0 {
		fmt.Fprintln(os.Stderr, "Error: number of frames must be zero or positive")
		return 2
	}
	if *delay < 0 {
		fmt.Fprintln(os.Stderr, "Error: GIF delay must be zero or positive")
		return 2
	}

	printBanner()
	fmt.Printf("Using seed %d\n", cfg.Seed)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := dispatch(ctx, cfg, output, *frames, *delay, *force)
	var uErr usageError
	switch {
	case errors.Is(err, errCancelled), errors.Is(err, context.Canceled):
		fmt.Fprintln(os.Stderr, "Cancelled; partial output removed")
		return 130
	case errors.As(err, &uErr):
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 2
	case err != nil:
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 1
	default:
		fmt.Printf("Successfully created output %s\n", output)
		return 0
	}
}

// dispatch selects static SVG, animated GIF or PNG sequence rendering and
// performs all output collision checks before any frame is generated.
func dispatch(ctx context.Context, cfg Config, output string, frames, delay int, force bool) error {
	// Static, single-image mode: the original SVG behaviour, unchanged.
	if frames == 0 {
		if filepath.Ext(output) != ".svg" {
			return usageError{fmt.Errorf("static mode writes SVG; use --frames for PNG or GIF output")}
		}
		if err := ensureWritable(output, force); err != nil {
			return err
		}
		return writeAtomic(output, func(w io.Writer) error {
			cfg.RenderSVG(w, cfg.TargetSquares())
			return nil
		})
	}

	switch {
	case strings.HasSuffix(output, ".gif"):
		if err := ensureWritable(output, force); err != nil {
			return err
		}
		return writeAtomic(output, func(w io.Writer) error {
			return cfg.RenderGIF(ctx, w, frames, delay)
		})

	case strings.HasSuffix(output, ".png") || strings.Contains(output, "%"):
		if frames > maxPNGFrames {
			return usageError{fmt.Errorf("PNG sequence is limited to %d frames", maxPNGFrames)}
		}
		// Precheck every frame name: a collision aborts before any work and
		// a pattern without a width verb would silently overwrite frames.
		for f := 0; f < frames; f++ {
			path, err := framePath(output, frames, f)
			if err != nil {
				return usageError{err}
			}
			if err := ensureWritable(path, force); err != nil {
				return err
			}
		}
		return cfg.RenderPNGSequence(ctx, output, frames)

	default:
		return usageError{fmt.Errorf("animation output must end in .gif or be a PNG pattern like frame_%%04d.png; got %q", output)}
	}
}

// ensureWritable fails deterministically when path exists and overwriting was
// not requested, and verifies the parent directory is usable.
func ensureWritable(path string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite", path)
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("output directory %s is not accessible: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("output directory %s is not a directory", dir)
	}
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

// Print banner
func printBanner() {
	color.HiCyan("             __          __   __              ")
	color.HiCyan(".-----.-----|  |--.-----|  |_|  |_.-----.-.--.")
	color.HiCyan("|__ --|  ---|  .  |  -  |   _|   _|  -__|  .-'")
	color.HiCyan("|_____|_____|__|__|_____|____|____|_____|__|  ")
	color.HiCyan("                                              ")
}

// printHelp prints the help screen using the program's flag definitions.
func printHelp(flags *pflag.FlagSet) {
	fmt.Println("Usage:")
	fmt.Println("  schotter [flags] file")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  schotter sample.svg                  # single SVG (original mode)")
	fmt.Println("  schotter --frames 24 anim.gif        # ordered-to-scattered GIF")
	fmt.Println("  schotter --frames 24 frame_%04d.png  # PNG sequence")
	fmt.Println("")
	fmt.Println("Flags:")
	flags.PrintDefaults()
}
