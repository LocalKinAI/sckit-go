// example-window-list — print every visible window on-screen, most
// obvious first (layer 0, onScreen=true). Useful for finding the window
// ID you want to capture.
//
// Run from repo root:
//
//	go run ./cmd/example-window-list
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	windows, err := sckit.ListWindows(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// Stable sort: on-screen first, then by layer (0 = normal content), then by app.
	sort.SliceStable(windows, func(i, j int) bool {
		a, b := windows[i], windows[j]
		if a.OnScreen != b.OnScreen {
			return a.OnScreen // true < false
		}
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		return a.App < b.App
	})

	fmt.Printf("%-8s  %-6s  %-8s  %-20s  %s\n", "id", "layer", "size", "app", "title")
	fmt.Println("--------  ------  --------  --------------------  -----")
	for _, w := range windows {
		if !w.OnScreen {
			continue
		}
		dx := w.Frame.Dx()
		dy := w.Frame.Dy()
		if dx == 0 || dy == 0 {
			continue
		}
		title := w.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		fmt.Printf("%-8d  %-6d  %4dx%-3d  %-20s  %s\n",
			w.ID, w.Layer, dx, dy,
			truncate(w.App, 20),
			title)
	}
	fmt.Printf("\nTotal: %d visible windows (out of %d enumerated)\n",
		countOnScreen(windows), len(windows))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func countOnScreen(ws []sckit.Window) int {
	n := 0
	for _, w := range ws {
		if w.OnScreen {
			n++
		}
	}
	return n
}
