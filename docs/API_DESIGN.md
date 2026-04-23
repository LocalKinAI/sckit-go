# API Design — sckit-go v0.1.0

**Purpose**: document the API choices made for v0.1.0, the alternatives
considered, and the reasoning. Once v0.1.0 is tagged, this API is frozen
until v1.0 — breaking changes go through an ADR in `docs/adr/`.

**Audience**: future-Jacky, contributors, and the one HN commenter who
will inevitably ask "why didn't you just do X".

---

## 1. Prior art we studied

### 1a. `mediar-ai/screenpipe` (Rust, 12.8k ⭐)

The reference implementation in the agent-screen space. Rust, full-stack
(capture + OCR + storage + query). API surface (paraphrased from
`screenpipe-vision/src/capture_screenshot_by_window.rs`):

```rust
pub async fn capture_screenshot_by_window(
    app_name: Option<&str>,
    window_name: Option<&str>,
) -> Result<Vec<(DynamicImage, String, String)>>
```

Plus `continuous_capture(...)` that spawns tokio tasks, writes frames to
an mpsc channel.

**What we take**: filter-by-app-name and filter-by-window-name are
necessary primitives. Agent code wants "capture Chrome" more than
"capture window ID 0x1234".

**What we leave**: mpsc channel as primary API. Rust's async is Go's
`<-chan`, but forcing a channel on every consumer is wrong when a lot of
users want "give me one frame now". We offer both (`NextFrame` blocking,
`Frames() <-chan` convenience on top).

### 1b. Apple ScreenCaptureKit (Swift)

```swift
let content = try await SCShareableContent.current
let filter = SCContentFilter(display: display, excludingWindows: [])
let config = SCStreamConfiguration()
let stream = SCStream(filter: filter, configuration: config, delegate: self)
try stream.addStreamOutput(self, type: .screen, sampleHandlerQueue: nil)
try await stream.startCapture()
```

**What we take**:
- `SCContentFilter` abstraction — a "what to capture" object — is the
  right primitive. We model this as our `Target` interface.
- `SCStreamConfiguration` as a separate options object — we model this
  as Go functional options (`WithResolution`, `WithCursor`, …).
- Async/await-native — we use `context.Context` for cancellation.

**What we leave**:
- `delegate:self` pattern — requires an ObjC protocol conformance we
  don't want to expose to Go users.
- Mandatory explicit `addStreamOutput` + `startCapture` two-step — we
  roll both into `NewStream` so the caller doesn't forget.

### 1c. `nut-tree/nut.js` (TypeScript, 2.8k ⭐)

```typescript
await screen.capture("filename.png");
const windows = await getWindows();
const region = new Region(100, 100, 500, 300);
await screen.captureRegion("name.png", region);
```

**What we take**:
- Region capture is a common need. We model as `Target`:
  `sckit.Region{Display: ..., X, Y, W, H}`.
- Convenience: "capture to file" is nice. We offer `sckit.CaptureToFile(ctx, target, path)` as a wrapper, not the primary API.

**What we leave**:
- Implicit global `screen` object. We want everything to be a value
  (testable, no magic). No package-level mutable state except the
  dylib load handle.

### 1d. `kbinani/screenshot` (Go, 2.3k ⭐, the thing we're replacing)

```go
img, err := screenshot.CaptureDisplay(0)
n := screenshot.NumActiveDisplays()
bounds := screenshot.GetDisplayBounds(0)
```

**What we take**: `image.Image` return type — every Go user expects this.
`kbinani` nailed that, and we honor the convention.

**What we leave**: integer display indexing by position. Displays come and
go; we identify by `CGDirectDisplayID` (stable across connect/disconnect).

---

## 2. Comparison matrix

| Concept | screenpipe | Swift SCK | nut.js | **sckit-go (chosen)** |
|---|---|---|---|---|
| "What to capture" | app/window name strings | `SCContentFilter` | methods on `screen` | **`Target` interface** (composable values) |
| Cancellation | tokio async | async/await | Promise | **`context.Context`** |
| One-shot | async fn | `SCScreenshotManager` | `screen.capture()` | **`Capture(ctx, target, opts...)`** |
| Continuous | mpsc channel | delegate callbacks | N/A | **`Stream.NextFrame(ctx)` + opt channel** |
| Pixel format | `DynamicImage` (RGBA) | `CMSampleBuffer` (BGRA) | file or buffer | **`image.Image` + `Frame` BGRA zero-copy** |
| Configuration | struct fields | `SCStreamConfiguration` | method chains | **Functional options** |
| Resource mgmt | `Drop` | ARC | GC | **`io.Closer` + finalizer fallback** |

