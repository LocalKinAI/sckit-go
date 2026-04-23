//go:build integration

// Integration tests — require the dylib and the "Screen Recording"
// TCC permission. Run via:
//
//	go test -tags integration ./...
//
// They are intentionally behind a build tag because:
//   - CI runners (GitHub Actions) can't grant TCC programmatically,
//     so an untagged run would hang on the first permission prompt.
//   - Developers without permission granted shouldn't be blocked from
//     a quick `go test`.
package sckit

import (
	"context"
	"errors"
	"image"
	"os"
	"testing"
	"time"
)

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ─── Enumeration ─────────────────────────────────────────────

func TestIntListDisplays(t *testing.T) {
	ds, err := ListDisplays(ctxT(t))
	if err != nil {
		t.Fatalf("ListDisplays: %v", err)
	}
	if len(ds) == 0 {
		t.Fatal("expected at least one display")
	}
	for i, d := range ds {
		if d.ID == 0 {
			t.Errorf("display[%d].ID = 0", i)
		}
		if d.Width <= 0 || d.Height <= 0 {
			t.Errorf("display[%d] size = %dx%d", i, d.Width, d.Height)
		}
	}
}

func TestIntListWindows(t *testing.T) {
	ws, err := ListWindows(ctxT(t))
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(ws) == 0 {
		t.Skip("no windows enumerated — skipping")
	}
	onScreen := 0
	for _, w := range ws {
		if w.OnScreen {
			onScreen++
		}
	}
	if onScreen == 0 {
		t.Error("no on-screen windows — highly unlikely")
	}
}

func TestIntListApps(t *testing.T) {
	apps, err := ListApps(ctxT(t))
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) == 0 {
		t.Skip("no apps enumerated")
	}
	// Dedupe sanity: no bundle appears twice.
	seen := map[string]bool{}
	for _, a := range apps {
		if a.BundleID == "" {
			t.Errorf("App with empty bundle: %+v", a)
			continue
		}
		if seen[a.BundleID] {
			t.Errorf("duplicate bundle %q", a.BundleID)
		}
		seen[a.BundleID] = true
	}
}

// ─── One-shot capture ────────────────────────────────────────

func TestIntCaptureDisplay(t *testing.T) {
	ds, err := ListDisplays(ctxT(t))
	if err != nil || len(ds) == 0 {
		t.Skipf("no displays: %v", err)
	}
	img, err := Capture(ctxT(t), ds[0])
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	b := img.Bounds()
	// Width/height often differs from reported Display.Width/Height on
	// Retina (where Display is in points, capture is in pixels). Accept
	// anything ≥ 100×100 as a sanity floor.
	if b.Dx() < 100 || b.Dy() < 100 {
		t.Errorf("capture too small: %v", b)
	}
}

func TestIntCaptureDisplayWithResolution(t *testing.T) {
	ds, err := ListDisplays(ctxT(t))
	if err != nil || len(ds) == 0 {
		t.Skipf("no displays: %v", err)
	}
	img, err := Capture(ctxT(t), ds[0], WithResolution(640, 480))
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if img.Bounds().Dx() != 640 || img.Bounds().Dy() != 480 {
		t.Errorf("WithResolution(640, 480) produced %v", img.Bounds())
	}
}

func TestIntCaptureRegion(t *testing.T) {
	ds, _ := ListDisplays(ctxT(t))
	if len(ds) == 0 {
		t.Skip("no displays")
	}
	d := ds[0]
	region := Region{
		Display: d,
		Bounds:  image.Rect(0, 0, 400, 300),
	}
	img, err := Capture(ctxT(t), region)
	if err != nil {
		t.Fatalf("Region capture: %v", err)
	}
	if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 300 {
		t.Errorf("Region capture size = %v, want 400x300", img.Bounds())
	}
}

// ─── Streams ─────────────────────────────────────────────────

