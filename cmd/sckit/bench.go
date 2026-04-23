package main

import (
	"context"
	"fmt"
	"image"
	"sort"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func runBench(args []string) int {
	ctx, cancel := newCtx(90 * time.Second)
	defer cancel()

	displays, err := sckit.ListDisplays(ctx)
	if err != nil || len(displays) == 0 {
		return die("no displays: %v", err)
	}
	d := displays[0]
	fmt.Printf("sckit benchmark — display %d (%dx%d), sckit-go v%s\n\n",
		d.ID, d.Width, d.Height, sckit.Version)

	// ─── 1. One-shot display capture ─────────────
	fmt.Println("1. One-shot display capture (5 runs)")
	timings := benchNCaptures(ctx, d, 5)
	printSummary(timings)

	// ─── 2. Stream open time ─────────────────────
	fmt.Println("\n2. Stream open (NewStream cold) × 5")
	openTimings := make([]time.Duration, 0, 5)
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		s, err := sckit.NewStream(ctx, d, sckit.WithFrameRate(60))
		if err != nil {
			fmt.Printf("   ERROR: %v\n", err)
			return 1
		}
		openTimings = append(openTimings, time.Since(t0))
		_ = s.Close()
	}
	printSummary(openTimings)

	// ─── 3. Stream steady state at 60/30/10 fps ──
	for _, fps := range []int{60, 30, 10} {
		fmt.Printf("\n3. Stream steady-state at %d fps (30 frames, 2 warmup)\n", fps)
		times := benchStreamFrames(ctx, d, fps, 30, 2)
		printSummary(times)
		fmt.Printf("   target = %s/frame (%d fps)\n",
			(time.Second / time.Duration(fps)).Round(time.Microsecond), fps)
	}

	// ─── 4. Window capture if one is available ───
	fmt.Println("\n4. Window one-shot capture (5 runs, foreground window)")
	windows, _ := sckit.ListWindows(ctx)
	var winTarget sckit.Window
	for _, w := range windows {
		if w.OnScreen && w.Layer == 0 && w.Frame.Dx() > 200 && w.Frame.Dy() > 200 {
			winTarget = w
			break
		}
	}
	if winTarget.ID == 0 {
		fmt.Println("   skipped — no suitable window")
	} else {
		timings = timings[:0]
		for i := 0; i < 5; i++ {
			t0 := time.Now()
			if _, err := sckit.Capture(ctx, winTarget); err != nil {
				fmt.Printf("   ERROR: %v\n", err)
				break
			}
			timings = append(timings, time.Since(t0))
		}
		fmt.Printf("   (window %s: %q, %dx%d)\n",
			winTarget.App, winTarget.Title, winTarget.Frame.Dx(), winTarget.Frame.Dy())
		printSummary(timings)
	}

	// ─── 5. BGRA→RGBA conversion cost ────────────
	fmt.Println("\n5. BGRA→RGBA conversion (1920×1080, 20 runs)")
	times := benchConversion(1920, 1080, 20)
	printSummary(times)

	fmt.Println("\n✓ bench complete")
	return 0
}

// ─── helpers ─────────────────────────────────────────────────

func benchNCaptures(ctx context.Context, d sckit.Display, n int) []time.Duration {
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := sckit.Capture(ctx, d); err != nil {
			fmt.Printf("   ERROR: %v\n", err)
			break
		}
		out = append(out, time.Since(t0))
	}
	return out
}

func benchStreamFrames(ctx context.Context, d sckit.Display, fps, n, warmup int) []time.Duration {
	s, err := sckit.NewStream(ctx, d, sckit.WithFrameRate(fps))
	if err != nil {
		return nil
	}
	defer func() { _ = s.Close() }()
	for i := 0; i < warmup; i++ {
		fc, c := context.WithTimeout(ctx, 3*time.Second)
		_, _ = s.NextFrameBGRA(fc)
		c()
	}
	times := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		fc, c := context.WithTimeout(ctx, 3*time.Second)
		t0 := time.Now()
		_, err := s.NextFrameBGRA(fc)
		c()
		if err != nil {
			break
		}
		times = append(times, time.Since(t0))
	}
	return times
}

// benchConversion measures Go-side BGRA→RGBA cost alone, without touching SCK.
func benchConversion(w, h, n int) []time.Duration {
	bgra := make([]byte, w*h*4)
	// Fill with a repeating pattern so the compiler doesn't optimize the memory away.
	for i := 0; i < len(bgra); i += 4 {
		bgra[i+0] = byte(i)
		bgra[i+1] = byte(i + 1)
		bgra[i+2] = byte(i + 2)
		bgra[i+3] = 0xFF
	}
	times := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for j := 0; j < len(bgra); j += 4 {
			img.Pix[j+0] = bgra[j+2]
			img.Pix[j+1] = bgra[j+1]
			img.Pix[j+2] = bgra[j+0]
			img.Pix[j+3] = bgra[j+3]
		}
		times = append(times, time.Since(t0))
	}
	return times
}

// printSummary writes a one-line summary of a slice of timings
// ("min=... avg=... p50=... p95=... max=...") prefixed with 3 spaces so
// it nests cleanly under a section header.
func printSummary(times []time.Duration) {
	const prefix = "   "
	if len(times) == 0 {
		fmt.Println(prefix + "no data")
		return
	}
	sorted := make([]time.Duration, len(times))
	copy(sorted, times)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total time.Duration
	for _, t := range times {
		total += t
	}
	avg := total / time.Duration(len(times))
	p50 := sorted[len(sorted)/2]
	p95 := sorted[(len(sorted)*95)/100]
	fmt.Printf("%smin=%s  avg=%s  p50=%s  p95=%s  max=%s\n",
		prefix, sorted[0].Round(time.Microsecond),
		avg.Round(time.Microsecond),
		p50.Round(time.Microsecond),
		p95.Round(time.Microsecond),
		sorted[len(sorted)-1].Round(time.Microsecond))
}
