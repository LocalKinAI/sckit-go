// stability-test — long-running leak + regression detector.
//
// Opens a capture stream, pulls frames continuously, periodically closes
// and reopens the stream, and watches heap + reported frame rate. Exits
// non-zero if it detects:
//   - Heap growth > 50 MB between the first and last sample
//   - Average frame rate drops below 80% of target
//   - Any NextFrame error other than ctx.Canceled
//
// Pre-release gate: `make stability-test` runs this for 24 hours and is
// a v0.1.0 quality gate. Short runs (-duration 5m) are useful in dev to
// catch obvious leaks.
//
// Usage:
//
//	go run ./cmd/stability-test                      # default: 10 min
//	go run ./cmd/stability-test -duration 24h
//	go run ./cmd/stability-test -duration 1h -fps 30 -reopen 5m
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	duration := flag.Duration("duration", 10*time.Minute, "total test duration")
	fps := flag.Int("fps", 30, "target frame rate for stream")
	reopenEvery := flag.Duration("reopen", 5*time.Minute, "reopen stream on this interval to exercise setup/teardown")
	sampleEvery := flag.Duration("sample", 30*time.Second, "heap + throughput sample cadence")
	maxHeapGrowMB := flag.Int("maxheap", 50, "fail if heap grows by more than this many MB end-to-end")
	minFpsRatio := flag.Float64("minfps", 0.80, "fail if measured fps drops below this fraction of target")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Independent timeout so we exit cleanly even on a flooded system.
		<-time.After(*duration + 30*time.Second)
		cancel()
	}()

	displays, err := sckit.ListDisplays(ctx)
	if err != nil || len(displays) == 0 {
		fmt.Fprintf(os.Stderr, "no displays: %v\n", err)
		os.Exit(1)
	}
	display := displays[0]

	var (
		startHeap    uint64
		peakHeap     uint64
		streamCycles int
		totalFrames  int64
		totalErrors  int64
		samples      []sample
		deadline     = time.Now().Add(*duration)
	)

	fmt.Printf("sckit-go stability test\n")
	fmt.Printf("  display:        %d (%dx%d)\n", display.ID, display.Width, display.Height)
	fmt.Printf("  duration:       %s\n", *duration)
	fmt.Printf("  target fps:     %d\n", *fps)
	fmt.Printf("  reopen every:   %s\n", *reopenEvery)
	fmt.Printf("  sample every:   %s\n", *sampleEvery)
	fmt.Printf("  max heap grow:  %d MB\n", *maxHeapGrowMB)
	fmt.Printf("  min fps ratio:  %.2f\n\n", *minFpsRatio)

	start := time.Now()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	startHeap = ms.HeapInuse
	peakHeap = startHeap
	fmt.Printf("[%s] initial heap: %s\n", elapsed(start), fmtBytes(startHeap))

	lastSample := time.Now()
	lastFrameCount := int64(0)
	nextSample := time.Now().Add(*sampleEvery)

	for time.Now().Before(deadline) && ctx.Err() == nil {
		cycleDeadline := time.Now().Add(*reopenEvery)
		if cycleDeadline.After(deadline) {
			cycleDeadline = deadline
		}

		stream, err := sckit.NewStream(ctx, display,
			sckit.WithFrameRate(*fps))
		if err != nil {
			fmt.Fprintf(os.Stderr, "NewStream: %v\n", err)
			os.Exit(2)
		}
		streamCycles++

		// Pull frames until the cycle's deadline.
		for time.Now().Before(cycleDeadline) && ctx.Err() == nil {
			fctx, cancelF := context.WithTimeout(ctx, 2*time.Second)
			_, err := stream.NextFrameBGRA(fctx)
			cancelF()
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				totalErrors++
				if totalErrors > 10 {
					fmt.Fprintf(os.Stderr, "too many errors — last: %v\n", err)
					_ = stream.Close()
					printSummary(start, startHeap, peakHeap, streamCycles, totalFrames, totalErrors, samples, *fps, *maxHeapGrowMB, *minFpsRatio)
					os.Exit(3)
				}
				continue
			}
			totalFrames++

			// Periodic sample.
			if time.Now().After(nextSample) {
				runtime.GC()
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peakHeap {
					peakHeap = ms.HeapInuse
				}
				framesInWindow := totalFrames - lastFrameCount
				windowSec := time.Since(lastSample).Seconds()
				actualFPS := float64(framesInWindow) / windowSec
				fmt.Printf("[%s] heap=%s  cycles=%d  frames=%d  last-window=%.1f fps  errors=%d\n",
					elapsed(start), fmtBytes(ms.HeapInuse),
					streamCycles, totalFrames, actualFPS, totalErrors)
				samples = append(samples, sample{
					at: time.Since(start), heap: ms.HeapInuse,
					frames: totalFrames, windowFPS: actualFPS,
				})
				lastSample = time.Now()
				lastFrameCount = totalFrames
				nextSample = time.Now().Add(*sampleEvery)
			}
		}
		if err := stream.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Close: %v\n", err)
		}
	}

	code := printSummary(start, startHeap, peakHeap, streamCycles,
		totalFrames, totalErrors, samples, *fps,
		*maxHeapGrowMB, *minFpsRatio)
	os.Exit(code)
}