func TestIntStreamDisplay(t *testing.T) {
	ds, _ := ListDisplays(ctxT(t))
	if len(ds) == 0 {
		t.Skip("no displays")
	}
	s, err := NewStream(ctxT(t), ds[0])
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	defer s.Close()
	if s.Width() == 0 || s.Height() == 0 {
		t.Errorf("Stream size unset: %dx%d", s.Width(), s.Height())
	}

	fctx, cancel := context.WithTimeout(ctxT(t), 3*time.Second)
	defer cancel()
	img, err := s.NextFrame(fctx)
	if err != nil {
		t.Fatalf("NextFrame: %v", err)
	}
	if img.Bounds().Dx() != s.Width() || img.Bounds().Dy() != s.Height() {
		t.Errorf("frame bounds %v don't match stream dims %dx%d",
			img.Bounds(), s.Width(), s.Height())
	}
}

func TestIntStreamFrameRateCap(t *testing.T) {
	ds, _ := ListDisplays(ctxT(t))
	if len(ds) == 0 {
		t.Skip("no displays")
	}
	s, err := NewStream(ctxT(t), ds[0], WithFrameRate(10))
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	defer s.Close()

	// Warm up.
	for i := 0; i < 2; i++ {
		fctx, c := context.WithTimeout(ctxT(t), 3*time.Second)
		_, _ = s.NextFrameBGRA(fctx)
		c()
	}

	const N = 10
	start := time.Now()
	for i := 0; i < N; i++ {
		fctx, c := context.WithTimeout(ctxT(t), 3*time.Second)
		_, err := s.NextFrameBGRA(fctx)
		c()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	// 10 frames at 10 fps should take ~1 second; allow 0.8-1.5s range.
	if elapsed < 800*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Errorf("10 frames at WithFrameRate(10) took %s, expected ~1s", elapsed)
	}
}

func TestIntStreamCloseIdempotent(t *testing.T) {
	ds, _ := ListDisplays(ctxT(t))
	if len(ds) == 0 {
		t.Skip("no displays")
	}
	s, err := NewStream(ctxT(t), ds[0])
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// NextFrame on a closed stream must return ErrStreamClosed.
	_, err = s.NextFrame(ctxT(t))
	if !errors.Is(err, ErrStreamClosed) {
		t.Errorf("NextFrame after Close = %v, want ErrStreamClosed", err)
	}
}

// ─── Error paths ─────────────────────────────────────────────

func TestIntCaptureUnknownDisplay(t *testing.T) {
	fake := Display{ID: 0xFFFFFFFE, Width: 1, Height: 1}
	_, err := Capture(ctxT(t), fake)
	if err == nil {
		t.Fatal("expected error for bogus display ID")
	}
	if !errors.Is(err, ErrDisplayNotFound) {
		// The dylib may report differently depending on OS version;
		// accept any non-nil error, but flag the mismatch for inspection.
		t.Logf("got error %v (not ErrDisplayNotFound — acceptable if dylib returns different message)", err)
	}
}

func TestIntCaptureAppMissingBundle(t *testing.T) {
	_, err := Capture(ctxT(t), App{BundleID: "does.not.exist.zzz"})
	if err == nil {
		t.Fatal("expected error for missing bundle")
	}
}

func TestIntNewStreamAppBundleRequired(t *testing.T) {
	_, err := NewStream(ctxT(t), App{}) // empty bundle
	if err == nil {
		t.Fatal("expected error for empty bundle")
	}
}

// ─── Context cancellation ────────────────────────────────────

func TestIntCancelledContextFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	_, err := ListDisplays(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestIntResolvedDylibPathSet(t *testing.T) {
	if err := Load(); err != nil {
		t.Skipf("Load failed: %v", err)
	}
	p := ResolvedDylibPath()
	if p == "" {
		t.Error("ResolvedDylibPath should be set after Load")
	}
}

// ─── Additional coverage ─────────────────────────────────────

func TestIntCaptureToFile(t *testing.T) {
	ds, err := ListDisplays(ctxT(t))
	if err != nil || len(ds) == 0 {
		t.Skipf("no displays: %v", err)
	}
	out := t.TempDir() + "/cap.png"
	if err := CaptureToFile(ctxT(t), ds[0], out); err != nil {
		t.Fatalf("CaptureToFile: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() < 1024 {
		t.Errorf("PNG suspiciously small: %d bytes", st.Size())
	}
}

func TestIntCaptureToFileUnsupportedExt(t *testing.T) {
	ds, _ := ListDisplays(ctxT(t))
	if len(ds) == 0 {
		t.Skip("no displays")
	}
	err := CaptureToFile(ctxT(t), ds[0], "/tmp/ignored.jpg")
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !contains(err.Error(), "unsupported extension") {
		t.Errorf("error doesn't mention extension: %v", err)
	}
}

func TestIntCaptureWindowBestEffort(t *testing.T) {
	ws, err := ListWindows(ctxT(t))
	if err != nil || len(ws) == 0 {
		t.Skip("no windows enumerated")
	}
	var target Window
	for _, w := range ws {
		if w.OnScreen && w.Layer == 0 &&
			w.Frame.Dx() > 200 && w.Frame.Dy() > 200 {
			target = w
			break
		}
	}
	if target.ID == 0 {
		t.Skip("no suitable foreground window")
	}
	img, err := Capture(ctxT(t), target)
	if err != nil {
		t.Fatalf("window capture: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Errorf("empty window capture: %v", img.Bounds())
	}
}

func TestIntFramesChannel(t *testing.T) {
	ds, _ := ListDisplays(ctxT(t))
	if len(ds) == 0 {
		t.Skip("no displays")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := NewStream(ctx, ds[0], WithFrameRate(30))
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	defer s.Close()

	frames, errs := s.Frames(ctx)
	got := 0
	for img := range frames {
		if img.Bounds().Dx() == 0 {
			t.Errorf("zero-width frame")
		}
		got++
		if got >= 5 {
			cancel() // request teardown
		}
	}
	if got < 5 {
		t.Errorf("expected ≥5 frames from channel, got %d", got)
	}
	// err channel should close cleanly (nil on ctx cancel).
	if err := <-errs; err != nil && err != context.Canceled {
		t.Errorf("errs: %v", err)
	}
}

func TestIntWindowStream(t *testing.T) {
	ws, err := ListWindows(ctxT(t))
	if err != nil || len(ws) == 0 {
		t.Skip("no windows")
	}
	var target Window
	for _, w := range ws {
		if w.OnScreen && w.Layer == 0 && w.Frame.Dx() > 300 && w.Frame.Dy() > 300 {
			target = w
			break
		}
	}
	if target.ID == 0 {
		t.Skip("no suitable window")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := NewStream(ctx, target, WithFrameRate(30))
	if err != nil {
		t.Fatalf("NewStream(Window): %v", err)
	}
	defer s.Close()
	fctx, cancelF := context.WithTimeout(ctx, 3*time.Second)
	defer cancelF()
	img, err := s.NextFrame(fctx)
	if err != nil {
		t.Fatalf("NextFrame: %v", err)
	}
	if img.Bounds().Dx() == 0 {
		t.Error("empty window stream frame")
	}
}

func TestIntAppStreamAndCapture(t *testing.T) {
	apps, err := ListApps(ctxT(t))
	if err != nil || len(apps) == 0 {
		t.Skip("no apps")
	}
	// Pick any app with an on-screen window — Finder or Dock are usually safe.
	target := apps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// One-shot.
	if _, err := Capture(ctx, target); err != nil {
		t.Logf("Capture(App) for %s: %v (skipping)", target.BundleID, err)
		t.Skip("App capture failed — probably an offscreen app")
	}
	// Stream.
	s, err := NewStream(ctx, target, WithFrameRate(15))
	if err != nil {
		t.Fatalf("NewStream(App): %v", err)
	}
	defer s.Close()
	fctx, cancelF := context.WithTimeout(ctx, 3*time.Second)
	defer cancelF()
	if _, err := s.NextFrame(fctx); err != nil {
		t.Fatalf("NextFrame: %v", err)
	}
}

func TestIntResolvedDylibPathStable(t *testing.T) {
	if err := Load(); err != nil {
		t.Skipf("Load failed: %v", err)
	}
	a := ResolvedDylibPath()
	b := ResolvedDylibPath()
	if a != b {
		t.Errorf("ResolvedDylibPath changed between calls: %q vs %q", a, b)
	}
}
