// poc-stream — persistent SCStream benchmark.
//
// Opens a capture stream once, pulls N frames back-to-back, reports
// per-frame latency. This is the real hot path for KinClaw-style
// "observe screen continuously" workloads.
//
// Run:
//
//	cd /Users/jackysun/Documents/Workspace/sckit-go
//	go run ./cmd/poc-stream [num_frames]
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

type sckitDisplay struct {
	DisplayID uint32
	Width     int32
	Height    int32
	FrameX    int32
	FrameY    int32
}

func main() {
	nFrames := 60
	if len(os.Args) > 1 {
		if v, err := strconv.Atoi(os.Args[1]); err == nil && v > 0 {
			nFrames = v
		}
	}

	lib, err := purego.Dlopen("./libsckit_sync.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dlopen: %v\n", err)
		os.Exit(1)
	}

	var listDisplays func(out unsafe.Pointer, max int32, err unsafe.Pointer, errLen int32) int32
	var streamStart func(displayID uint32, w, h int32, err unsafe.Pointer, errLen int32) uintptr
	var streamDims func(handle uintptr, outW, outH unsafe.Pointer) int32
	var streamNext func(handle uintptr, pixels unsafe.Pointer, cap int32, timeoutMs int32, err unsafe.Pointer, errLen int32) int32
	var streamStop func(handle uintptr) int32

	purego.RegisterLibFunc(&listDisplays, lib, "sckit_list_displays")
	purego.RegisterLibFunc(&streamStart, lib, "sckit_stream_start")
	purego.RegisterLibFunc(&streamDims, lib, "sckit_stream_dims")
	purego.RegisterLibFunc(&streamNext, lib, "sckit_stream_next_frame")
	purego.RegisterLibFunc(&streamStop, lib, "sckit_stream_stop")

	// Pick display 0.
	displays := make([]sckitDisplay, 4)
	errBuf := make([]byte, 256)
	n := listDisplays(unsafe.Pointer(&displays[0]), int32(len(displays)),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)))
	if n <= 0 {
		fmt.Fprintf(os.Stderr, "list failed: %s\n", cstr(errBuf))
		os.Exit(1)
	}
	d := displays[0]
	fmt.Printf("→ streaming display %d at %dx%d (requested native)\n", d.DisplayID, d.Width, d.Height)

	// Start stream at native res.
	tStart := time.Now()
	handle := streamStart(d.DisplayID, 0, 0,
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)))
	if handle == 0 {
		fmt.Fprintf(os.Stderr, "stream_start failed: %s\n", cstr(errBuf))
		os.Exit(1)
	}
	var w, h int32
	streamDims(handle, unsafe.Pointer(&w), unsafe.Pointer(&h))
	fmt.Printf("✅ stream opened in %s (effective %dx%d)\n", time.Since(tStart), w, h)

	bufSize := int(w) * int(h) * 4
	pixels := make([]byte, bufSize)

	// Pull N frames.
	latencies := make([]time.Duration, 0, nFrames)
	tBench := time.Now()
	for i := 0; i < nFrames; i++ {
		t0 := time.Now()
		got := streamNext(handle,
			unsafe.Pointer(&pixels[0]), int32(bufSize),
			int32(1000),
			unsafe.Pointer(&errBuf[0]), int32(len(errBuf)))
		el := time.Since(t0)
		if got <= 0 {
			fmt.Fprintf(os.Stderr, "frame %d failed (got=%d): %s\n", i, got, cstr(errBuf))
			break
		}
		latencies = append(latencies, el)
	}
	wallTotal := time.Since(tBench)

	streamStop(handle)
	fmt.Println()

	if len(latencies) == 0 {
		fmt.Fprintln(os.Stderr, "no frames captured")
		os.Exit(1)
	}

	// Stats.
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

	fmt.Printf("Frames:    %d\n", len(latencies))
	fmt.Printf("Wall:      %s  (%.1f fps)\n", wallTotal, float64(len(latencies))/wallTotal.Seconds())
	fmt.Printf("Per-frame latency:\n")
	fmt.Printf("  min      %s\n", sorted[0])
	fmt.Printf("  avg      %s\n", avg)
	fmt.Printf("  p50      %s\n", p50)
	fmt.Printf("  p95      %s\n", p95)
	fmt.Printf("  p99      %s\n", p99)
	fmt.Printf("  max      %s\n", sorted[len(sorted)-1])
	fmt.Printf("First 5:  ")
	for i := 0; i < 5 && i < len(latencies); i++ {
		fmt.Printf("%s ", latencies[i])
	}
	fmt.Println()
	fmt.Printf("Data:      %d bytes/frame × %d = %.1f MB\n",
		bufSize, len(latencies), float64(bufSize*len(latencies))/1024/1024)
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
