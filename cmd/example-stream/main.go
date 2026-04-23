// example-stream — persistent capture, 30 frames, per-frame latency.
//
// Shows the common NextFrame loop plus ctx cancellation. For the
// zero-copy variant (NextFrameBGRA) see the inline commented line.
//
// Run from repo root:
//
//	go run ./cmd/example-stream
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	displays, err := sckit.ListDisplays(ctx)
	if err != nil {
		die(err)
	}
	if len(displays) == 0 {
		die(fmt.Errorf("no displays attached"))
	}
	d := displays[0]

	stream, err := sckit.NewStream(ctx, d,
		sckit.WithFrameRate(60),
		sckit.WithCursor(true),
	)
	if err != nil {
		die(err)
	}
	defer stream.Close()

	fmt.Printf("Streaming %dx%d\n", stream.Width(), stream.Height())

	const N = 30
	var total time.Duration
	for i := 0; i < N; i++ {
		// Per-frame context lets you bound how long you're willing
		// to wait for the next frame before giving up.
		frameCtx, frameCancel := context.WithTimeout(ctx, 1*time.Second)
		t0 := time.Now()

		// For zero-copy (no BGRA→RGBA alloc) use:
		//   frame, err := stream.NextFrameBGRA(frameCtx)
		//   _ = frame.Pixels // valid until next call
		img, err := stream.NextFrame(frameCtx)
		frameCancel()
		if err != nil {
			die(err)
		}
		el := time.Since(t0)
		total += el
		_ = img // process frame here
		fmt.Printf("  frame %2d: %s\n", i+1, el)
	}

	fmt.Printf("\nAverage: %s (%.1f fps)\n", total/N, float64(N)/total.Seconds())
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