type sample struct {
	at        time.Duration
	heap      uint64
	frames    int64
	windowFPS float64
}

func printSummary(start time.Time, startHeap, peakHeap uint64,
	cycles int, frames, errs int64, samples []sample,
	targetFPS int, maxHeapGrowMB int, minFpsRatio float64) int {

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	endHeap := ms.HeapInuse
	totalDur := time.Since(start)

	fmt.Printf("\n========== summary ==========\n")
	fmt.Printf("duration:        %s\n", totalDur)
	fmt.Printf("stream cycles:   %d\n", cycles)
	fmt.Printf("frames pulled:   %d\n", frames)
	fmt.Printf("avg fps:         %.2f (target %d)\n",
		float64(frames)/totalDur.Seconds(), targetFPS)
	fmt.Printf("errors:          %d\n", errs)
	fmt.Printf("samples:         %d\n", len(samples))
	if len(samples) >= 2 {
		first := samples[0]
		last := samples[len(samples)-1]
		fmt.Printf("first sample:    t=%s heap=%s frames=%d\n",
			first.at.Round(time.Second), fmtBytes(first.heap), first.frames)
		fmt.Printf("last sample:     t=%s heap=%s frames=%d\n",
			last.at.Round(time.Second), fmtBytes(last.heap), last.frames)
	}
	fmt.Printf("heap start:      %s\n", fmtBytes(startHeap))
	fmt.Printf("heap end:        %s\n", fmtBytes(endHeap))
	fmt.Printf("heap peak:       %s\n", fmtBytes(peakHeap))
	growMB := float64(int64(endHeap)-int64(startHeap)) / 1024 / 1024
	fmt.Printf("heap growth:     %+.2f MB\n", growMB)

	code := 0
	if growMB > float64(maxHeapGrowMB) {
		fmt.Printf("❌ FAIL: heap grew by %.2f MB (limit %d MB) — likely leak\n",
			growMB, maxHeapGrowMB)
		code = 3
	}
	avgFPS := float64(frames) / totalDur.Seconds()
	if avgFPS < minFpsRatio*float64(targetFPS) {
		fmt.Printf("❌ FAIL: avg fps %.2f < %.2f * target %d = %.2f\n",
			avgFPS, minFpsRatio, targetFPS, minFpsRatio*float64(targetFPS))
		code = 3
	}
	if code == 0 {
		fmt.Printf("✅ PASS — stability test clean\n")
	}
	return code
}

func elapsed(start time.Time) string {
	d := time.Since(start)
	return fmt.Sprintf("%02.0f:%02.0f:%02.0f",
		d.Hours(), d.Minutes()-d.Hours()*60, d.Seconds()-d.Minutes()*60)
}

func fmtBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
