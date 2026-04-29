// Package sckit is a pure-Go binding to macOS ScreenCaptureKit.
//
// sckit provides the modern replacement for the deprecated
// [CGDisplayCreateImage] path removed in macOS 15+. It uses
// [github.com/ebitengine/purego] plus a small companion ObjC dylib — no cgo
// required in downstream projects.
//
// # Quick start
//
//	displays, _ := sckit.ListDisplays(ctx)
//	img, _ := sckit.Capture(ctx, displays[0])
//	png.Encode(w, img)
//
// # Persistent stream
//
//	stream, _ := sckit.NewStream(ctx, displays[0], sckit.WithFrameRate(60))
//	defer stream.Close()
//	for {
//	    img, err := stream.NextFrame(ctx)
//	    if err != nil { break }
//	    process(img)
//	}
//
// # Targets
//
// Every capture function takes a [Target] describing what to record.
// Values of [Display], [Window], [App], [Region], and [Exclude] all
// satisfy Target. The interface is sealed; only types in this package
// can implement it.
//
// # Requirements
//
// macOS 14 (Sonoma) or newer. First use triggers the "Screen Recording"
// TCC prompt; grant the permission in System Settings → Privacy &
// Security → Screen Recording, then rerun.
//
// # Dylib placement
//
// sckit ships a universal (arm64+x86_64) companion dylib via
// [go:embed]. On the first call into the package, the embedded bytes
// are extracted to ~/Library/Caches/sckit-go/<hash>/libsckit_sync.dylib
// and Dlopened from there — downstream users never need to manage the
// dylib themselves. Set [DylibPath] to a non-empty value before the
// first call if you ship a custom-built or patched dylib.
//
// [go:embed]: https://pkg.go.dev/embed
package sckit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/LocalKinAI/sckit-go/internal/dylib"
	"github.com/ebitengine/purego"
)

// Version is the semantic-version tag of this package.
// Kept in sync with git tags; updated per release.
const Version = "0.2.0"

// DylibPath is an optional override for the location of libsckit_sync.dylib.
//
// Default behavior (empty DylibPath): sckit extracts its embedded copy of
// the dylib to the user's cache directory (~/Library/Caches/sckit-go/<hash>/
// on macOS) on first use, then Dlopens from there. Downstream users never
// need to manage the dylib themselves.
//
// Set DylibPath to a non-empty string BEFORE the first call into this
// package if you ship a custom-built or patched dylib. Must be set
// before Load — subsequent changes are ignored (Load caches its result).
var DylibPath = ""

// ─── Dylib handle (unexported) ───────────────────────────────

var (
	loadOnce sync.Once
	loadErr  error

	// C ABI function pointers — one per exported dylib symbol.
	// Every entry that takes a sckit_config_t* accepts unsafe.Pointer
	// pointing to a Go cfgC struct (32 bytes, matching ObjC layout).
	listDisplaysFn      func(unsafe.Pointer, int32, unsafe.Pointer, int32) int32
	captureFn           func(uint32, unsafe.Pointer, unsafe.Pointer, int32, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, int32) int32
	streamStartFn       func(uint32, unsafe.Pointer, unsafe.Pointer, int32) uintptr
	streamDimsFn        func(uintptr, unsafe.Pointer, unsafe.Pointer) int32
	streamNextFn        func(uintptr, unsafe.Pointer, int32, int32, unsafe.Pointer, int32) int32
	streamStopFn        func(uintptr) int32
	listWindowsFn       func(unsafe.Pointer, int32, unsafe.Pointer, int32, unsafe.Pointer, unsafe.Pointer, int32) int32
	captureWindowFn     func(uint32, unsafe.Pointer, unsafe.Pointer, int32, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, int32) int32
	windowStreamStartFn func(uint32, unsafe.Pointer, unsafe.Pointer, int32) uintptr
	captureAppFn        func(unsafe.Pointer, uint32, unsafe.Pointer, unsafe.Pointer, int32, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, int32) int32
	appStreamStartFn    func(unsafe.Pointer, uint32, unsafe.Pointer, unsafe.Pointer, int32) uintptr
	ocrImageFn          func(unsafe.Pointer, int32, unsafe.Pointer, int32, unsafe.Pointer, int32) int32
)

