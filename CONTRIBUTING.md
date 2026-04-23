# Contributing to sckit-go

Thanks for your interest. This project is small and focused; that's a
feature. The bar for changes is: does it help more people use
ScreenCaptureKit from Go, without locking us into a design we'll regret?

## Reporting bugs

Before filing, run these locally and include the output:

```bash
sw_vers                               # macOS version
go version
make clean-all && make dylib          # fresh dylib
go test -tags integration ./...       # integration smoke
```

Minimum viable bug report:

1. macOS version + architecture (arm64 / x86_64).
2. Go version.
3. sckit-go version (git commit or tag).
4. Minimal reproducer (≤ 30 lines if possible).
5. Expected behavior vs. actual.

## Proposing features

Before coding, open an issue with:

- **What**: one paragraph describing the feature.
- **Why**: the use case. Not "would be nice," but "X is currently impossible
  / annoying because Y."
- **API sketch**: what the caller writes. Compare to existing API shape.
- **Non-goals**: what this feature should explicitly NOT do.

API changes that add a new function or type → likely fine. API changes
that modify an existing signature → need an ADR under `docs/adr/`
before code lands.

## Pull requests

### Quality bar

Every PR must pass:

```bash
make verify                              # build + vet + one capture
go test -count=1 ./...                   # unit tests
go test -tags integration -count=1 ./... # integration tests (requires TCC perm)
go tool cover -func=cov.out | tail -1    # coverage
staticcheck ./... && golangci-lint run   # 0 warnings
gofmt -l .                               # empty output
```

New features must add tests. Bug fixes must add a regression test that
fails on the current code and passes on your fix.

### Commit style

Short subject (≤ 72 chars), body explaining *why*, not *what* (the diff
shows what). One logical change per commit. Squash noise commits before
review.

```
feat(stream): support windowScoped app composition

Add sckit_app_stream_start entry point. Filters to windows of a
given bundle ID. Composes onto the display owning the majority of
the app's on-screen area (auto-pick), falling back to main display.
```

### Touching the dylib

When you edit `objc/sckit_sync.m`:

1. `make dylib` — rebuild the universal binary.
2. **Commit the rebuilt `internal/dylib/libsckit_sync.dylib`** along
   with the source change. Users depend on the committed binary so
   `go get` works out of the box.
3. Add/update an ADR under `docs/adr/` if the dylib ABI shape changes
   (new params to existing exports, new exported functions, struct
   layout drift, etc.).

### Stability testing

For changes touching stream lifecycle, ObjC sink, or config struct:

```bash
make stability-test                      # defaults to 10-minute run
go run ./cmd/stability-test -duration 1h # longer sanity
```

Before a release tag, a **24-hour** stability run is required and the
result logged in the PR.

## Code style

Idiomatic Go:
- `gofmt` + `goimports`.
- No global mutable state (except the dylib load handle, which is
  `sync.Once`-guarded).
- Every blocking public function takes `context.Context`.
- Every error wraps with `fmt.Errorf(..., %w, err)`.
- Exported symbols get full godoc. `go doc ./...` should have zero
  empty descriptions.
- Match the existing file structure: `target.go`, `options.go`, etc.
  New public concepts get their own file.

## What we will NOT accept

- Cross-platform abstractions (Windows / Linux). sckit-go is macOS-only
  by design; a different project can build on top of it.
- cgo. The whole point is to avoid it; a cgo PR means we rewrite.
- Dependencies beyond `purego` and the Go standard library.
- Features that require root, sudo, or unsigned private entitlements.

## Licensing

By contributing, you agree your code ships under the repository's MIT
license.
