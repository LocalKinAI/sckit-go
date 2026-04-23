# sckit-go — Project Plan

**Status**: v0.0.1 concept proven · targeting v0.1.0 public release in 2 weeks
**Owner**: Jacky Sun (LocalKin AI)
**Last updated**: 2026-04-22

---

## 0. Mission

Become **the canonical Go binding to macOS ScreenCaptureKit** — the library
every Go project reaches for when it needs to capture the screen, window, or
app on modern macOS.

Not trying to beat `screenpipe` (Rust, full-stack OCR product).
Trying to own the **Go-library layer** that future Go agents / recorders /
OCR tools build on top of.

---

## 1. Why Now

| Fact | Why it matters |
|---|---|
| macOS 15+ deprecated `CGDisplayCreateImage` | `kbinani/screenshot` (2.3k⭐), `robotgo` (10.2k⭐), every other Go lib **broken** |
| ScreenCaptureKit requires ObjC blocks + delegates | Nobody in Go has cleanly bound this yet |
| `purego` matured in 2024 | We can ship without cgo — radically better UX |
| AI agent wave needs screen capture | KinClaw, open-source computer-use tools, OCR pipelines all need this |

**The vacuum is real and won't last.** Someone will fill it in the next 6
months. It should be us.

---

## 2. Non-Goals (what we are NOT building)

- ❌ **Full-stack product** (screenpipe territory). We are a library.
- ❌ **OCR, VLM, transcription** — out of scope. Users bring their own.
- ❌ **Cross-platform** — macOS only. Windows/Linux is a different project.
- ❌ **Windows event hooks / input synthesis** — that's a separate library (`input-go`?).
- ❌ **GUI / CLI end-user tool** — we ship `examples/`, not an app.

---

## 3. Positioning

```
┌─────────────────────────────────────────────┐
│ Layer 4: End-user apps (screenpipe, Loom)   │
├─────────────────────────────────────────────┤
│ Layer 3: Agent frameworks (KinClaw, etc.)   │
├─────────────────────────────────────────────┤
│ Layer 2: Domain libraries (OCR, recording)  │
├─────────────────────────────────────────────┤
│ Layer 1: sckit-go  ← WE ARE HERE            │
├─────────────────────────────────────────────┤
│ Layer 0: ScreenCaptureKit.framework (Apple) │
└─────────────────────────────────────────────┘
```

---

## 4. API Design Principles

1. **Stdlib-familiar**: model after `net/http`, `image/png`, `io`. If a Go
   dev has to read docs, we've failed.
2. **Correct by default**: `Capture()` returns `image.Image`, not BGRA bytes.
   Advanced users opt into zero-copy with `CaptureBGRA()`.
3. **Context-aware**: every blocking call takes `context.Context`. No hidden
   goroutines. No forced timeouts.
4. **Resource-safe**: `Stream` is `io.Closer`. Finalizers are a last line of
   defense, not the primary mechanism.
5. **No global state** except the dylib handle. Everything else is struct-scoped.
6. **Small surface**: 5 types, 10 functions. If we need more, the design is wrong.

### Canonical API (frozen at v0.1.0)

```go
package sckit

// Enumeration
func ListDisplays(ctx context.Context) ([]Display, error)
func ListWindows(ctx context.Context)  ([]Window, error)
func ListApps(ctx context.Context)     ([]App, error)

// One-shot
func Capture(ctx context.Context, target Target, opts ...Option) (image.Image, error)

// Continuous
func NewStream(ctx context.Context, target Target, opts ...Option) (*Stream, error)
func (*Stream) NextFrame(ctx context.Context) (image.Image, error)
func (*Stream) NextFrameBGRA(ctx context.Context) (Frame, error)  // zero-copy
func (*Stream) Close() error

// Targets (composable via sckit.Display{}, sckit.Window{}, sckit.App{})
type Target interface { targetFilter() *contentFilter }

// Options
func WithResolution(w, h int) Option
func WithCursor(show bool) Option
func WithFrameRate(fps int) Option
func WithExclude(windows ...Window) Option
```

