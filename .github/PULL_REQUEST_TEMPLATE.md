<!-- Thanks for contributing! Please walk through this checklist. -->

## What

<!-- One-paragraph summary of the change. Link the issue it closes
if applicable: "Closes #123". -->

## Why

<!-- The reason this matters. -->

## How

<!-- Key design choices, and alternatives considered + rejected. -->

## Testing

- [ ] `make verify` passes
- [ ] `go test -count=1 ./...` passes
- [ ] `go test -tags integration -count=1 ./...` passes
- [ ] `staticcheck ./...` 0 warnings
- [ ] `golangci-lint run` 0 issues
- [ ] Added unit tests for new code (bug fixes include regression test)
- [ ] (If touching stream lifecycle) ran `make stability-test` for ≥ 10 min

## Checklist

- [ ] Commit messages follow the `feat(area): subject` style
- [ ] Updated `CHANGELOG.md` under `## [Unreleased]`
- [ ] (If dylib changed) rebuilt + committed `internal/dylib/libsckit_sync.dylib`
- [ ] (If ABI changed) added an ADR under `docs/adr/`
- [ ] (If public API changed) updated `docs/API_DESIGN.md` and godoc
- [ ] Signed off that this code is MIT-licensed
