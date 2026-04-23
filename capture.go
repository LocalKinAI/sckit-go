package sckit

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// ─── Enumeration ─────────────────────────────────────────────

// ListDisplays returns all currently-attached displays.
func ListDisplays(ctx context.Context) ([]Display, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type raw struct {
		ID   uint32
		W, H int32
		X, Y int32
	}
	buf := make([]raw, 16)
	errBuf := make([]byte, 256)
	n := listDisplaysFn(
		unsafe.Pointer(&buf[0]), int32(len(buf)),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if n < 0 {
		return nil, wrapDylibErr("list_displays", cstr(errBuf))
	}
	out := make([]Display, n)
	for i := int32(0); i < n; i++ {
		r := buf[i]
		out[i] = Display{
			ID:     r.ID,
			Width:  int(r.W),
			Height: int(r.H),
			X:      int(r.X),
			Y:      int(r.Y),
		}
	}
	return out, nil
}

// ListWindows enumerates windows visible to the capture system. The
// result includes off-screen, minimized, and menu-bar windows; filter
// with Window.OnScreen and Window.Layer if you want only the obvious
// user-facing ones.
func ListWindows(ctx context.Context) ([]Window, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// sckit_window_t layout, mirrors objc/sckit_sync.m exactly (56 bytes).
	type raw struct {
		ID        uint32
		FrameX    int32
		FrameY    int32
		FrameW    int32
		FrameH    int32
		PID       int32
		Layer     int32
		OnScreen  int32
		AppOff    uint32
		AppLen    uint32
		BundleOff uint32
		BundleLen uint32
		TitleOff  uint32
		TitleLen  uint32
	}
	const maxWindows = 1024
	const stringCap = 64 * 1024
	buf := make([]raw, maxWindows)
	strPool := make([]byte, stringCap)
	var used int32
	errBuf := make([]byte, 256)

	n := listWindowsFn(
		unsafe.Pointer(&buf[0]), int32(maxWindows),
		unsafe.Pointer(&strPool[0]), int32(stringCap),
		unsafe.Pointer(&used),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if n < 0 {
		return nil, wrapDylibErr("list_windows", cstr(errBuf))
	}
	out := make([]Window, n)
	for i := int32(0); i < n; i++ {
		r := buf[i]
		out[i] = Window{
			ID:       r.ID,
			App:      sliceStr(strPool, r.AppOff, r.AppLen),
			BundleID: sliceStr(strPool, r.BundleOff, r.BundleLen),
			Title:    sliceStr(strPool, r.TitleOff, r.TitleLen),
			Frame: image.Rectangle{
				Min: image.Point{X: int(r.FrameX), Y: int(r.FrameY)},
				Max: image.Point{X: int(r.FrameX + r.FrameW), Y: int(r.FrameY + r.FrameH)},
			},
			OnScreen: r.OnScreen != 0,
			Layer:    int(r.Layer),
			PID:      r.PID,
		}
	}
	return out, nil
}

// sliceStr safely extracts a UTF-8 string from a string pool by offset+length.
// Returns empty string if bounds are invalid (shouldn't happen, defensive).
func sliceStr(pool []byte, off, length uint32) string {
	end := off + length
	if int(end) > len(pool) || length == 0 {
		return ""
	}
	return string(pool[off:end])
}

// ListApps enumerates applications with at least one on-screen window.
// The result is derived from [ListWindows] — deduplicated by bundle
// identifier. BundleID may be empty for privileged system processes.
func ListApps(ctx context.Context) ([]App, error) {
	windows, err := ListWindows(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]*App{}
	for i := range windows {
		w := &windows[i]
		if w.BundleID == "" {
			continue
		}
		if a, ok := seen[w.BundleID]; ok {
			_ = a
			continue
		}
		seen[w.BundleID] = &App{
			BundleID: w.BundleID,
			Name:     w.App,
			PID:      w.PID,
		}
	}
	out := make([]App, 0, len(seen))
	for _, a := range seen {
		out = append(out, *a)
	}
	return out, nil
}

// ─── One-shot capture ────────────────────────────────────────

// Capture takes a single screenshot of the given target and returns it
// as an [image.Image] (concretely an *[image.RGBA]).
//
// Internally this uses SCScreenshotManager (macOS 14+). Supported
// targets: [Display], [Window]. [App] and [Region] return
// [ErrNotImplemented] and arrive in v0.2.0.
func Capture(ctx context.Context, target Target, opts ...Option) (image.Image, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := applyOptions(opts)
	f := target.filter()

	switch f.kind {
	case filterKindDisplay, filterKindRegion:
		return captureDisplay(ctx, f.displayID, cfg, f)
	case filterKindWindow:
		return captureWindow(ctx, f.windowID, cfg, f)
	case filterKindApp:
		return captureApp(ctx, f.bundleID, cfg, f)
	default:
		return nil, fmt.Errorf("sckit: Capture: unknown target kind %d", f.kind)
	}
}

// windowsToIDs extracts a []uint32 of window IDs from a []Window, for
// passing across the C ABI as an exclude list. Returns nil if the slice
// is empty (caller passes nil down as the "no exclusions" case).
func windowsToIDs(ws []Window) []uint32 {
	if len(ws) == 0 {
		return nil
	}
	ids := make([]uint32, len(ws))
	for i := range ws {
		ids[i] = ws[i].ID
	}
	return ids
}

func (k filterKind) String() string {
	switch k {
	case filterKindDisplay:
		return "Display"
	case filterKindWindow:
		return "Window"
	case filterKindApp:
		return "App"
	case filterKindRegion:
		return "Region"
	default:
		return "unknown"
	}
}

// captureWindow runs a one-shot SCScreenshotManager capture for a
// single window. The output buffer is sized conservatively to 4K*4K*4
// bytes — far above typical window sizes but cheap to allocate transiently.
//
// The f argument carries region / exclude hints through, though neither
// currently applies to bare-window capture (they're silently ignored).
func captureWindow(ctx context.Context, windowID uint32, cfg config, f contentFilter) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Window target cannot exclude other windows; ignore excludeWindows if set.
	c := cfg.toC(f, nil)
	// 64 MB upper bound; enough for 4096×4096 BGRA. Grow on demand if a
	// window is somehow larger.
	const initialCap = 64 * 1024 * 1024
	bgra := make([]byte, initialCap)
	errBuf := make([]byte, 256)
	var outW, outH int32
	n := captureWindowFn(windowID,
		unsafe.Pointer(&c),
		unsafe.Pointer(&bgra[0]), int32(len(bgra)),
		unsafe.Pointer(&outW), unsafe.Pointer(&outH),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if n <= 0 {
		msg := cstr(errBuf)
		if outW > 0 && outH > 0 && contains(msg, "buffer too small") {
			needed := int(outW) * int(outH) * 4
			bgra = make([]byte, needed)
			n = captureWindowFn(windowID,
				unsafe.Pointer(&c),
				unsafe.Pointer(&bgra[0]), int32(len(bgra)),
				unsafe.Pointer(&outW), unsafe.Pointer(&outH),
				unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
			)
			if n <= 0 {
				return nil, wrapDylibErr("capture_window", cstr(errBuf))
			}
		} else {
			return nil, wrapDylibErr("capture_window", msg)
		}
	}
	return bgraToRGBA(bgra[:n], int(outW), int(outH)), nil
}

// captureApp runs a one-shot capture that shows only `bundleID`'s windows
// composited onto the display where most of that app lives. The filter
// may include exclusions that further mask specific windows of the app.
func captureApp(ctx context.Context, bundleID string, cfg config, f contentFilter) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bundleID == "" {
		return nil, fmt.Errorf("sckit: Capture(App): bundleID is required")
	}
	excludeIDs := windowsToIDs(f.excludeWindows)
	c := cfg.toC(f, excludeIDs)
	_ = excludeIDs // keep slice alive across dylib call
	// NUL-terminated copy for C ABI.
	bidBytes := append([]byte(bundleID), 0)
	// Size the output buffer to the main display worth of pixels; grow on demand.
	const initialCap = 64 * 1024 * 1024
	bgra := make([]byte, initialCap)
	errBuf := make([]byte, 256)
	var outW, outH int32
	n := captureAppFn(unsafe.Pointer(&bidBytes[0]),
		0, // auto-pick display
		unsafe.Pointer(&c),
		unsafe.Pointer(&bgra[0]), int32(len(bgra)),
		unsafe.Pointer(&outW), unsafe.Pointer(&outH),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if n <= 0 {
		msg := cstr(errBuf)
		if outW > 0 && outH > 0 && contains(msg, "buffer too small") {
			needed := int(outW) * int(outH) * 4
			bgra = make([]byte, needed)
			n = captureAppFn(unsafe.Pointer(&bidBytes[0]), 0,
				unsafe.Pointer(&c),
				unsafe.Pointer(&bgra[0]), int32(len(bgra)),
				unsafe.Pointer(&outW), unsafe.Pointer(&outH),
				unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
			)
			if n <= 0 {
				return nil, wrapDylibErr("capture_app", cstr(errBuf))
			}
		} else {
			return nil, wrapDylibErr("capture_app", msg)
		}
	}
	return bgraToRGBA(bgra[:n], int(outW), int(outH)), nil
}

func captureDisplay(ctx context.Context, displayID uint32, cfg config, f contentFilter) (image.Image, error) {
	// Look up the display to size the output buffer correctly.
	displays, err := ListDisplays(ctx)
	if err != nil {
		return nil, err
	}
	var disp *Display
	for i := range displays {
		if displays[i].ID == displayID {
			disp = &displays[i]
			break
		}
	}
	if disp == nil {
		return nil, fmt.Errorf("sckit: Capture: %w (id=%d)", ErrDisplayNotFound, displayID)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	excludeIDs := windowsToIDs(f.excludeWindows)
	c := cfg.toC(f, excludeIDs)
	_ = excludeIDs // keep slice alive across dylib call

	// Output buffer sizing. Precedence:
	//   1. explicit WithResolution
	//   2. Region.Bounds
	//   3. display native
	bufW, bufH := disp.Width, disp.Height
	if cfg.width > 0 {
		bufW = cfg.width
	} else if f.kind == filterKindRegion && !f.region.Empty() {
		bufW = f.region.Dx()
	}
	if cfg.height > 0 {
		bufH = cfg.height
	} else if f.kind == filterKindRegion && !f.region.Empty() {
		bufH = f.region.Dy()
	}
	bgra := make([]byte, bufW*bufH*4)
	errBuf := make([]byte, 256)
	var outW, outH int32
	n := captureFn(displayID,
		unsafe.Pointer(&c),
		unsafe.Pointer(&bgra[0]), int32(len(bgra)),
		unsafe.Pointer(&outW), unsafe.Pointer(&outH),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if n <= 0 {
		return nil, wrapDylibErr("capture", cstr(errBuf))
	}
	return bgraToRGBA(bgra[:n], int(outW), int(outH)), nil
}

// CaptureToFile captures a single screenshot and writes it to path. The
// output format is chosen by the file extension; currently only .png is
// supported.
func CaptureToFile(ctx context.Context, target Target, path string, opts ...Option) error {
	img, err := Capture(ctx, target, opts...)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("sckit: CaptureToFile: %w", err)
		}
		if err := png.Encode(f, img); err != nil {
			_ = f.Close()
			return fmt.Errorf("sckit: CaptureToFile: encode: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("sckit: CaptureToFile: close: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("sckit: CaptureToFile: unsupported extension %q (supported: .png)", ext)
	}
}

// ─── Pixel conversion ────────────────────────────────────────

// bgraToRGBA copies a tight-packed BGRA buffer into a fresh *image.RGBA.
// w*h*4 must equal len(bgra).
func bgraToRGBA(bgra []byte, w, h int) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	// Byte-order swap B↔R. Alpha and G unchanged.
	// Unrolled 4 bytes per iteration; Go escape analysis keeps this in registers.
	for i := 0; i < len(bgra); i += 4 {
		rgba.Pix[i+0] = bgra[i+2] // R ← BGRA[i+2]
		rgba.Pix[i+1] = bgra[i+1] // G
		rgba.Pix[i+2] = bgra[i+0] // B ← BGRA[i+0]
		rgba.Pix[i+3] = bgra[i+3] // A
	}
	return rgba
}
