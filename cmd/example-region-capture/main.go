// example-region-capture — capture a sub-rectangle of a display.
//
//	go run ./cmd/example-region-capture            # upper-left 800×600
//	go run ./cmd/example-region-capture 100 100 640 480 out.png
package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"strconv"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	x, y, w, h := 0, 0, 800, 600
	out := "region.png"

	args := os.Args[1:]
	// If 4 numeric args present, treat as x y w h.
	if len(args) >= 4 {
		if pv, ok := parseN(args[:4]); ok {
			x, y, w, h = pv[0], pv[1], pv[2], pv[3]
			args = args[4:]
		}
	}
	if len(args) > 0 {
		out = args[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	displays, err := sckit.ListDisplays(ctx)
	if err != nil {
		die(err)
	}
	d := displays[0]

	region := sckit.Region{
		Display: d,
		Bounds:  image.Rect(x, y, x+w, y+h),
	}
	fmt.Printf("Capturing region %dx%d @ (%d,%d) on display %d\n", w, h, x, y, d.ID)

	if err := sckit.CaptureToFile(ctx, region, out); err != nil {
		die(err)
	}
	fmt.Printf("Wrote %s\n", out)
}

func parseN(args []string) ([]int, bool) {
	out := make([]int, len(args))
	for i, a := range args {
		v, err := strconv.Atoi(a)
		if err != nil {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
