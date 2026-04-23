package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func runStream(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sckit stream: need target (display | window | app)")
		return 2
	}
	fs := flag.NewFlagSet("stream", flag.ExitOnError)
	n := fs.Int("n", 30, "number of frames to pull")
	fps := fs.Int("fps", 60, "target frame rate")
	noCursor := fs.Bool("no-cursor", false, "hide cursor")

	// Separate the subtarget (first positional) from flags.
	sub := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	ctx, cancel := newCtx(time.Duration(*n)*time.Second + 10*time.Second)
	defer cancel()

	target, err := parseStreamTarget(ctx, sub, fs.Args())
	if err != nil {
		return die("%v", err)
	}

	opts := []sckit.Option{sckit.WithFrameRate(*fps)}
	if *noCursor {
		opts = append(opts, sckit.WithCursor(false))
	}

	return streamAndReport(ctx, target, *n, *fps, opts)
}

func parseStreamTarget(ctx context.Context, sub string, rest []string) (sckit.Target, error) {
	switch sub {
	case "display", "d":
		var id uint32
		if len(rest) >= 1 {
			v, err := parseUint32(rest[0], "display ID")
			if err != nil {
				return nil, err
			}
			id = v
		}
		return parseDisplay(ctx, id)
	case "window", "w":
		if len(rest) == 0 {
			return nil, fmt.Errorf("stream window: need window ID (try `sckit list windows`)")
		}
		id, err := parseUint32(rest[0], "window ID")
		if err != nil {
			return nil, err
		}
		return sckit.Window{ID: id}, nil
	case "app", "a":
		if len(rest) == 0 {
			return nil, fmt.Errorf("stream app: need bundle ID (try `sckit list apps`)")
		}
		return sckit.App{BundleID: rest[0]}, nil
	default:
		return nil, fmt.Errorf("stream: unknown target %q", sub)
	}
}

func streamAndReport(ctx context.Context, target sckit.Target, n, targetFPS int, opts []sckit.Option) int {
	t0 := time.Now()
	stream, err := sckit.NewStream(ctx, target, opts...)
	if err != nil {
		return die("NewStream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	openDur := time.Since(t0)
	fmt.Printf("stream opened in %s (%dx%d, target %d fps)\n",
		openDur.Round(time.Millisecond), stream.Width(), stream.Height(), targetFPS)

	latencies := make([]time.Duration, 0, n)
	benchStart := time.Now()
	for i := 0; i < n; i++ {
		fctx, c := context.WithTimeout(ctx, 3*time.Second)
		st := time.Now()
		_, err := stream.NextFrameBGRA(fctx)
		el := time.Since(st)
		c()
		if err != nil {
			fmt.Fprintf(os.Stderr, "frame %d: %v\n", i, err)
			break
		}
		latencies = append(latencies, el)
	}
	wall := time.Since(benchStart)

	if len(latencies) == 0 {
		return die("no frames captured")
	}
	printLatencyReport(latencies, wall, targetFPS)
	return 0
}

func printLatencyReport(latencies []time.Duration, wall time.Duration, targetFPS int) {
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, v := range latencies {
		total += v
	}
	avg := total / time.Duration(len(latencies))
	p50 := sorted[len(sorted)/2]
	p95 := sorted[(len(sorted)*95)/100]
	p99 := sorted[(len(sorted)*99)/100]

	fmt.Printf("\nframes:          %d\n", len(latencies))
	fmt.Printf("wall:            %s  (%.1f fps measured)\n",
		wall.Round(time.Millisecond), float64(len(latencies))/wall.Seconds())
	fmt.Printf("per-frame:\n")
	fmt.Printf("  min            %s\n", sorted[0])
	fmt.Printf("  avg            %s\n", avg)
	fmt.Printf("  p50            %s\n", p50)
	fmt.Printf("  p95            %s\n", p95)
	fmt.Printf("  p99            %s\n", p99)
	fmt.Printf("  max            %s\n", sorted[len(sorted)-1])
	fmt.Printf("target:          %d fps (= %s/frame)\n",
		targetFPS, (time.Second / time.Duration(targetFPS)).Round(time.Millisecond))
}
