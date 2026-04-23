# ADR 003 — NSApplicationLoad for Core Graphics init

**Date**: 2026-04-22
**Status**: Accepted

## Context

During Day 3 window-capture implementation, `sckit_capture_window` and
`sckit_window_stream_start` consistently aborted with:

```
Assertion failed: (did_initialize), function CGS_REQUIRE_INIT,
file CGInitialization.c, line 44.
signal: abort trap
```

when invoked from a plain `go run` CLI binary. The same code path
worked fine for display capture (`sckit_capture_display`).

Root cause: window-scoped SCScreenshotManager paths touch Core
Graphics internals (CGS — Core Graphics Services) that require the
process to have connected to WindowServer. A CLI binary started
without `NSApplicationMain` never establishes that connection, so
CGS's `did_initialize` assertion fires.

Display capture avoids this because its path stays entirely within
IOSurface territory.

## Decision

Add a `sckit_ensure_cg_init()` helper in the dylib that calls
`NSApplicationLoad()` exactly once per process, and invoke it at the
top of every window-scoped entry point.

```objc
static void sckit_ensure_cg_init(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        NSApplicationLoad();
    });
}
```

`NSApplicationLoad()` is lighter than `[NSApplication sharedApplication]`:
it loads the AppKit framework and sets up the minimum state Core
Graphics needs, but does not start an event loop or claim foreground
app status. The Dock icon does not appear.

Link `-framework AppKit` in the Makefile (previously only CoreGraphics
was linked).

## Consequences

### Positive

- Window capture and window streaming work from any process type,
  including `go run` CLI binaries, background daemons, and `go test`
  harnesses.
- No user-facing API change; the fix is entirely inside the dylib.
- Idempotent + cheap (`dispatch_once`).

### Negative

- Adds AppKit as a linked framework (~dozens of MB nominally, but only
  dynamically linked — does not inflate the dylib binary).
- `NSApplicationLoad()` creates an NSApplication instance side-effect.
  This is observable via `[NSApplication sharedApplication]`. Downstream
  embedders who *themselves* want to run `NSApplicationMain` later must
  be aware the sharedApplication already exists. In practice this is
  compatible — AppKit expects a single singleton and we're creating
  that singleton.
- Costs one allocation + a few thousand ObjC dispatches on the first
  window call. Immeasurable in practice (~1 ms) and gated by
  dispatch_once so the second call is a single atomic load.

### Alternatives considered

#### A. Use `CGMainDisplayID()` (rejected)
Calling `CGMainDisplayID()` also forces CG init, but it's documented
as deprecated in macOS 15+. Fragile to rely on.

#### B. Require callers to do `NSApplicationLoad` themselves (rejected)
Pushes an ObjC implementation detail onto every Go user. Defeats the
point of the library.

#### C. Build an LSBackgroundOnly helper bundle (rejected)
Too much ceremony for a single function call.

## Soak period

Revisit if:
- AppKit-free environments (e.g. a future macOS "server" SKU) break.
- A lower-level initialization call becomes available that doesn't
  require AppKit.
