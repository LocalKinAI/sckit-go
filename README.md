# sckit-go

**Pure-Go binding to macOS ScreenCaptureKit.** No cgo. Sub-20ms frames.
Display, window, app, region, and exclude-list capture — one library, one CLI.

[![Go Reference](https://pkg.go.dev/badge/github.com/LocalKinAI/sckit-go.svg)](https://pkg.go.dev/github.com/LocalKinAI/sckit-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![macOS 14+](https://img.shields.io/badge/macOS-14+-blue.svg)](https://www.apple.com/macos/)

---

## Why

In macOS 15+ Apple deprecated `CGDisplayCreateImage` — the path every Go
screenshot library has used for a decade
([`kbinani/screenshot`](https://github.com/kbinani/screenshot) 2.3k⭐,
[`go-vgo/robotgo`](https://github.com/go-vgo/robotgo) 10.2k⭐, and friends).
On macOS 26 (Tahoe) it's gone. The replacement is
[ScreenCaptureKit](https://developer.apple.com/documentation/screencapturekit),
which is all-async, ObjC-block-heavy, and historically ugly to call from Go.

`sckit-go` closes that gap:

- **No cgo.** Uses [`ebitengine/purego`](https://github.com/ebitengine/purego)
  to call a small companion ObjC dylib that ships inside the module via
  `//go:embed`. `go get` and you're done.
- **Universal binary.** The embedded dylib runs on both Intel and Apple
  Silicon out of the box.
- **Modern APIs.** Built on `SCStream`, `SCShareableContent`,
  `SCScreenshotManager` (macOS 14+).
- **Sub-20ms frame latency.** Persistent streams hit the display refresh
  rate cap (~17ms at 60Hz, ~8ms at 120Hz).
- **Idiomatic Go.** `context.Context` on every blocking call, `io.Closer`
  resource model, functional options, sealed `Target` interface.
- **OCR + pixel diff in the same kit.** `sckit.OCR(png)` returns
  recognized text regions (Vision framework, on-device, ~50–200 ms).
  `sckit.DiffImages(before, after, 16, 16)` returns a token-cheap
  pixel-delta grid for verifying that an action actually changed the
  screen — no vision-LLM round-trip required for "did anything happen?"

---

## 30-second quickstart — the `sckit` CLI

No Go code required:

```bash
go install github.com/LocalKinAI/sckit-go/cmd/sckit@latest
```

```bash
sckit list displays
sckit list windows --all
sckit list apps --json

sckit capture display                          # main display → auto-named PNG
sckit capture display 2 -o ~/Desktop/disp.png
sckit capture window 28533 --no-cursor
sckit capture app com.google.Chrome            # all Chrome windows composed
sckit capture region 100 100 640 480 -o crop.png

sckit stream display -n 60                     # pull 60 frames, report p50/p95
sckit stream display --fps 30 -n 90
sckit stream window 28533 -n 30
sckit stream app com.google.Chrome --fps 10

sckit bench                                    # full benchmark suite
sckit version
```

Sample `sckit bench` output on M-series, 1920×1080 display, macOS 26.3:

```
1. One-shot display capture
   min=130ms  avg=151ms  p50=132ms  p95=225ms

2. Stream open (cold)
   min=80ms   avg=82ms   p50=81ms   p95=85ms

3. Stream steady-state at 60 fps
   min=16ms   avg=17.3ms p50=17.4ms p95=18.2ms
   target = 16.7ms/frame

5. BGRA→RGBA conversion (1920×1080)
   min=2.2ms  avg=2.4ms  p50=2.4ms  p95=2.8ms
```

---

## Install as a library

```bash
go get github.com/LocalKinAI/sckit-go
```

That's it. The ObjC companion dylib (~147 KB, universal arm64+x86_64) is
embedded via `//go:embed` and auto-extracts to
`~/Library/Caches/sckit-go/<content-hash>/libsckit_sync.dylib` on first
use. No `make`, no `CGO_ENABLED`, no PATH juggling.

Power users shipping custom-built or patched dylibs can override:

```go
sckit.DylibPath = "/usr/local/lib/libsckit_sync.dylib"
```

> **Permission.** First use triggers a macOS "Screen Recording" TCC prompt.
> Grant it in System Settings → Privacy & Security → Screen Recording, then
> re-run.

---

## Usage

### One-shot screenshot

```go
package main

import (
    "context"
    "github.com/LocalKinAI/sckit-go"
)

func main() {
    ctx := context.Background()
    displays, _ := sckit.ListDisplays(ctx)
    sckit.CaptureToFile(ctx, displays[0], "screenshot.png")
}
```

Prefer raw `image.Image`?

```go
img, _ := sckit.Capture(ctx, displays[0])
png.Encode(w, img)
```

### Capture a single window

```go
windows, _ := sckit.ListWindows(ctx)
for _, w := range windows {
    if w.OnScreen && w.App == "Google Chrome" {
        sckit.CaptureToFile(ctx, w, "chrome.png")
        break
    }
}
```

### Capture an entire app (all its windows composed)

```go
chrome := sckit.App{BundleID: "com.google.Chrome"}
sckit.CaptureToFile(ctx, chrome, "chrome.png")
```

### Capture a region (cropped)

```go
region := sckit.Region{
    Display: displays[0],
    Bounds:  image.Rect(100, 100, 900, 700),  // 800×600 crop
}
sckit.CaptureToFile(ctx, region, "crop.png")
```

### Exclude specific windows (hide your own app, etc.)

```go
myWindow := windows[0] // the window you want masked out
target := sckit.Exclude{
    Target:  displays[0],
    Windows: []sckit.Window{myWindow},
}
sckit.CaptureToFile(ctx, target, "desktop-minus-me.png")
```

### Persistent stream (agents, UI automation, mirroring)

```go
stream, err := sckit.NewStream(ctx, displays[0],
    sckit.WithFrameRate(60),
    sckit.WithCursor(true),
)
if err != nil { log.Fatal(err) }
defer stream.Close()

for {
    frameCtx, cancel := context.WithTimeout(ctx, time.Second)
    img, err := stream.NextFrame(frameCtx)
    cancel()
    if errors.Is(err, sckit.ErrTimeout) { continue }
    if err != nil { log.Fatal(err) }
    analyze(img) // *image.RGBA — fresh copy each call
}
```

### Zero-copy BGRA (hot loop)

`NextFrame` allocates an 8MB RGBA buffer per 4K frame. In hot loops where
you'll JPEG-encode or send to a GPU anyway, use `NextFrameBGRA`:

```go
frame, _ := stream.NextFrameBGRA(ctx)
// frame.Pixels is B,G,R,A,... — valid only until the NEXT call on this Stream
gpuUpload(frame.Pixels, frame.Width, frame.Height)
```

### Channel-style convenience

```go
frames, errs := stream.Frames(ctx)
for img := range frames {
    process(img)
}
if err := <-errs; err != nil { log.Fatal(err) }
```

---

## What can I capture?

Five `Target` types, all interchangeable in `Capture` and `NewStream`:

| Target | What it is | Example |
|---|---|---|
| `Display{ID}` | A whole display | `Display{ID: 2}` |
| `Window{ID}` | A single window | `Window{ID: 28533}` |
| `App{BundleID}` | All windows of an app, composed | `App{BundleID: "com.google.Chrome"}` |
| `Region{Display, Bounds}` | A rectangle within a display | `Region{Display: d, Bounds: image.Rect(0, 0, 800, 600)}` |
| `Exclude{Target, Windows}` | Wrap any target, mask windows out | `Exclude{Target: d, Windows: []Window{myWin}}` |

The `Target` interface is sealed (unexported method) — only types in this
package can satisfy it. This lets us evolve the C-boundary filter shape
without worrying about external implementors.

---

## Options

Functional options apply to both `Capture` and `NewStream`:

```go
sckit.WithResolution(1920, 1080) // default: target's native size
sckit.WithFrameRate(30)          // streams only, default 60; display-refresh capped
sckit.WithCursor(false)          // default: true
sckit.WithColorSpace(sckit.ColorSpaceDisplayP3)  // default: sRGB
sckit.WithQueueDepth(5)          // SCStream internal buffer count, default 3
```

---

## Benchmarks

On M-series Mac, 1920×1080 display, macOS 26.3:

| Operation | p50 | p95 | Notes |
|---|---|---|---|
| `NextFrame` steady-state @ 60 fps | **17.4 ms** | 18.2 ms | = 1/60s display cap |
| `NextFrame` steady-state @ 30 fps | **34.0 ms** | 41.0 ms | exactly as configured |
| `NextFrame` steady-state @ 10 fps | **100.9 ms** | 102.0 ms | exactly as configured |
| `NewStream` (cold open) | 81 ms | 85 ms | first call pays ObjC + WindowServer handshake |
| `Capture(Display)` one-shot | 132 ms | 225 ms | includes SCShareableContent enumeration |
| `Capture(Window)` one-shot | 89 ms | 108 ms | SCScreenshotManager + BGRA copy |
| `ListDisplays` | 45 ms | 75 ms | enumerates displays only |
| `ListWindows` | 40 ms | 60 ms | with string pool serialization |
| BGRA→RGBA (pure Go, 1920×1080) | 2.4 ms | 2.8 ms | one conversion per `NextFrame` |

The `NextFrame` p50 floor is the display refresh interval — no library
can go faster than the hardware. On a ProMotion display at 120Hz the
same code hits ~8 ms. Use `NextFrameBGRA` to skip the 2.4ms conversion
when you don't need `image.Image`.

**Stability**: 3-minute test with stream reopens every 45s produces
+72 KB heap growth total. A `make stability-24h` gate runs the full
24-hour leak detector before every release.

---

## Architecture

```
  Go code
    │
    │  purego.RegisterLibFunc  (no cgo, no compiler toolchain needed downstream)
    ▼
  libsckit_sync.dylib  (~147KB universal, //go:embed'd)
    │
    │  11 plain C-ABI functions
    │  dispatch_semaphore wraps async block APIs
    ▼
  ScreenCaptureKit.framework + AppKit (CGS init)
```

### Exported C functions (from `objc/sckit_sync.m`)

| Function | Purpose |
|---|---|
| `sckit_list_displays` | Enumerate attached displays |
| `sckit_list_windows` | Enumerate windows + app/title/bundle strings |
| `sckit_capture_display` | One-shot screenshot of a display |
| `sckit_capture_window` | One-shot screenshot of a single window |
| `sckit_capture_app` | One-shot screenshot of an app's composed windows |
| `sckit_stream_start` | Open persistent stream for a display |
| `sckit_window_stream_start` | Open persistent stream for a window |
| `sckit_app_stream_start` | Open persistent stream for an app |
| `sckit_stream_dims` | Report effective capture width/height |
| `sckit_stream_next_frame` | Block until next frame, copy BGRA out |
| `sckit_stream_stop` | Tear down stream |

Each one uses `dispatch_semaphore_create + signal + wait` to turn
ScreenCaptureKit's completion-handler async style into blocking sync
calls Go can invoke directly. The stream sink is a 40-line ObjC class
implementing `SCStreamOutput`; it filters on `SCStreamFrameInfoStatus`
so Idle/Blank frames re-deliver the last Complete buffer (the right
semantics for static-screen capture).

See [`docs/API_DESIGN.md`](docs/API_DESIGN.md) for the full design
rationale, and [`docs/adr/`](docs/adr/) for the decision log.

### Why not pure purego (no dylib at all)?

You can call `SCShareableContent` class methods from Go via `purego/objc`,
but the methods take ObjC `^(args...)` blocks as callbacks. `purego` has
experimental block support, but wiring up delegate protocol conformance
(`SCStreamOutput`), bridging `CMSampleBuffer`, and locking `CVPixelBuffer`
from Go is ~500 lines of fragile boilerplate. A ~900-line dylib is smaller
than the alternative, faster to audit, and compiles once.

---

## Status

**v0.3.0** (released 2026-05-07) — capture, OCR, and pixel-diff all
shipped. Five target kinds, persistent + one-shot capture, OCR via
Vision framework, `DiffImages` for token-cheap action verification.
API stable; SemVer-protected from here.

| Test | Count | Pass | Coverage |
|---|---|---|---|
| Unit tests | 50+ | ✅ | (pure Go) |
| Integration tests | 19 | ✅ | (needs TCC permission) |
| `go test -cover` main package | — | — | **78.8%** |
| `staticcheck` | — | ✅ 0 warnings | — |
| `golangci-lint` (9 linters) | — | ✅ 0 issues | — |
| 3-min stability (stream reopens × 4) | — | ✅ +72 KB heap | — |

### Platform matrix

| Platform | Arch | Status |
|---|---|---|
| macOS 26 (Tahoe) | arm64 | ✅ Primary dev target |
| macOS 15 (Sequoia) | arm64 | Expected to work (CI target) |
| macOS 14 (Sonoma) | arm64 | Expected to work (CI target) |
| macOS 15/14 | x86_64 | Universal dylib ships x86_64; untested on real hardware |
| macOS 13 and earlier | any | ❌ SCScreenshotManager requires macOS 14+ |

CI runs on `macos-14` + `macos-15` GitHub Actions runners.

---

## Roadmap

### v0.1.0 — Capture (shipped 2026-04-22)
- [x] Display / window / app / region / exclude capture
- [x] Display / window / app streaming
- [x] `go:embed` dylib + universal binary
- [x] Functional options + `context.Context` on every blocking call
- [x] Zero-copy `NextFrameBGRA` + channel adapter `Stream.Frames`
- [x] 43 unit + 19 integration tests, 78.8% coverage
- [x] `sckit` CLI with `list`, `capture`, `stream`, `bench`, `version`
- [x] GitHub Actions CI (macOS 14 + 15)
- [x] `golangci-lint` 0 warnings, stability test harness

### v0.2.0 — On-device OCR (shipped 2026-04-29)
- [x] `sckit.OCR(imageBytes []byte) ([]TextRegion, error)` via
      `VNRecognizeTextRequest` (Vision framework)
- [x] Top-left origin coordinates (matches CGImage / drawing convention)
- [x] Recognition level: Accurate; language correction: on
- [x] No additional dylib export — same companion lib

### v0.3.0 — Pixel-grid diff (shipped 2026-05-07)
- [x] `sckit.DiffImages(a, b, rows, cols) (*DiffGrid, error)` —
      mean-abs-delta per cell, 0–255 scale
- [x] `DiffGrid.Dirty(threshold)` / `BoundingBox(threshold)` /
      `Render(threshold)` (ASCII heatmap for LLM prompts)
- [x] Pure Go — no dylib changes, lifted from kinclaw skill helpers

### v0.4.0 (planned) — Performance + recording
- [ ] Hardware H.264/HEVC encoding via VideoToolbox
- [ ] `io.Writer` streaming: `stream.RecordTo(w, duration)` → mp4
- [ ] SIMD BGRA→RGBA via `golang.org/x/sys/cpu`
- [ ] Benchmark suite in `/benchmarks` with tracked history

### v0.5.0 (planned) — Audio + cancellation
- [ ] `SCStreamOutputTypeAudio` capture
- [ ] Synchronized A/V streams (PCM + AAC)
- [ ] `ctx.Cancel` triggers in-flight dylib abort (`sckit_stream_cancel`)

### v1.0.0 — Stable
- [ ] API frozen for 2+ months without breaking changes
- [ ] 100+ external consumers or 500+ stars
- [ ] Programmatic TCC permission request flow
- [ ] Featured in `awesome-go` / Go Weekly

---

## Comparison: sckit-go vs screenpipe vs kbinani

| | sckit-go | [screenpipe](https://github.com/mediar-ai/screenpipe) | [kbinani/screenshot](https://github.com/kbinani/screenshot) |
|---|---|---|---|
| Language | Go | Rust | Go |
| macOS 15+ support | ✅ | ✅ | ❌ (broken; API removed) |
| Scope | Library (capture only) | Full product (capture + OCR + DB + audio + query) | Library (capture only) |
| Install | `go get` | Install app + Rust | `go get` (but broken) |
| cgo required | ❌ (purego) | N/A | ❌ |
| Window capture | ✅ | ✅ | ❌ |
| App capture | ✅ | ✅ | ❌ |
| Region capture | ✅ | ✅ | (via cropping) |
| Exclude lists | ✅ | ? | ❌ |
| Audio capture | ❌ (v0.5) | ✅ | ❌ |
| OCR / text extraction | ✅ (v0.2 — Vision framework) | ✅ | ❌ |
| Pixel-grid diff | ✅ (v0.3 — `DiffImages`) | ❌ | ❌ |
| 24/7 persistent DB | ❌ (out of scope) | ✅ | ❌ |
| License | MIT | NOASSERTION (custom) | MIT |
| Repo size | ~500 KB | 407 MB | ~200 KB |
| Go ecosystem native | ✅ | ❌ | ✅ (was) |

**sckit-go is Layer 1** (primitive capture). **screenpipe is Layer 4**
(end-user product). We are complementary, not competitors — the right
outcome is for future Go-based products like screenpipe to build on
top of sckit-go.

---

## Development

```bash
git clone https://github.com/LocalKinAI/sckit-go
cd sckit-go

make help              # list all targets
make dylib             # build universal libsckit_sync.dylib
make build             # go build ./...
make test              # unit tests only
make verify            # build + vet + one capture (CI-style smoke)
make examples          # run every example program
make stability-test    # 10-minute leak detector
make stability-24h     # full pre-release gate (24 hours)
make cli               # build ./sckit CLI binary
make install-cli       # install sckit to $GOBIN
```

### Running tests

```bash
# Pure unit tests — no permissions required, runs anywhere:
go test -count=1 ./...

# Integration tests — require Screen Recording permission:
go test -tags integration -count=1 ./...

# Coverage:
go test -tags integration -count=1 -coverprofile=cov.out .
go tool cover -html=cov.out
```

### Linting

```bash
go vet ./...
staticcheck ./...                  # go install honnef.co/go/tools/cmd/staticcheck@latest
golangci-lint run                  # https://golangci-lint.run/welcome/install/
```

---

## License

MIT — see [LICENSE](LICENSE). Contributions welcome under the same license.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) before filing issues or PRs.
See [`SECURITY.md`](SECURITY.md) for security-related reports.
See [`docs/API_DESIGN.md`](docs/API_DESIGN.md) + [`docs/adr/`](docs/adr/)
for design rationale and historical decisions.

---

Built by [LocalKin AI](https://localkin.ai) as the capture layer
for [KinClaw](https://github.com/LocalKinAI/kinclaw) — open-sourced so
nobody else has to rewrite the ScreenCaptureKit binding from scratch.
