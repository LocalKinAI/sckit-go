// sckit — the canonical command-line interface for the sckit-go library.
//
// Install:
//
//	go install github.com/LocalKinAI/sckit-go/cmd/sckit@latest
//
// Then:
//
//	sckit list displays                    # enumerate displays
//	sckit list windows                     # enumerate on-screen windows
//	sckit list apps                        # enumerate running apps
//	sckit capture display                  # screenshot main display → file
//	sckit capture window 28533             # screenshot a window by ID
//	sckit capture app com.google.Chrome    # all Chrome windows composed
//	sckit capture region 0 0 800 600       # region capture
//	sckit stream display -n 60             # pull 60 frames, report latency
//	sckit bench                            # full benchmark suite
//	sckit version
//
// Run `sckit help <command>` for detail.
package main

import (
	"fmt"
	"os"
	"sort"
)

type command struct {
	run   func([]string) int
	short string
	long  string // longer help, printed by `sckit help <cmd>`
}

var cmds map[string]command

// init builds the cmds map. Using init() instead of a package-level
// literal avoids the initialization cycle caused by runHelp referencing
// the map it's being added to.
func init() {
	cmds = map[string]command{
		"list": {
			run:   runList,
			short: "Enumerate displays, windows, or apps",
			long: `sckit list <displays|windows|apps> [flags]

Enumerate capture targets. Default output is a human-readable table; add
--json for a machine-parseable dump that other tools can consume.

Examples:
  sckit list displays
  sckit list windows --all              # include off-screen + menu bar
  sckit list apps --json                # JSON for scripting`,
		},
		"capture": {
			run:   runCapture,
			short: "Take a screenshot",
			long: `sckit capture <display|window|app|region> [target] [flags]

Take a single screenshot of the specified target. The output is a PNG
written to -o (default: ./sckit-<kind>-<timestamp>.png).

Examples:
  sckit capture display                            # main display, auto name
  sckit capture display 2 -o ~/Desktop/disp.png   # display ID 2
  sckit capture window 28533 --no-cursor
  sckit capture app com.google.Chrome
  sckit capture region 0 0 800 600                 # x y w h
  sckit capture region 100 100 640 480 --display 2 -o crop.png`,
		},
		"stream": {
			run:   runStream,
			short: "Pull frames and report per-frame latency",
			long: `sckit stream <display|window|app> [target] [-n N] [flags]

Open a persistent capture stream, pull N frames, print per-frame
latency stats. Used to benchmark or verify throughput.

Examples:
  sckit stream display -n 60
  sckit stream display --fps 30 -n 90
  sckit stream window 28533 -n 30
  sckit stream app com.google.Chrome --fps 10 -n 20`,
		},
		"bench": {
			run:   runBench,
			short: "Full benchmark suite",
			long: `sckit bench

Run a benchmark suite that reports:
  - Single-frame capture latency (display, window)
  - Stream open time
  - Stream steady-state frame latency at 60 / 30 / 10 fps
  - BGRA→RGBA conversion cost

Useful for regression testing after changes or when reporting issues.`,
		},
		"version": {
			run:   runVersion,
			short: "Print version and environment info",
		},
		"help": {
			run:   runHelp,
			short: "Show help",
		},
	}
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runHelp(nil)
		os.Exit(2)
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "sckit: unknown command %q\n\n", args[0])
		_ = runHelp(nil)
		os.Exit(2)
	}
	os.Exit(cmd.run(args[1:]))
}

func runHelp(args []string) int {
	if len(args) == 1 {
		if c, ok := cmds[args[0]]; ok {
			if c.long != "" {
				fmt.Println(c.long)
			} else {
				fmt.Printf("sckit %s — %s\n", args[0], c.short)
			}
			return 0
		}
		fmt.Fprintf(os.Stderr, "sckit: unknown command %q\n", args[0])
		return 2
	}
	fmt.Println(`sckit — capture your Mac screen from the command line.

USAGE:
  sckit <command> [args] [flags]

COMMANDS:`)
	// Stable alphabetical order for help.
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("  %-10s  %s\n", n, cmds[n].short)
	}
	fmt.Println(`
Run 'sckit help <command>' for detailed usage.

DOCS:
  https://pkg.go.dev/github.com/LocalKinAI/sckit-go
  https://github.com/LocalKinAI/sckit-go`)
	return 0
}
