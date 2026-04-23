package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LocalKinAI/sckit-go"
)

// ─── Context / exit helpers ──────────────────────────────────

func newCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

func die(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "sckit: "+format+"\n", args...)
	return 1
}

// ─── JSON output helper ──────────────────────────────────────

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ─── Target parsing ──────────────────────────────────────────

// parseDisplay returns the Display matching id, or displays[0] if id==0.
func parseDisplay(ctx context.Context, id uint32) (sckit.Display, error) {
	ds, err := sckit.ListDisplays(ctx)
	if err != nil {
		return sckit.Display{}, err
	}
	if len(ds) == 0 {
		return sckit.Display{}, fmt.Errorf("no displays attached")
	}
	if id == 0 {
		return ds[0], nil
	}
	for _, d := range ds {
		if d.ID == id {
			return d, nil
		}
	}
	return sckit.Display{}, fmt.Errorf("display %d not found (attached: %s)",
		id, displayIDsShort(ds))
}

func displayIDsShort(ds []sckit.Display) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = strconv.FormatUint(uint64(d.ID), 10)
	}
	return strings.Join(parts, ", ")
}

// parseUint32 parses a string as uint32, with a friendly error.
func parseUint32(s, what string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", what, s, err)
	}
	return uint32(v), nil
}

// parseInt parses as int with friendly error.
func parseInt(s, what string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", what, s, err)
	}
	return v, nil
}

// ─── Arg reordering ──────────────────────────────────────────
//
// Go's stdlib flag package stops at the first non-flag argument, which
// breaks commands where positional args come before flags — e.g.
//
//	sckit capture region 100 100 640 480 -o out.png
//
// reorderArgs moves all -f / --flag tokens (including their values) to
// the front, leaving positional args at the end. Handles:
//
//	-f value
//	-f=value
//	--flag value
//	--flag=value
//	-b (boolean short form)
//
// `valueFlags` names the flags that consume a following token as their
// value; all other `-f` tokens are treated as booleans.
func reorderArgs(args []string, valueFlags map[string]bool) []string {
	var head, tail []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			head = append(head, a)
			tail = append(tail, args[i+1:]...)
			return append(head, tail...)
		case strings.HasPrefix(a, "-"):
			if strings.Contains(a, "=") || isBoolFlag(a, valueFlags) {
				head = append(head, a)
			} else {
				head = append(head, a)
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					head = append(head, args[i+1])
					i++
				}
			}
		default:
			tail = append(tail, a)
		}
	}
	return append(head, tail...)
}

func isBoolFlag(flag string, valueFlags map[string]bool) bool {
	name := strings.TrimLeft(flag, "-")
	return !valueFlags[name]
}

// ─── Auto-filename ───────────────────────────────────────────

func autoFilename(kind string) string {
	return fmt.Sprintf("sckit-%s-%s.png", kind, time.Now().Format("20060102-150405"))
}

// ─── Table printer (TSV-style, aligned) ──────────────────────

type table struct {
	headers []string
	rows    [][]string
}

func (t *table) print(w *os.File) {
	cols := len(t.headers)
	widths := make([]int, cols)
	for i, h := range t.headers {
		widths[i] = len(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < cols && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Header row.
	for i, h := range t.headers {
		fmt.Fprintf(w, "%-*s", widths[i]+2, h)
	}
	fmt.Fprintln(w)
	// Separator.
	for i := range t.headers {
		fmt.Fprint(w, strings.Repeat("-", widths[i]), "  ")
		_ = i
	}
	fmt.Fprintln(w)
	// Data rows.
	for _, row := range t.rows {
		for i, cell := range row {
			if i < cols {
				fmt.Fprintf(w, "%-*s", widths[i]+2, cell)
			}
		}
		fmt.Fprintln(w)
	}
}

// truncate cuts a string to max chars, appending … if cut.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

// ─── Options shared across subcommands ───────────────────────

type captureFlags struct {
	output     string
	noCursor   bool
	resolution string // WxH
	color      string // srgb|p3|bt709
}

func (cf *captureFlags) sckitOptions() ([]sckit.Option, error) {
	var opts []sckit.Option
	if cf.noCursor {
		opts = append(opts, sckit.WithCursor(false))
	}
	if cf.resolution != "" {
		parts := strings.Split(cf.resolution, "x")
		if len(parts) != 2 {
			return nil, fmt.Errorf("bad --resolution %q (want WxH)", cf.resolution)
		}
		w, err := parseInt(parts[0], "resolution width")
		if err != nil {
			return nil, err
		}
		h, err := parseInt(parts[1], "resolution height")
		if err != nil {
			return nil, err
		}
		opts = append(opts, sckit.WithResolution(w, h))
	}
	switch strings.ToLower(cf.color) {
	case "", "srgb":
		// default
	case "p3", "displayp3", "display-p3":
		opts = append(opts, sckit.WithColorSpace(sckit.ColorSpaceDisplayP3))
	case "bt709", "709":
		opts = append(opts, sckit.WithColorSpace(sckit.ColorSpaceBT709))
	default:
		return nil, fmt.Errorf("unknown --color %q (want srgb|p3|bt709)", cf.color)
	}
	return opts, nil
}
