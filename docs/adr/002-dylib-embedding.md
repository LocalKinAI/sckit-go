# ADR 002 — Embed the dylib via go:embed

**Date**: 2026-04-22
**Status**: Accepted
**Supersedes**: none

## Context

v0.0.1 required downstream users to run `make dylib` before `go build`
could succeed. This is a hard blocker for:

- `go get github.com/LocalKinAI/sckit-go` — fails at link time with
  "dylib not found" because the user never ran Make.
- CI systems that only run `go test ./...`.
- IDE integrations that rely on `go run .` Just Working.
- Windows/Linux devs who want to at least type-check their Go code.

Empirically, Go users will walk away from any library that forces
manual C toolchain steps. The most successful Go libraries hide their C
dependencies entirely (`github.com/mattn/go-sqlite3` via cgo tax,
`github.com/ebitengine/purego` via... dynamic loading).

## Decision

Embed the compiled dylib inside the Go package using `//go:embed`, and
extract it to the user's OS cache directory on first call to `Load()`.

### Implementation

- New internal package `github.com/LocalKinAI/sckit-go/internal/dylib`
  holds a single `//go:embed libsckit_sync.dylib` directive exposing
  `dylib.Bytes []byte`.
- `internal/dylib/libsckit_sync.dylib` is **committed to the repository**
  — it's treated as a build artifact but tracked so `go get` works
  without requiring the user to invoke `make`.
- `sckit.Load()` resolves the dylib path via `resolveDylib()`:
  1. If `DylibPath` is non-empty, use it as-is (user override).
  2. Otherwise call `extractEmbedded()`:
     - Hash the embedded bytes (first 8 bytes of SHA-256).
     - Target directory: `<UserCacheDir>/sckit-go/<hash>/libsckit_sync.dylib`.
     - If target already exists with matching size, return the path
       (cache hit — no write needed).
     - Otherwise write via temp-file + rename (atomic).
- The hash-in-path naming means multiple sckit versions in the same
  system (vendored across projects) coexist without collision.

### Universal binary

The dylib is built with `clang -arch arm64 -arch x86_64`, producing a
single Mach-O fat file (~108 KB) that runs on both Intel and Apple
Silicon Macs. No per-architecture extraction logic needed on the Go
side — macOS dyld picks the right slice.

### Makefile flow

- `make dylib` builds `internal/dylib/libsckit_sync.dylib`.
- Contributors rerun this after editing `objc/sckit_sync.m`; they must
  commit the resulting binary.
- `make clean-all` removes it (use before verifying "fresh clone"
  behavior in CI).
- Root-level `./libsckit_sync.dylib` is a legacy path, still produced
  if anyone compiles by hand, ignored by git.

## Consequences

### Positive

- **`go get` works out of the box.** This is the single biggest UX win
  available to this project; it's table-stakes for any Go library
  hoping for 1000+ stars.
- **Per-user cache is idempotent and self-healing.** Corrupted extracts
  recover on next call (hash mismatch → re-extract). Multiple versions
  coexist. No sudo, no PATH manipulation.
- **`DylibPath` override preserved** for power users shipping custom
  builds or reproducing bugs against specific dylib versions.
- **No cgo still true** — `//go:embed` is pure Go stdlib (1.16+),
  doesn't add compile-time dependencies.

### Negative

- **~108 KB added to every `go get`**. Acceptable: `runtime/cgo` is
  larger on its own. Downstream binaries don't ship the embedded copy
  unless they import sckit directly.
- **Git history gains binary churn.** Every dylib rebuild produces a
  different commit. Mitigation: only rebuild on ObjC source changes;
  binaries are reproducible (same source + same clang = same bytes at
  the instruction level; Mach-O headers can differ in timestamps).
  Long-term: consider `git-lfs` if the repo bloats past ~10 MB cumulative
  dylib history.
- **First call writes to `~/Library/Caches/`.** Some sandboxed macOS
  environments (Mac App Store apps, strict MDM profiles) may block
  this. Workaround: set `DylibPath` at startup; document in README.

### Alternatives considered

#### A. `go generate` that builds dylib at `go get` time (rejected)
`go:generate` is not run by `go get`; users would have to invoke it
manually. Same UX failure as the original Makefile.

#### B. Require cgo (rejected)
Would let us #include the ObjC directly. But:
- Forces every downstream user to have clang + macOS SDK headers.
- Breaks cross-compilation completely.
- Defeats the whole point of using purego.

#### C. Download dylib from GitHub Releases at first use (rejected)
Silently hitting the network from a library call is hostile to offline
/ air-gapped development. Embedding is deterministic and offline-safe.

#### D. Ship two release artifacts: `sckit-go` (pure Go) + `sckit-go-dylib` (binary) (rejected)
Adds release complexity and a second supply-chain surface. Embed keeps
everything in one proxy-delivered module.

## Soak period

Decision is effective immediately. Revisit only if:
- Repo size crosses 10 MB from dylib churn, OR
- A class of users reports the cache-extract fails consistently (sandboxing), OR
- Go adds a first-class "companion binary" story that beats go:embed.
