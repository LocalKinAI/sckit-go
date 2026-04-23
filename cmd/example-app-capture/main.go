// example-app-capture — capture every on-screen window of a single
// application (by bundle ID) composed together.
//
//	go run ./cmd/example-app-capture com.google.Chrome chrome.png
//
// If no bundle ID is given, lists all apps with on-screen windows.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if len(os.Args) < 2 {
		listApps(ctx)
		return
	}
	bundleID := os.Args[1]
	out := "app.png"
	if len(os.Args) > 2 {
		out = os.Args[2]
	}

	app := sckit.App{BundleID: bundleID}
	if err := sckit.CaptureToFile(ctx, app, out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s\n", out)
}

func listApps(ctx context.Context) {
	apps, err := sckit.ListApps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	fmt.Printf("Usage: go run ./cmd/example-app-capture <bundle-id> [out.png]\n\n")
	fmt.Printf("%-40s  %-7s  %s\n", "bundle-id", "pid", "name")
	fmt.Println(strings.Repeat("-", 72))
	for _, a := range apps {
		fmt.Printf("%-40s  %-7d  %s\n", a.BundleID, a.PID, a.Name)
	}
}
