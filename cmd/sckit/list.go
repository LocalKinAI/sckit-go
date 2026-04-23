package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

func runList(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sckit list: need subtarget (displays | windows | apps)")
		return 2
	}
	switch args[0] {
	case "displays", "d":
		return runListDisplays(args[1:])
	case "windows", "w":
		return runListWindows(args[1:])
	case "apps", "a":
		return runListApps(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "sckit list: unknown target %q (want displays | windows | apps)\n", args[0])
		return 2
	}
}

// ─── list displays ───────────────────────────────────────────

func runListDisplays(args []string) int {
	fs := flag.NewFlagSet("list displays", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)

	ctx, cancel := newCtx(5 * time.Second)
	defer cancel()

	ds, err := sckit.ListDisplays(ctx)
	if err != nil {
		return die("list displays: %v", err)
	}
	if *asJSON {
		return jsonOrDie(ds)
	}

	t := &table{headers: []string{"id", "size", "origin"}}
	for _, d := range ds {
		t.rows = append(t.rows, []string{
			fmt.Sprintf("%d", d.ID),
			fmt.Sprintf("%dx%d", d.Width, d.Height),
			fmt.Sprintf("(%d, %d)", d.X, d.Y),
		})
	}
	t.print(os.Stdout)
	fmt.Printf("\n%d display(s)\n", len(ds))
	return 0
}

// ─── list windows ────────────────────────────────────────────

func runListWindows(args []string) int {
	fs := flag.NewFlagSet("list windows", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	all := fs.Bool("all", false, "include off-screen + zero-size windows")
	_ = fs.Parse(args)

	ctx, cancel := newCtx(5 * time.Second)
	defer cancel()

	ws, err := sckit.ListWindows(ctx)
	if err != nil {
		return die("list windows: %v", err)
	}

	var filtered []sckit.Window
	for _, w := range ws {
		if !*all {
			if !w.OnScreen {
				continue
			}
			if w.Frame.Dx() == 0 || w.Frame.Dy() == 0 {
				continue
			}
		}
		filtered = append(filtered, w)
	}

	// Sort: layer ascending (normal content first), then by app.
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Layer != filtered[j].Layer {
			return filtered[i].Layer < filtered[j].Layer
		}
		return filtered[i].App < filtered[j].App
	})

	if *asJSON {
		return jsonOrDie(filtered)
	}

	t := &table{headers: []string{"id", "layer", "size", "app", "title"}}
	for _, w := range filtered {
		t.rows = append(t.rows, []string{
			fmt.Sprintf("%d", w.ID),
			fmt.Sprintf("%d", w.Layer),
			fmt.Sprintf("%dx%d", w.Frame.Dx(), w.Frame.Dy()),
			truncate(w.App, 20),
			truncate(w.Title, 60),
		})
	}
	t.print(os.Stdout)
	fmt.Printf("\n%d window(s) (of %d total)\n", len(filtered), len(ws))
	return 0
}

// ─── list apps ───────────────────────────────────────────────

func runListApps(args []string) int {
	fs := flag.NewFlagSet("list apps", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)

	ctx, cancel := newCtx(5 * time.Second)
	defer cancel()

	apps, err := sckit.ListApps(ctx)
	if err != nil {
		return die("list apps: %v", err)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	if *asJSON {
		return jsonOrDie(apps)
	}

	t := &table{headers: []string{"bundle-id", "pid", "name"}}
	for _, a := range apps {
		t.rows = append(t.rows, []string{
			a.BundleID,
			fmt.Sprintf("%d", a.PID),
			a.Name,
		})
	}
	t.print(os.Stdout)
	fmt.Printf("\n%d app(s) with on-screen windows\n", len(apps))
	return 0
}

// ─── JSON helper ─────────────────────────────────────────────

func jsonOrDie(v any) int {
	if err := emitJSON(v); err != nil {
		return die("json encode: %v", err)
	}
	return 0
}