---

## 3. The `Target` interface — our central abstraction

```go
// Target describes what to capture. Implementations are values; combine
// via sckit.Display{}, sckit.Window{}, sckit.App{}, sckit.Region{}, etc.
//
// Target is a sealed interface: only types in this package can implement
// it. This lets us evolve the C-boundary filter shape without breaking
// external implementors.
type Target interface {
    targetFilter() *contentFilter   // unexported: seals the interface
}

// Concrete Target implementations:

type Display struct {
    ID     uint32  // CGDirectDisplayID
    Width  int
    Height int
    X, Y   int
}

type Window struct {
    ID       uint32  // SCWindow.windowID
    App      string  // owning app name
    BundleID string  // e.g. "com.google.Chrome"
    Title    string
    Frame    image.Rectangle
    OnScreen bool
    Layer    int
}

type App struct {
    BundleID string  // capture ALL windows of this app, composed
    PID      int32
}

type Region struct {
    Display Display         // parent display (required)
    Bounds  image.Rectangle // in display-local points
}

type Exclude struct {
    Target  Target    // wrap any target
    Windows []Window  // windows to mask out
}
```

### Why an interface instead of a discriminated union / sum type?

- Go doesn't have sum types. Options: interface, or a big struct with
  "kind" enum.
- Interface is cleaner. Each concrete type stays simple and only carries
  its own fields.
- Sealed (unexported method) prevents foot-guns — an external package
  can't claim to implement `Target` and pass garbage to the dylib.

### Why `Exclude` wraps instead of being a field?

- Composability. `sckit.Exclude{Target: someDisplay, Windows: [...]}` is
  obviously composable. Adding `Exclude` as a field on every concrete
  Target means mutating each one when we add a new Target later.

---

## 4. Context.Context everywhere blocking

Every function that makes a dylib round-trip (which blocks on
`dispatch_semaphore_wait` in ObjC) takes a `context.Context`.

```go
ListDisplays(ctx context.Context) ([]Display, error)
Capture(ctx context.Context, target Target, opts ...Option) (image.Image, error)
Stream.NextFrame(ctx context.Context) (image.Image, error)
```

### Why?

- Agents need timeouts. A user query might cancel mid-capture.
- `screenpipe` learned this the hard way — its early API had no
  cancellation, later bolted-on `CancellationToken`.
- Swift SCK inherits cooperative cancellation from async/await.
- Go's stdlib has set this precedent since 1.7 (`net/http`, `database/sql`).

### How?

- A goroutine watches `ctx.Done()`. On cancel, it calls
  `sckit_stream_cancel(handle)` on the dylib which sets a flag the ObjC
  side checks and early-exits the `dispatch_semaphore_wait` (via
  `dispatch_semaphore_signal` + a "canceled" flag).
- Consequence: we do need a small additional dylib function
  `sckit_stream_cancel` — will add in Day 4.

---

## 5. Functional options

```go
sckit.Capture(ctx, sckit.Display{ID: 2},
    sckit.WithResolution(1920, 1080),
    sckit.WithCursor(false),
)
```

### Why?

- Struct-with-many-fields (like `SCStreamConfiguration`) means every API
  bump requires all callers to recheck. Options let us add
  `WithColorSpace(...)` in v0.3 without breaking anyone.
- Rob Pike blessed this pattern in 2014 and it's now idiomatic Go.
- `net/http.Server{Addr: ..., Handler: ...}` is fine for a config
  struct, but a capture call is closer to `exec.Command(...)` with
  optional tweaks — functional options win there.

### Options we ship at v0.1.0

```go
func WithResolution(width, height int) Option   // 0,0 = native
func WithFrameRate(fps int) Option              // default 60, used by Stream only
func WithCursor(show bool) Option               // default true
func WithColorSpace(cs ColorSpace) Option       // default ColorSpaceSRGB
func WithQueueDepth(n int) Option               // default 3, used by Stream
```

---

## 6. Pixel-format policy

Two return paths, explicit to the caller:

```go
// Safe / idiomatic: decode to standard RGBA.
img, err := stream.NextFrame(ctx)  // returns image.Image (*image.RGBA under the hood)

// Zero-copy / hot path: raw native BGRA.
frame, err := stream.NextFrameBGRA(ctx)
// frame.Pixels is valid until next call — DO NOT retain.
```

### Why both?

- 99% of Go code wants `image.Image`. That's what `image/png`, `image/jpeg`,
  and every other Go library consumes.
