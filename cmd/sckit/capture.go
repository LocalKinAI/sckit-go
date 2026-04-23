package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"sort"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func runCapture(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sckit capture: need target (display | window | app | region)")
		return 2
	}
	switch args[0] {
	case "display", "d":
		return runCaptureDisplay(args[1:])
	case "window", "w":
		return runCaptureWindow(args[1:])
	case "app", "a":
		return runCaptureApp(args[1:])
	case "region", "r":
		return runCaptureRegion(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "sckit capture: unknown target %q\n", args[0])
		return 2
	}
}

// common flags wiring used by all capture subcommands.
func addCaptureFlags(fs *flag.FlagSet, cf *captureFlags) {
	fs.StringVar(&cf.output, "o", "", "output PNG path (default: auto-generated)")
	fs.StringVar(&cf.output, "output", "", "output PNG path (default: auto-generated)")
	fs.BoolVar(&cf.noCursor, "no-cursor", false, "hide the cursor in the capture")
	fs.StringVar(&cf.resolution, "resolution", "", "output resolution as WxH (default: native)")
	fs.StringVar(&cf.color, "color", "srgb", "color space: srgb | p3 | bt709")
}

// captureValueFlags lists capture-subcommand flags that consume a
// following token. Used by reorderArgs to correctly split interleaved
// flags + positional args.
var captureValueFlags = map[string]bool{
	"o":          true,
	"output":     true,
	"resolution": true,
	"color":      true,
	"display":    true,
}

// ─── capture display ─────────────────────────────────────────

func runCaptureDisplay(args []string) int {
	cf := &captureFlags{}
	fs := flag.NewFlagSet("capture display", flag.ExitOnError)
	addCaptureFlags(fs, cf)
	_ = fs.Parse(reorderArgs(args, captureValueFlags))

	var displayID uint32
	rest := fs.Args()
	if len(rest) >= 1 {
		v, err := parseUint32(rest[0], "display ID")
		if err != nil {
			return die("%v", err)
		}
		displayID = v
	}

	ctx, cancel := newCtx(10 * time.Second)
	defer cancel()

	d, err := parseDisplay(ctx, displayID)
	if err != nil {
		return die("%v", err)
	}
	opts, err := cf.sckitOptions()
	if err != nil {
		return die("%v", err)
	}
	out := cf.output
	if out == "" {
		out = autoFilename("display")
	}
	fmt.Printf("Capturing display %d (%dx%d) → %s\n", d.ID, d.Width, d.Height, out)

	t0 := time.Now()
	if err := sckit.CaptureToFile(ctx, d, out, opts...); err != nil {
		return die("capture: %v", err)
	}
	fmt.Printf("✓ done in %s\n", time.Since(t0).Round(time.Millisecond))
	return 0
}

// ─── capture window ──────────────────────────────────────────

func runCaptureWindow(args []string) int {
	cf := &captureFlags{}
	fs := flag.NewFlagSet("capture window", flag.ExitOnError)
	addCaptureFlags(fs, cf)
	_ = fs.Parse(reorderArgs(args, captureValueFlags))

	rest := fs.Args()
	var target sckit.Window
	ctx, cancel := newCtx(10 * time.Second)
	defer cancel()

	if len(rest) >= 1 {
		id, err := parseUint32(rest[0], "window ID")
		if err != nil {
			return die("%v", err)
		}
		target = sckit.Window{ID: id}
	} else {
		// auto-pick largest foreground window
		windows, err := sckit.ListWindows(ctx)
		if err != nil {
			return die("list windows: %v", err)
		}
		var candidates []sckit.Window
		for _, w := range windows {
			if w.OnScreen && w.Layer == 0 && w.Frame.Dx() > 100 && w.Frame.Dy() > 100 {
				candidates = append(candidates, w)
			}
		}
		if len(candidates) == 0 {
			return die("no suitable foreground window found; pass a window ID")
		}
		sort.Slice(candidates, func(i, j int) bool {
			a := candidates[i].Frame.Dx() * candidates[i].Frame.Dy()
			b := candidates[j].Frame.Dx() * candidates[j].Frame.Dy()
			return a > b
		})
		target = candidates[0]
		fmt.Printf("Auto-picked window %d (%s: %q %dx%d)\n",
			target.ID, target.App, target.Title, target.Frame.Dx(), target.Frame.Dy())
	}
	opts, err := cf.sckitOptions()
	if err != nil {
		return die("%v", err)
	}
	out := cf.output
	if out == "" {
		out = autoFilename("window")
	}
	t0 := time.Now()
	if err := sckit.CaptureToFile(ctx, target, out, opts...); err != nil {
		return die("capture window: %v", err)
	}
	fmt.Printf("✓ %s  (done in %s)\n", out, time.Since(t0).Round(time.Millisecond))
	return 0
}

