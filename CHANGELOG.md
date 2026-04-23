# Changelog

All notable changes to sckit-go are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-04-22

Initial public release. Pure-Go binding to macOS ScreenCaptureKit; no
cgo required downstream. Five target kinds (display, window, app,
region, exclude), streaming + one-shot capture, sub-20ms frame latency
at 60 fps, `sckit` companion CLI.

### Added

#### Library — capture targets
- `Display`, `Window`, `App`, `Region`, and `Exclude` capture targets,
  all satisfying the sealed `Target` interface.
- `ListDisplays(ctx)` — enumerate attached displays with stable
  `CGDirectDisplayID`.
- `ListWindows(ctx)` — enumerate all visible windows, populated with
  app name, bundle ID, title, frame, layer, on-screen flag, and PID.
- `ListApps(ctx)` — enumerate running apps with at least one on-screen
  window. Deduplicated by bundle ID, derived from `ListWindows` (zero
  additional dylib call).
- `Capture(ctx, target, opts...)` — one-shot screenshot returning
  `image.Image`.
- `CaptureToFile(ctx, target, path, opts...)` — convenience wrapper
  writing PNG.
- `NewStream(ctx, target, opts...)` — open a persistent capture stream.
- `Stream.NextFrame(ctx)` — blocking frame retrieval returning fresh
  `*image.RGBA`.
- `Stream.NextFrameBGRA(ctx)` — zero-copy `Frame` view into the
  internal reused buffer (B,G,R,A; no per-call allocation).
- `Stream.Frames(ctx)` — convenience channel adapter atop `NextFrame`
  for range-style consumption.
- `Stream.Close()` — idempotent, safe from any goroutine; finalizer
  backstop for forgotten streams.

#### Library — options (functional-options pattern)
- `WithResolution(w, h)` — downsample at the dylib (saves IPC + memory).
- `WithFrameRate(fps)` — stream frame rate cap. Verified correct at
  60/30/10 fps via empirical measurement.
- `WithCursor(show)` — include or hide the hardware cursor in frames.
- `WithColorSpace(sRGB | DisplayP3 | BT.709)` — select output color
  space via `SCStreamConfiguration.colorSpaceName`.
- `WithQueueDepth(n)` — `SCStream` internal buffer count, default 3,
  range [1, 8].