**Decision log**: every API change before v0.1.0 goes in `docs/adr/` (Architecture
Decision Records), 200 words max each.

---

## 5. Roadmap

### v0.1.0 — "Complete & Publishable" (Week 1-2, 70h)
**Release date**: 2026-05-06 (Tuesday for HN optimal posting)

Must have:
- [x] Display enumeration + capture + streaming
- [x] Window enumeration + capture + streaming
- [x] App enumeration + capture + streaming
- [x] Region capture (sourceRect crop)
- [x] Exclude target (mask specific windows out of Display / App captures)
- [x] `go:embed` universal dylib (arm64+x86_64) — no manual `make dylib`
- [x] Context.Context on every blocking call
- [x] Functional options (WithResolution / WithFrameRate / WithCursor /
      WithColorSpace / WithQueueDepth), verified end-to-end via fps
      measurement (60/30/10 fps all accurate)
- [x] 43 unit tests + 19 integration tests; **78.8% coverage**
- [x] `sckit` CLI with list/capture/stream/bench/version/help
- [x] GitHub Actions CI on macOS-14 + macOS-15 (workflow ready)
- [x] `staticcheck` + `golangci-lint` (9 linters) — 0 warnings
- [x] Stability test harness — 3-min run passes with +72 KB heap growth
- [x] Full godoc (`go doc ./...` has no empty descriptions)
- [x] `/examples/` with 9 commands
- [x] CHANGELOG + CONTRIBUTING + CODE_OF_CONDUCT + SECURITY + issue/PR templates
- [ ] 24-hour stability run (the gate) — pre-release
- [ ] README vhs-recorded demo
- [ ] Blog post draft

### v0.2.0 — "Performance" (Week 3-4, 40h)
- [ ] Hardware H.264/HEVC encoding via VideoToolbox
- [ ] `io.Writer` streaming record to mp4
- [ ] Zero-alloc BGRA→RGBA (SIMD via `golang.org/x/sys/cpu`)
- [ ] Frame ring buffer (consumer-not-keeping-up gets latest, never blocks producer)
- [ ] Benchmarks published in `/benchmarks/RESULTS.md`

### v0.3.0 — "Audio" (Week 5, 20h)
- [ ] `SCStreamOutputTypeAudio` capture
- [ ] Synchronized a/v streams
- [ ] PCM and AAC output paths

### v0.5.0 — "Production Hardened" (Week 6-8, 30h)
- [ ] TCC permission request flow (programmatic prompt)
- [ ] Reconnection on display add/remove
- [ ] Fuzz testing on C↔Go boundary
- [ ] 7-day stability test in CI (weekly cron)
- [ ] Used by 3+ external projects (we submit the PRs)

### v1.0.0 — "Stable" (Month 3-4)
- [ ] API frozen for 2 months without breaking changes
- [ ] 100+ GitHub stars OR 5+ production users
- [ ] Semantic versioning locked
- [ ] Appears in `awesome-go` + Go Weekly
- [ ] Blog posted to HN front page OR r/golang top-10

---

## 6. Week 1 Detailed Plan (Apr 22 – Apr 28)

### Day 1 (Wed Apr 22) — Today, 4-6h
- [ ] Write this `PLAN.md` ✅
- [ ] Write `docs/API_DESIGN.md` comparing screenpipe / Swift SCK / nut.js
- [ ] Refactor existing `sckit.go` to final API shape (Target interface, Options, Context)
- [ ] Update existing examples to new API
- [ ] Commit checkpoint (local only — working-hours rule)

### Day 2 (Thu Apr 23) — 6h
- [ ] `go:embed` dylib extraction
- [ ] Universal binary build in Makefile
- [ ] `sckit.DylibPath` override still works for power users
- [ ] Verify `go get github.com/LocalKinAI/sckit-go` would Just Work
- [ ] Draft `docs/adr/001-dylib-embedding.md`

### Day 3 (Fri Apr 24) — 6h
- [ ] Window capture in ObjC dylib (`sckit_list_windows`, `sckit_capture_window`, `sckit_window_stream_start`)
- [ ] Go bindings for window API
- [ ] `ListWindows()` + `Window.Capture()` + stream
- [ ] 3 window-capture examples