// ─── capture app ─────────────────────────────────────────────

func runCaptureApp(args []string) int {
	cf := &captureFlags{}
	fs := flag.NewFlagSet("capture app", flag.ExitOnError)
	addCaptureFlags(fs, cf)
	_ = fs.Parse(reorderArgs(args, captureValueFlags))

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "sckit capture app: need bundle ID")
		fmt.Fprintln(os.Stderr, "  (try `sckit list apps` to find one)")
		return 2
	}
	bundleID := rest[0]
	opts, err := cf.sckitOptions()
	if err != nil {
		return die("%v", err)
	}
	out := cf.output
	if out == "" {
		out = autoFilename("app")
	}

	ctx, cancel := newCtx(10 * time.Second)
	defer cancel()

	t0 := time.Now()
	if err := sckit.CaptureToFile(ctx, sckit.App{BundleID: bundleID}, out, opts...); err != nil {
		return die("capture app: %v", err)
	}
	fmt.Printf("✓ %s  (done in %s)\n", out, time.Since(t0).Round(time.Millisecond))
	return 0
}

// ─── capture region ──────────────────────────────────────────

func runCaptureRegion(args []string) int {
	cf := &captureFlags{}
	fs := flag.NewFlagSet("capture region", flag.ExitOnError)
	addCaptureFlags(fs, cf)
	displayID := fs.Uint("display", 0, "display ID (default: main)")
	_ = fs.Parse(reorderArgs(args, captureValueFlags))

	rest := fs.Args()
	if len(rest) < 4 {
		fmt.Fprintln(os.Stderr, "sckit capture region: need x y w h (4 integers)")
		return 2
	}
	x, err := parseInt(rest[0], "x")
	if err != nil {
		return die("%v", err)
	}
	y, err := parseInt(rest[1], "y")
	if err != nil {
		return die("%v", err)
	}
	w, err := parseInt(rest[2], "w")
	if err != nil {
		return die("%v", err)
	}
	h, err := parseInt(rest[3], "h")
	if err != nil {
		return die("%v", err)
	}
	if w <= 0 || h <= 0 {
		return die("region width/height must be positive (got %dx%d)", w, h)
	}

	ctx, cancel := newCtx(10 * time.Second)
	defer cancel()

	d, err := parseDisplay(ctx, uint32(*displayID))
	if err != nil {
		return die("%v", err)
	}
	opts, err := cf.sckitOptions()
	if err != nil {
		return die("%v", err)
	}
	out := cf.output
	if out == "" {
		out = autoFilename("region")
	}

	region := sckit.Region{Display: d, Bounds: image.Rect(x, y, x+w, y+h)}
	fmt.Printf("Capturing region %dx%d @ (%d,%d) on display %d → %s\n",
		w, h, x, y, d.ID, out)

	t0 := time.Now()
	if err := sckit.CaptureToFile(ctx, region, out, opts...); err != nil {
		return die("capture region: %v", err)
	}
	fmt.Printf("✓ done in %s\n", time.Since(t0).Round(time.Millisecond))
	return 0
}
