// example-window-capture — capture a single window by ID and write PNG.
//
// Find the target window ID first:
//
//	go run ./cmd/example-window-list
//
// Then:
//
//	go run ./cmd/example-window-capture <window-id> [out.png]
//
// If no window ID is given, captures the topmost normal-layer window of
// the foreground app, which is often what you want.
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var targetID uint32
	out := "window.png"

	// Parse args: any numeric arg becomes the window ID; any non-numeric
	// arg becomes the output path. Order-agnostic.
	for _, a := range os.Args[1:] {
		if id, err := strconv.ParseUint(a, 10, 32); err == nil {
			targetID = uint32(id)
		} else {
			out = a
		}
	}

	// Auto-pick a window if the user didn't specify.
	if targetID == 0 {
		picked, err := pickForeground(ctx)
		if err != nil {
			die(err.Error())
		}
		targetID = picked.ID
		fmt.Printf("Auto-picked window %d (app=%q title=%q %dx%d)\n",
			picked.ID, picked.App, picked.Title,
			picked.Frame.Dx(), picked.Frame.Dy())
	}

	w := sckit.Window{ID: targetID}
	if err := sckit.CaptureToFile(ctx, w, out); err != nil {
		die(err.Error())
	}
	fmt.Printf("Wrote %s\n", out)
}

func pickForeground(ctx context.Context) (sckit.Window, error) {
	windows, err := sckit.ListWindows(ctx)
	if err != nil {
		return sckit.Window{}, err
	}
	// Filter: on-screen, layer 0 (normal content), non-zero size.
	var candidates []sckit.Window
	for _, w := range windows {
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
	// Largest first — usually "the main window".
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