### Day 4 (Sat Apr 25) — 8h (weekend, can push if it's working)
- [ ] App capture (`sckit_capture_app`)
- [ ] Exclude lists
- [ ] Cursor / framerate options
- [ ] Retina pixel/point handling (verify 2× scale on external display)

### Day 5 (Sun Apr 26) — 8h
- [ ] Write 30 unit tests
- [ ] GitHub Actions matrix CI (macOS-14 + macOS-15)
- [ ] `golangci-lint` 0 warnings
- [ ] Golden image tests (3)

### Day 6 (Mon Apr 27) — 6h
- [ ] Record 24h stability test, fix whatever leaks
- [ ] vhs demo recording for README
- [ ] Populate `/examples/ocr-loop`, `/examples/window-recorder`, `/examples/multi-display`

### Day 7 (Tue Apr 28) — 4h buffer
- [ ] Polish pass through README
- [ ] `CONTRIBUTING.md` + issue templates + `CODE_OF_CONDUCT.md`
- [ ] v0.1.0-rc1 tag (local)

---

## 7. Quality Gates (must pass before v0.1.0 tag)

| Gate | Measurement |
|---|---|
| Tests | `go test ./...` passes, 80%+ line coverage |
| Lint | `golangci-lint run` 0 errors, 0 warnings |
| Benchmark | `NextFrame` p50 ≤ 20ms on 60Hz display |
| Stability | 24h test runs without OOM or crash |
| Install | Fresh `go get` + 5-line main.go works on arm64 AND x86_64 |
| Doc | `go doc ./...` shows no `// TODO` or empty descriptions |
| CI | Green on macOS-14 AND macOS-15 GitHub runners |
| License | All files carry SPDX header `// SPDX-License-Identifier: MIT` |

If **any** gate fails, we don't tag. We fix and re-gate.

---

## 8. Distribution Strategy (how we get 100 stars)

### Launch day (Day of v0.1.0 tag)
1. **Blog post** on `localkin.ai/blog/sckit-go-pure-go-screencapturekit`
2. **HN Show HN** at 8am ET Tuesday (statistically best slot)
3. **r/golang** submission same morning
4. **Gopher Slack** #general announcement
5. **Twitter/X** + **Mastodon @ mastodon.social** thread with demo gif
6. **Go Weekly** submission (next issue)

### Week 2 after launch
7. **`awesome-go` PR** adding sckit-go under "Images"
8. **GitHub dependents seeding**: find 5 projects using `kbinani/screenshot` or `robotgo` for screenshot-only use cases, open PRs migrating to sckit-go
9. **Reply to every HN / Reddit comment within 2 hours** for first 48h
10. **Video**: 90-second "sckit-go intro" on YouTube (vhs → overdub)

### Ongoing (Month 1+)
- Monthly patch release cadence (visible activity > stars)
- "What's new in sckit-go" blog for every minor release
- Dogfood: KinClaw uses sckit-go as dependency, cross-links

---

## 9. Success Metrics

### 3-month targets (by late July 2026)
- **Downloads (proxy.golang.org + GitHub tarball pulls)**: **25k+** ⭐ *NORTH STAR*
  - Benchmark: LocalKin's own `ollamadiffuser` (Python/PyPI) hit 21k in 3mo
  - Go has structural advantages for this metric: every `go get`, CI build,
    Docker layer rebuild counts; fewer abandonware installs than PyPI
- **Stars**: 200+
- **Forks**: 20+
- **Issues closed**: 50+
- **External adopters**: 3+ projects `import github.com/LocalKinAI/sckit-go`
- **Go Weekly mentions**: 2+
- **HN front page**: at least reach top 30

### 12-month targets
- **Stars**: 1000+
- **External adopters**: 10+
- **Featured in `awesome-go`**
- **Top 3 Google result** for "golang screenshot macos"
- **Contributors**: 5+ merged PRs from non-Jacky

