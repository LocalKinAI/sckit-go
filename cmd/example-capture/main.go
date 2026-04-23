// example-capture — minimal screenshot using the public sckit API.
//
// Run from repo root (so ./libsckit_sync.dylib is findable):
//
//	go run ./cmd/example-capture out.png
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	out := "screenshot.png"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	displays, err := sckit.ListDisplays(ctx)
	if err != nil {
		die(err)
	}
	if len(displays) == 0 {
		die(fmt.Errorf("no displays attached"))
	}
	d := displays[0]
	fmt.Printf("Capturing display %d (%dx%d)\n", d.ID, d.Width, d.Height)

	if err := sckit.CaptureToFile(ctx, d, out); err != nil {
		die(err)
	}
	fmt.Printf("Wrote %s\n", out)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