// cfgC mirrors ObjC sckit_config_t exactly. 56 bytes, naturally aligned.
// DO NOT reorder fields without also updating objc/sckit_sync.m and
// bumping the ABI via an ADR.
type cfgC struct {
	Width      int32
	Height     int32
	FrameRate  int32
	ShowCursor int32
	QueueDepth int32
	ColorSpace int32
	// Source rect for Region target (0s = no crop / use full target).
	SrcX int32
	SrcY int32
	SrcW int32
	SrcH int32
	// Exclude list for Exclude target. ExcludeIDs points into Go-owned
	// memory that MUST stay alive for the duration of the dylib call
	// (typical pattern: pass a slice from a local variable, held by Go
	// GC until the calling function returns).
	ExcludeIDs unsafe.Pointer // *uint32
	NExclude   int32
	Reserved0  int32
}

// toC renders the Go config + filter details into the wire struct. The
// returned cfgC holds a raw pointer to excludeIDs; callers must keep
// excludeIDs live (pass it as a separate slice on the stack) for the
// duration of the dylib call.
func (c config) toC(f contentFilter, excludeIDs []uint32) cfgC {
	cursor := int32(1)
	if !c.showCursor {
		cursor = 0
	}
	out := cfgC{
		Width:      int32(c.width),
		Height:     int32(c.height),
		FrameRate:  int32(c.frameRate),
		ShowCursor: cursor,
		QueueDepth: int32(c.queueDepth),
		ColorSpace: int32(c.colorSpace),
	}
	if f.kind == filterKindRegion && !f.region.Empty() {
		out.SrcX = int32(f.region.Min.X)
		out.SrcY = int32(f.region.Min.Y)
		out.SrcW = int32(f.region.Dx())
		out.SrcH = int32(f.region.Dy())
	}
	if len(excludeIDs) > 0 {
		out.ExcludeIDs = unsafe.Pointer(&excludeIDs[0])
		out.NExclude = int32(len(excludeIDs))
	}
	return out
}

// ResolvedDylibPath returns the filesystem path that Load used (or would
// use) to Dlopen the dylib. Call after Load for the path actually loaded.
// Intended for debugging — e.g. telling a user where to check permissions.
func ResolvedDylibPath() string {
	resolvedPathMu.RLock()
	defer resolvedPathMu.RUnlock()
	return resolvedPath
}

var (
	resolvedPath   string
	resolvedPathMu sync.RWMutex
)

// Load explicitly loads the companion dylib. It's idempotent: subsequent
// calls return the same cached error (or nil).
//
// Resolution order:
//  1. If DylibPath is non-empty, use it (user override).
//  2. Otherwise, extract the embedded universal dylib to the user's cache
//     directory (~/Library/Caches/sckit-go/<sha256-prefix>/libsckit_sync.dylib
//     on macOS) and Dlopen from there. Extraction is skipped if a file
//     with the matching hash is already present.
//
// Load is called automatically by every public function; the exported
// form exists so applications can fail fast at startup rather than on
// the first capture.
func Load() error {
	loadOnce.Do(func() {
		if runtime.GOOS != "darwin" {
			loadErr = fmt.Errorf("sckit: ScreenCaptureKit is macOS-only (runtime.GOOS=%s)", runtime.GOOS)
			return
		}
		path, err := resolveDylib()
		if err != nil {
			loadErr = err
			return
		}
		resolvedPathMu.Lock()
		resolvedPath = path
		resolvedPathMu.Unlock()

		h, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("sckit: dlopen %q: %w", path, err)
			return
		}
		purego.RegisterLibFunc(&listDisplaysFn, h, "sckit_list_displays")
		purego.RegisterLibFunc(&captureFn, h, "sckit_capture_display")
		purego.RegisterLibFunc(&streamStartFn, h, "sckit_stream_start")
		purego.RegisterLibFunc(&streamDimsFn, h, "sckit_stream_dims")
		purego.RegisterLibFunc(&streamNextFn, h, "sckit_stream_next_frame")
		purego.RegisterLibFunc(&streamStopFn, h, "sckit_stream_stop")
		purego.RegisterLibFunc(&listWindowsFn, h, "sckit_list_windows")
		purego.RegisterLibFunc(&captureWindowFn, h, "sckit_capture_window")
		purego.RegisterLibFunc(&windowStreamStartFn, h, "sckit_window_stream_start")
		purego.RegisterLibFunc(&captureAppFn, h, "sckit_capture_app")
		purego.RegisterLibFunc(&appStreamStartFn, h, "sckit_app_stream_start")
		purego.RegisterLibFunc(&ocrImageFn, h, "sckit_ocr_image")
	})
	return loadErr
}