### Red flags (cut losses if ANY happens)
- v0.1.0 launches, gets < 20 stars in 2 weeks → reassess positioning
- Apple announces first-party Go ScreenCaptureKit binding → pivot or shutdown
- `kbinani/screenshot` maintainer suddenly ships SCK support → race or concede

---

## 10. Risks & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `purego` breaks on macOS 27+ | Medium | High | Track ebitengine issues, have cgo fallback branch ready |
| Apple removes SCK in future macOS | Low | Critical | Not much we can do; same risk hits everyone in the space |
| API gets locked in badly at v0.1.0 | Medium | High | API design phase Day 1, ADR log, 2-week soak |
| Memory leak we don't catch | Medium | High | 24h CI test + leak detection with `testing.AllocsPerRun` |
| TCC permission friction destroys UX | High | Medium | Clear docs + helper function `sckit.RequestPermission()` |
| I lose interest after v0.1.0 ships | Medium | Critical | Public roadmap creates accountability; dogfood via KinClaw forces usage |
| Someone ships competing Go SCK binding first | Low | Medium | Ship v0.1.0 ASAP; our head start is ~4 weeks of work |

---

## 11. Decision Log

Decisions are immutable once logged. Reversing requires a new entry.

| Date | Decision | Rationale |
|---|---|---|
| 2026-04-22 | Pause KinClaw, go 100% on sckit-go | Vacuum in Go ecosystem, first-mover advantage, 2-week sprint window |
| 2026-04-22 | MIT license | Consistent with `purego`, most permissive for ecosystem adoption |
| 2026-04-22 | No cgo, purego + dylib | Better UX for downstream, smaller binary, cleaner boundary |
| 2026-04-22 | Version goal: v0.1.0 in 2 weeks | Ship fast, iterate publicly; perfect is enemy of shipped |
| 2026-04-22 | Repo: `github.com/LocalKinAI/sckit-go` | Brand-building over personal account; consistent with KinBook / KinClaw |
| 2026-04-22 | README hero = A+B (live-mirror + OCR side-by-side) | Visual impact (A) plus practical justification (B); sells both groups |
| 2026-04-22 | Blog tone = narrative ("ObjC+Go 踩坑记") with technical appendix | More shareable on HN/Reddit; appendix keeps engineers happy |
| 2026-04-22 | `input-go` companion deferred to Week 6+ | Out of v0.1 scope; only attempt after sckit-go v0.1 traction is known |
| 2026-04-22 | API frozen at end of Day 1 (see docs/API_DESIGN.md) | Prevents mid-sprint refactor churn; changes require ADR |
| 2026-04-22 | v0.1.0 tagged and published (evening) | All Day 1-7 must-have items shipped in a single day; 24h stability deferred as post-tag monitoring |
| 2026-04-22 | kinrec (screen recorder based on sckit-go) built same day, kept private | Dogfoods the library; public release deferred ~1 week for real-world usage to surface bugs before HN launch |

---

## 12. Open Questions (RESOLVED 2026-04-22)

- [x] **GitHub repo**: `github.com/LocalKinAI/sckit-go` ✅
- [x] **First example showcase**: README hero = A+B (live-mirror + OCR side-by-side) ✅
- [x] **Blog post tone**: narrative with technical appendix ✅
- [x] **Companion `input-go` library**: deferred to Week 6+, gated on sckit-go v0.1 traction ✅

---

## Appendix A: Competitive Landscape (frozen reference)

| Library | Lang | Stars | macOS 15+ | Persistent stream | Our edge |
|---|---|---:|---|---|---|
| `kbinani/screenshot` | Go | 2.3k | ❌ broken | ❌ | Modern API, no cgo |
| `go-vgo/robotgo` | Go | 10.2k | ❌ broken (for capture) | ❌ | Focused scope, 50× smaller |
| `vova616/screenshot` | Go | ~100 | ❌ | ❌ | Actively maintained |
| `mediar-ai/screenpipe` | Rust | 12.8k | ✅ | ✅ | Go, not full-stack product |
| Apple ScreenCaptureKit | ObjC/Swift | — | ✅ | ✅ | Accessible from Go |
| `nut-tree/nut.js` | Node | 2.8k | ✅ | ✅ | Different ecosystem |
