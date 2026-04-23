// example-window-stream — persistent capture of a single window.
// Benchmarks per-frame latency for 30 frames.
//
// Find the target window ID with example-window-list; omit to
// auto-pick the largest foreground window.
//
//	go run ./cmd/example-window-stream [window-id]
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var targetID uint32
	if len(os.Args) > 1 {
		if id, err := strconv.ParseUint(os.Args[1], 10, 32); err == nil {
			targetID = uint32(id)
		}
	}
	if targetID == 0 {
		w, err := pickForeground(ctx)
		if err != nil {
			die(err.Error())
		}
		targetID = w.ID
		fmt.Printf("Auto-picked window %d (app=%q title=%q %dx%d)\n\n",
			w.ID, w.App, w.Title, w.Frame.Dx(), w.Frame.Dy())
	}

	stream, err := sckit.NewStream(ctx, sckit.Window{ID: targetID},
		sckit.WithFrameRate(60))
	if err != nil {
		die(err.Error())
	}
	defer stream.Close()
	fmt.Printf("Streaming window at %dx%d\n", stream.Width(), stream.Height())

	const N = 30
	var total time.Duration
	for i := 0; i < N; i++ {
		fctx, cancelf := context.WithTimeout(ctx, time.Second)
		t0 := time.Now()
		_, err := stream.NextFrameBGRA(fctx)
		cancelf()
		if err != nil {
			die(fmt.Sprintf("frame %d: %v", i, err))
		}
		el := time.Since(t0)
		total += el
		fmt.Printf("  frame %2d: %s\n", i+1, el)
	}
	fmt.Printf("\nAverage: %s (%.1f fps)\n", total/N, float64(N)/total.Seconds())
}

func pickForeground(ctx context.Context) (sckit.Window, error) {
	ws, err := sckit.ListWindows(ctx)
	if err != nil {
		return sckit.Window{}, err
	}
	var candidates []sckit.Window
	for _, w := range ws {
		if !w.OnScreen || w.Layer != 0 {
			continue
		}
		if w.Frame.Dx() < 100 || w.Frame.Dy() < 100 {
			continue
		}
		candidates = append(candidates, w)
	}
	if len(candidates) == 0 {
		return sckit.Window{}, fmt.Errorf("no foreground windows found")
	}
	sort.Slice(candidates, func(i, j int) bool {
		ai := candidates[i].Frame.Dx() * candidates[i].Frame.Dy()
		aj := candidates[j].Frame.Dx() * candidates[j].Frame.Dy()
		return ai > aj
	})
	return candidates[0], nil
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