// resolveDylib returns the path to Dlopen. When DylibPath is set, returns
// it directly after verifying the file exists. Otherwise extracts the
// embedded dylib to the user cache directory.
func resolveDylib() (string, error) {
	if DylibPath != "" {
		if _, err := os.Stat(DylibPath); err != nil {
			return "", fmt.Errorf("sckit: DylibPath override %q: %w", DylibPath, err)
		}
		return DylibPath, nil
	}
	return extractEmbedded()
}

// extractEmbedded writes the embedded dylib bytes to the user cache
// directory (keyed by content hash so multiple sckit versions coexist)
// and returns the path. If a file with the matching hash already
// exists, no write happens — cold boots after the first are effectively
// free.
func extractEmbedded() (string, error) {
	if len(dylib.Bytes) == 0 {
		return "", errors.New("sckit: embedded dylib is empty (build issue — make dylib)")
	}

	// Hash-prefix the cache dir. Multiple sckit-go versions in the same
	// process (vendored deps, etc.) keep separate extracts; corruption
	// is self-healing because a stale file won't match the hash.
	h := sha256.Sum256(dylib.Bytes)
	hashPrefix := hex.EncodeToString(h[:8]) // 16 hex chars is plenty

	baseCache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("sckit: locate cache dir: %w", err)
	}
	cacheDir := filepath.Join(baseCache, "sckit-go", hashPrefix)
	target := filepath.Join(cacheDir, "libsckit_sync.dylib")

	// Fast path: already extracted and byte-identical.
	if existing, err := os.ReadFile(target); err == nil && len(existing) == len(dylib.Bytes) {
		// SHA already matched via hashPrefix path routing; just byte-compare length.
		// (The directory name encodes the content hash, so any file at that
		// path is by construction the right bytes unless something external
		// truncated it.)
		return target, nil
	}

	// Slow path: create dir, write file atomically via temp+rename.
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("sckit: mkdir %s: %w", cacheDir, err)
	}
	tmp, err := os.CreateTemp(cacheDir, "libsckit_sync-*.dylib.tmp")
	if err != nil {
		return "", fmt.Errorf("sckit: create tmp: %w", err)
	}
	tmpName := tmp.Name()
	// cleanup tears down partial state on error. Close/Remove failures
	// themselves are best-effort — the caller already has the real error.
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(dylib.Bytes); err != nil {
		cleanup()
		return "", fmt.Errorf("sckit: write dylib: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		cleanup()
		return "", fmt.Errorf("sckit: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("sckit: close tmp: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("sckit: rename into place: %w", err)
	}
	return target, nil
}

// ─── Sentinel errors ─────────────────────────────────────────

// ErrTimeout is returned when a blocking call exceeded its deadline with
// no data available (not to be confused with context cancellation,
// which returns ctx.Err()).
var ErrTimeout = errors.New("sckit: timeout")

// ErrPermissionDenied is returned when macOS Screen Recording permission
// has not been granted. Direct users to System Settings → Privacy &
// Security → Screen Recording.
var ErrPermissionDenied = errors.New("sckit: screen recording permission denied")

// ErrDisplayNotFound is returned when a target Display.ID does not match
// any currently-attached display.
var ErrDisplayNotFound = errors.New("sckit: display not found")

// ErrStreamClosed is returned when a method is called on a Stream after
// Close.
var ErrStreamClosed = errors.New("sckit: stream closed")

// ErrNotImplemented is returned for Target kinds not yet implemented in
// this release (e.g. Window or App targets before v0.2.0).
var ErrNotImplemented = errors.New("sckit: not implemented in this version")

// ─── Internal helpers ────────────────────────────────────────

// cstr reads a NUL-terminated C string from a Go byte slice.
func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// wrapDylibErr converts a raw dylib error message into a typed Go error
// where possible, preserving the original text.
func wrapDylibErr(op string, raw string) error {
	if raw == "" {
		return fmt.Errorf("sckit: %s: unknown error", op)
	}
	// Heuristic: TCC denial messages contain these phrases across macOS versions.
	if contains(raw, "not authorized") || contains(raw, "permission") || contains(raw, "denied") {
		return fmt.Errorf("sckit: %s: %w (%s)", op, ErrPermissionDenied, raw)
	}
	return fmt.Errorf("sckit: %s: %s", op, raw)
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
