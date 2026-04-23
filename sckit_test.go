package sckit

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// ─── helpers ─────────────────────────────────────────────────

func TestCstrNulTerminated(t *testing.T) {
	b := []byte{'h', 'e', 'l', 'l', 'o', 0, 'x', 'x', 'x'}
	got := cstr(b)
	if got != "hello" {
		t.Errorf("cstr = %q, want hello", got)
	}
}

func TestCstrNoNul(t *testing.T) {
	b := []byte("world")
	got := cstr(b)
	if got != "world" {
		t.Errorf("cstr no-nul = %q, want world", got)
	}
}

func TestCstrEmpty(t *testing.T) {
	if cstr(nil) != "" {
		t.Error("cstr(nil) should be empty")
	}
	if cstr([]byte{0}) != "" {
		t.Error("cstr([]byte{0}) should be empty")
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello world", "xyz", false},
		{"abc", "", true},
		{"", "x", false},
		{"", "", true},
	}
	for _, tc := range cases {
		if got := contains(tc.s, tc.sub); got != tc.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}

func TestWrapDylibErr(t *testing.T) {
	// Unknown error preserved.
	err := wrapDylibErr("capture", "something broke")
	if err == nil {
		t.Fatal("nil error")
	}
	if !contains(err.Error(), "something broke") {
		t.Errorf("wrapped message lost: %v", err)
	}
	if !contains(err.Error(), "capture") {
		t.Errorf("op label lost: %v", err)
	}
}

func TestWrapDylibErrDetectsPermissionDenied(t *testing.T) {
	for _, msg := range []string{
		"not authorized",
		"The user has denied permission",
		"Screen recording permission required",
	} {
		err := wrapDylibErr("stream_start", msg)
		if !isErrPermissionDenied(err) {
			t.Errorf("msg %q should map to ErrPermissionDenied, got %v", msg, err)
		}
	}
}

func isErrPermissionDenied(err error) bool {
	for err != nil {
		if err == ErrPermissionDenied {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestWrapDylibErrEmpty(t *testing.T) {
	err := wrapDylibErr("x", "")
	if err == nil {
		t.Fatal("nil error for empty raw")
	}
}

// ─── cfgC / toC translation ──────────────────────────────────

func TestCfgCSize(t *testing.T) {
	// Must match ObjC sckit_config_t exactly: 56 bytes.
	if s := unsafe.Sizeof(cfgC{}); s != 56 {
		t.Errorf("sizeof(cfgC) = %d, want 56 — ObjC/Go struct layout drift", s)
	}
}

func TestConfigToCDefaults(t *testing.T) {
	c := defaultConfig()
	out := c.toC(contentFilter{}, nil)
	if out.Width != 0 || out.Height != 0 {
		t.Errorf("default toC should keep size 0 (native); got %dx%d", out.Width, out.Height)
	}
	if out.FrameRate != 60 {
		t.Errorf("FrameRate = %d, want 60", out.FrameRate)
	}
	if out.ShowCursor != 1 {
		t.Errorf("ShowCursor = %d, want 1", out.ShowCursor)
	}
	if out.QueueDepth != 3 {
		t.Errorf("QueueDepth = %d, want 3", out.QueueDepth)
	}
}

func TestConfigToCCursorFalse(t *testing.T) {
	c := defaultConfig()
	c.showCursor = false
	out := c.toC(contentFilter{}, nil)
	if out.ShowCursor != 0 {
		t.Errorf("ShowCursor = %d, want 0 after WithCursor(false)", out.ShowCursor)
	}
}

func TestConfigToCRegion(t *testing.T) {
	c := defaultConfig()
	f := contentFilter{
		kind:   filterKindRegion,
		region: image.Rect(100, 200, 500, 700),
	}
	out := c.toC(f, nil)
	if out.SrcX != 100 || out.SrcY != 200 {
		t.Errorf("SrcX/Y = %d,%d; want 100,200", out.SrcX, out.SrcY)
	}
	if out.SrcW != 400 || out.SrcH != 500 {
		t.Errorf("SrcW/H = %d,%d; want 400,500", out.SrcW, out.SrcH)
	}
}

func TestConfigToCExcludeList(t *testing.T) {
	c := defaultConfig()
	ids := []uint32{10, 20, 30}
	out := c.toC(contentFilter{}, ids)
	if out.NExclude != 3 {
		t.Errorf("NExclude = %d, want 3", out.NExclude)
	}
	if out.ExcludeIDs == nil {
		t.Fatal("ExcludeIDs should point at the slice")
	}
	// Read back through the pointer to verify layout match.
	first := *(*uint32)(out.ExcludeIDs)
	if first != 10 {
		t.Errorf("first exclude ID through pointer = %d, want 10", first)
	}
}

func TestConfigToCEmptyExcludeKeepsNil(t *testing.T) {
	out := defaultConfig().toC(contentFilter{}, nil)
	if out.ExcludeIDs != nil {
		t.Errorf("ExcludeIDs should be nil when no exclusions; got %v", out.ExcludeIDs)
	}
	if out.NExclude != 0 {
		t.Errorf("NExclude = %d, want 0", out.NExclude)
	}
}

// ─── helpers in capture.go ───────────────────────────────────

func TestSliceStr(t *testing.T) {
	pool := []byte("helloworldhi")
	if got := sliceStr(pool, 0, 5); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	if got := sliceStr(pool, 5, 5); got != "world" {
		t.Errorf("got %q, want world", got)
	}
	if got := sliceStr(pool, 10, 2); got != "hi" {
		t.Errorf("got %q, want hi", got)
	}
}

func TestSliceStrEmpty(t *testing.T) {
	if sliceStr([]byte("abc"), 0, 0) != "" {
		t.Error("zero length should be empty")
	}
}

func TestSliceStrOutOfBounds(t *testing.T) {
	// Defensive: out-of-range should return empty, not panic.
	got := sliceStr([]byte("abc"), 0, 999)
	if got != "" {
		t.Errorf("out-of-bounds should be empty defensively, got %q", got)
	}
}

func TestWindowsToIDs(t *testing.T) {
	ws := []Window{{ID: 1}, {ID: 2}, {ID: 3}}
	ids := windowsToIDs(ws)
	if len(ids) != 3 {
		t.Fatalf("len = %d, want 3", len(ids))
	}
	for i, want := range []uint32{1, 2, 3} {
		if ids[i] != want {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], want)
		}
	}
}

func TestWindowsToIDsEmpty(t *testing.T) {
	if windowsToIDs(nil) != nil {
		t.Error("empty slice should map to nil")
	}
	if windowsToIDs([]Window{}) != nil {
		t.Error("empty slice should map to nil")
	}
}

// ─── BGRA→RGBA pixel conversion ──────────────────────────────

func TestBgraToRgbaSinglePixel(t *testing.T) {
	// BGRA pixel: 0x11 0x22 0x33 0xFF  (B=0x11 G=0x22 R=0x33 A=0xFF)
	bgra := []byte{0x11, 0x22, 0x33, 0xFF}
	img := bgraToRGBA(bgra, 1, 1)
	want := []byte{0x33, 0x22, 0x11, 0xFF} // RGBA
	for i := range want {
		if img.Pix[i] != want[i] {
			t.Errorf("RGBA[%d] = 0x%02x, want 0x%02x", i, img.Pix[i], want[i])
		}
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Errorf("bounds wrong: %v", img.Bounds())
	}
}

func TestBgraToRgbaMultiPixel(t *testing.T) {
	// 2x2 checker: red, green / blue, white.
	bgra := []byte{
		0x00, 0x00, 0xFF, 0xFF, // BGRA red
		0x00, 0xFF, 0x00, 0xFF, // BGRA green
		0xFF, 0x00, 0x00, 0xFF, // BGRA blue
		0xFF, 0xFF, 0xFF, 0xFF, // BGRA white
	}
	img := bgraToRGBA(bgra, 2, 2)
	// Check pixel (0,0) is red in RGBA:
	if img.Pix[0] != 0xFF || img.Pix[1] != 0x00 || img.Pix[2] != 0x00 {
		t.Errorf("pixel(0,0) RGBA = %02x%02x%02x, want FF0000", img.Pix[0], img.Pix[1], img.Pix[2])
	}
	// Pixel (1,0) green:
	if img.Pix[4] != 0x00 || img.Pix[5] != 0xFF || img.Pix[6] != 0x00 {
		t.Errorf("pixel(1,0) RGBA = %02x%02x%02x, want 00FF00", img.Pix[4], img.Pix[5], img.Pix[6])
	}
}

// ─── Version / basic init ────────────────────────────────────

func TestVersionConstant(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
}

// ─── DylibPath override / resolve paths (no actual Dlopen) ───

// We can't call Load() from a unit test without actually loading the
// dylib. But we can exercise resolveDylib's "explicit override exists"
// and "embed extracts to cache" code paths by poking its internals.

func TestResolveDylibPathOverrideMissing(t *testing.T) {
	// Saves + restores the global — serialize with other Load-touching tests via t.Helper ordering.
	orig := DylibPath
	t.Cleanup(func() { DylibPath = orig })

	DylibPath = "/nonexistent/path/libfake.dylib"
	_, err := resolveDylib()
	if err == nil {
		t.Fatal("expected error for missing DylibPath override")
	}
	if !contains(err.Error(), "/nonexistent/path/libfake.dylib") {
		t.Errorf("error should include bad path: %v", err)
	}
}

func TestResolveDylibPathOverrideValid(t *testing.T) {
	orig := DylibPath
	t.Cleanup(func() { DylibPath = orig })

	// Write a dummy file — resolveDylib only checks os.Stat, doesn't dlopen.
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "libsckit_sync.dylib")
	if err := os.WriteFile(fake, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	DylibPath = fake
	got, err := resolveDylib()
	if err != nil {
		t.Fatalf("resolveDylib: %v", err)
	}
	if got != fake {
		t.Errorf("resolveDylib = %q, want %q", got, fake)
	}
}

func TestExtractEmbeddedIsIdempotent(t *testing.T) {
	// Two back-to-back calls should both succeed and return the same path
	// (cache hit on the second call — exercise the stat+byte-compare path).
	p1, err := extractEmbedded()
	if err != nil {
		t.Fatalf("extractEmbedded 1: %v", err)
	}
	p2, err := extractEmbedded()
	if err != nil {
		t.Fatalf("extractEmbedded 2: %v", err)
	}
	if p1 != p2 {
		t.Errorf("paths differ across calls: %q vs %q", p1, p2)
	}
	// The file must exist + be >= the size of the embedded bytes.
	info, err := os.Stat(p1)
	if err != nil {
		t.Fatalf("stat %s: %v", p1, err)
	}
	if info.Size() < 1024 {
		t.Errorf("extracted dylib suspiciously small: %d bytes", info.Size())
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	seen := map[string]error{}
	for _, e := range []error{
		ErrTimeout,
		ErrPermissionDenied,
		ErrDisplayNotFound,
		ErrStreamClosed,
		ErrNotImplemented,
	} {
		if seen[e.Error()] != nil {
			t.Errorf("duplicate sentinel message: %q", e.Error())
		}
		seen[e.Error()] = e
	}
}
