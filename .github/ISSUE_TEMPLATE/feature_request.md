---
name: Feature request
about: Propose a new capability or API
title: '[feat] '
labels: enhancement
assignees: ''
---

## What

<!-- One-paragraph description of the feature. -->

## Why

<!-- The use case. "Would be nice" is not enough — describe a real scenario
that is currently impossible or painful, and why. -->

## API sketch

<!-- What the caller writes. Compare to the existing API shape. -->

```go
// Proposed:
sckit.XxxExample(ctx, ...)

// Alternative considered:
// ...
```

## Non-goals

<!-- What this feature should explicitly NOT do. Scope-cutting clarifies
what's in scope. -->

## Have you checked?

- [ ] This is not already possible via the current API
- [ ] This doesn't require cross-platform support (macOS-only by design)
- [ ] This doesn't require cgo (sckit-go is purego-only)
- [ ] I have read `docs/API_DESIGN.md` and understand the design principles