#### Library — infrastructure
- Pure-Go, zero-cgo architecture via
  [`github.com/ebitengine/purego`](https://github.com/ebitengine/purego).
- `//go:embed` ships a universal (arm64 + x86_64) 108 KB companion dylib
  inside the module. First call extracts it to
  `~/Library/Caches/sckit-go/<content-hash>/libsckit_sync.dylib` and
  Dlopens from there. `go get` users never need to run `make` or handle
  a C toolchain.
- `DylibPath` override for power users shipping custom-built dylibs.
- `ResolvedDylibPath()` — introspection of the actual path Dlopened,
  useful for diagnostics.
- `context.Context` on every blocking public function.
- Sentinel errors: `ErrTimeout`, `ErrPermissionDenied`,
  `ErrDisplayNotFound`, `ErrStreamClosed`, `ErrNotImplemented`.
- Heuristic TCC denial detection — dylib error messages containing
  "not authorized" / "permission" / "denied" get wrapped with
  `ErrPermissionDenied`.

#### CLI — `cmd/sckit`
- One binary with 6 subcommands: `list`, `capture`, `stream`, `bench`,
  `version`, `help`.
- `sckit list <displays|windows|apps>` — table output by default,
  `--json` for scripting.
- `sckit capture <display|window|app|region>` — auto-generated
  timestamp filenames, `-o FILE` override, `--no-cursor`,
  `--resolution WxH`, `--color srgb|p3|bt709`.
- `sckit stream <display|window|app> [-n N] [--fps N]` — pull N frames
  and report min/avg/p50/p95/p99/max latency.
- `sckit bench` — five-section benchmark suite: one-shot display,
  stream open cost, steady-state at 60/30/10 fps, window one-shot,
  BGRA→RGBA pure-Go conversion.
- `sckit version` — sckit version + Go version + macOS version +
  resolved dylib path + size.
- `sckit help <cmd>` — per-command long-form help with examples.
- Custom arg reorderer so positional args and flags can interleave
  (Go stdlib `flag` stops at the first non-flag by default).
- `--json` output for `list` subcommands enables shell pipelines.
- Zero third-party dependencies — stdlib `flag` only.

#### Testing
- 43 unit tests covering `Target.filter()` for all 5 types, all
  functional options, pixel conversion correctness, ABI size
  assertions, error wrapping, and the `cfgC` marshaling to C layout.
- 19 integration tests (build-tag `integration`) exercising live SCK
  calls — `ListDisplays/Windows/Apps`, `Capture` with all 5 targets,
  `NewStream` with Display/Window/App targets, `Frames` channel,
  `CaptureToFile`, error paths (missing display ID, missing bundle,
  pre-cancelled context), ctx cancellation semantics, and
  frame-rate-cap accuracy.
- `go test -cover` main package coverage: **78.8%**.
- `cmd/stability-test` long-running leak + regression detector. Catches
  heap growth, drops in frame rate, and stream-reopen corruption.

#### Tooling / CI
- `staticcheck`: 0 warnings.
- `golangci-lint` (v2) with 9 linters enabled: errcheck, govet,
  ineffassign, staticcheck, unused, goconst, misspell, unconvert,
  unparam. 0 issues across the main package.
- GitHub Actions CI on `macos-14` and `macos-15` matrix: build, vet,
  unit tests, coverage report, lint.
- Makefile targets: `dylib`, `build`, `test`, `examples`, `cli`,
  `install-cli`, `stability-test`, `stability-24h`, `clean`,
  `clean-all`, `verify`, `help`.
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`.
- GitHub issue templates (bug report + feature request) and PR
  template.

#### Examples
- `cmd/example-capture` — one-shot display capture, CLI.
- `cmd/example-stream` — persistent display stream + latency report.
- `cmd/example-window-list` — window enumeration sorted for readability.
- `cmd/example-window-capture` — window one-shot with auto-pick.
- `cmd/example-window-stream` — window streaming benchmark.
- `cmd/example-region-capture` — region capture with x/y/w/h parsing.
- `cmd/example-app-capture` — app composed capture + `ListApps`.
- `cmd/stability-test` — memory + throughput + reopen cycle checker.
- `cmd/poc-list-displays`, `cmd/poc-capture`, `cmd/poc-stream` — raw
  dylib usage for contributors.

### Performance

On M-series Mac, 1920×1080 display, macOS 26.3:

| Operation | p50 | p95 |
|---|---|---|
| `NextFrame` steady-state @ 60 fps | 17.4 ms | 18.2 ms (= display refresh cap) |
| `NextFrame` steady-state @ 30 fps | 34.0 ms | 41.0 ms |
| `NextFrame` steady-state @ 10 fps | 100.9 ms | 102.0 ms |
| `NewStream` (cold open) | 81 ms | 85 ms |
| `Capture(Display)` one-shot | 132 ms | 225 ms |
| `Capture(Window)` one-shot | 89 ms | 108 ms |
| `ListDisplays` | 45 ms | 75 ms |
| BGRA→RGBA (pure Go, 1920×1080) | 2.4 ms | 2.8 ms |

Minimum observed `NextFrame` call (frame already pending in sink):
**<1 ms** — purego boundary + memcpy only.

### Stability

3-minute test, stream reopened every 45 seconds (4 cycles, 4683
frames):
- Heap start: 664 KB → Heap end: 736 KB (**+72 KB** total).
- Average frame rate: 26 fps (86% of 30 fps target; warmup errors
  during stream reopen account for the gap).
- Zero errors after the first second of each cycle.

`make stability-24h` is the required pre-release gate.

### Architecture

Inside the ObjC dylib:
- 11 exported plain C-ABI functions.
- Shared `sckit_config_t` struct (56 bytes, naturally aligned) mirrors
  the Go `cfgC` layout — extensible via reserved fields without ABI
  breaks.
- Stream sink class (`SCKitFrameSink`) filters
  `SCStreamFrameInfoStatus` so idle/blank frames re-deliver the
  last-known-good buffer — critical fix for static-screen streaming
  surfaced by `cmd/stability-test`.
- `NSApplicationLoad()` one-shot initializer gates window/app capture
  entry points, avoiding the `CGS_REQUIRE_INIT` assertion when called
  from CLI binaries (documented in `docs/adr/003-cgs-init.md`).

### Documentation

- `README.md` — hero, quickstart, usage, targets, options, benchmarks,
  architecture, status, roadmap, competitive comparison, development.
- `PLAN.md` — 12-section launch sprint with success metrics, risks,
  and decision log.
- `docs/API_DESIGN.md` — prior-art comparison (screenpipe, Swift SCK,
  nut.js, kbinani) + rationale for every design choice.
- `docs/adr/001-api-v01.md` — API freeze at v0.1.0.
- `docs/adr/002-dylib-embedding.md` — `//go:embed` over network
  download, Makefile-only, and cgo alternatives.
- `docs/adr/003-cgs-init.md` — `NSApplicationLoad` CGS fix rationale.

### Known limitations

- Audio capture is not implemented (planned for v0.3.0).
- Video file recording is not implemented (planned for v0.2.0).
- Context cancellation can return fast but cannot abort an in-flight
  dylib call; mid-call abort arrives with `sckit_stream_cancel` in
  v0.5.0.
- Intel Mac hardware is unverified; the universal dylib ships x86_64
  slices but no one has exercised them on real Intel hardware yet.
- `Capture(Window)` on Retina displays returns pixels at the backing
  resolution (2× reported point size); this is correct but callers
  should be aware if they compare to `Window.Frame` dimensions.