- 1% of code (realtime pipelines feeding GPU, VLM JPEG encoders, OCR
  engines) can't afford the 8 MB allocation + BGRA→RGBA conversion per
  frame. We give them zero-copy by exposing the dylib's internal buffer,
  with clear docs that it's reused.
- Swift SCK has the same split (`CGImage` vs. `CVPixelBuffer`).

### Why not just always return `image.Image`?

- 1920×1080 BGRA → RGBA conversion + allocation is ~12 ms on M1. Over a
  60 FPS stream that's 720 ms/sec of CPU, i.e., ~70% of one core wasted
  on a conversion the caller may be about to throw away anyway (e.g.
  they encode the frame to JPEG immediately, which accepts BGRA directly
  via a color model hint).
- We can offer `Frame.ToImage()` as a convenience when the caller
  changes their mind.

---

## 7. Stream lifecycle

```go
stream, err := sckit.NewStream(ctx, target, opts...)
defer stream.Close()        // io.Closer, idempotent

for {
    img, err := stream.NextFrame(ctx)
    if errors.Is(err, context.Canceled) { break }
    if err != nil { return err }
    process(img)
}
```

### Choices

- `NewStream` blocks until the stream is actually delivering frames (or
  errors). No "started-but-not-ready" zombie state.
- `Close` is idempotent and safe to call from any goroutine. A finalizer
  backstops forgotten streams (logs a warning like
  `net/http.Response.Body`).
- A single stream is NOT safe for concurrent `NextFrame` calls. This
  matches `bufio.Reader`, `sql.Rows`, etc. — the common Go convention.

### Optional channel adapter (convenience)

```go
frames, errs := stream.Frames(ctx)  // spawns a goroutine, closes on Close/ctx
for img := range frames {
    process(img)
}
if err := <-errs; err != nil { ... }
```

Built on top of `NextFrame`, ~30 lines. Given as convenience, not
primary API, so users who want full control stay in the imperative path.

---

## 8. Errors

- Sentinel for common cases: `ErrTimeout`, `ErrPermissionDenied`,
  `ErrDisplayNotFound`, `ErrStreamClosed`.
- Wrapped errors from the dylib: `fmt.Errorf("sckit: capture: %w",
  underlyingDylibErr)`.
- `ErrPermissionDenied` triggers a helpful message: "Grant Screen
  Recording permission in System Settings → Privacy & Security".
- No panics from library code. A dylib load failure returns from `Load()`.

---

## 9. What we explicitly defer past v0.1.0

| Feature | When | Why not now |
|---|---|---|
| Audio capture (`SCStreamOutputTypeAudio`) | v0.3 | Needs separate CMSampleBufferRef path; ships as separate Target |
| Hardware H.264/HEVC encoding | v0.2 | VideoToolbox; perf, not correctness |
| Region capture | v0.2 | Built by wrapping a display Target + CGRect filter; low priority vs. window capture |
| Frame diff detection | never | Out of scope; users compose with `image/draw` |
| Windows / Linux support | never | Different project |
| TCC permission prompt programmatic request | v0.5 | Needs Swift bridging for `NSApplicationSCPresentationOptions`; we can document the manual path for now |

---

## 10. Summary: the API in 20 lines

```go
// Enumerate
sckit.ListDisplays(ctx)       // []Display
sckit.ListWindows(ctx)        // []Window
sckit.ListApps(ctx)           // []App

// One-shot
sckit.Capture(ctx, target, opts...)        // image.Image
sckit.CaptureToFile(ctx, target, path, opts...)  // convenience

// Continuous
stream, _ := sckit.NewStream(ctx, target, opts...)
defer stream.Close()
stream.NextFrame(ctx)         // image.Image
stream.NextFrameBGRA(ctx)     // Frame (zero-copy, reused buffer)
stream.Frames(ctx)            // <-chan image.Image convenience

// Targets (compose with struct literals)
sckit.Display{ID: ...}
sckit.Window{ID: ...}
sckit.App{BundleID: "com.google.Chrome"}
sckit.Region{Display: d, Bounds: image.Rect(...)}
sckit.Exclude{Target: t, Windows: []Window{...}}

// Options
sckit.WithResolution(w, h)
sckit.WithFrameRate(fps)
sckit.WithCursor(show)
sckit.WithColorSpace(cs)
sckit.WithQueueDepth(n)
```

**20 lines. 5 types. 10 functions. 5 options.** That's the whole surface
at v0.1.0. If we find ourselves wanting to add more before release,
we push back.
