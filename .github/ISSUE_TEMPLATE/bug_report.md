---
name: Bug report
about: Something is broken or behaving unexpectedly
title: '[bug] '
labels: bug
assignees: ''
---

## Environment

- macOS: <!-- output of `sw_vers` -->
- Architecture: <!-- arm64 / x86_64 -->
- Go version: <!-- `go version` -->
- sckit-go version / commit: <!-- tag or git sha -->

## What I tried

<!-- Paste the minimum Go code that reproduces. ≤30 lines preferred. -->

```go
package main

import (
    "context"
    "github.com/LocalKinAI/sckit-go"
)

func main() {
    ctx := context.Background()
    // ...
}
```

## Expected

<!-- What you thought would happen. -->

## Actual

<!-- What happened instead. Include error messages, panics, stack traces verbatim. -->

## Additional context

<!-- Screenshots, PNG output, relevant console logs. -->

## Have you tried?

- [ ] `make clean-all && make dylib` to rebuild fresh
- [ ] Confirmed Screen Recording permission is granted
- [ ] `go test -tags integration ./...` passes locally
- [ ] Search for existing issues with the same symptom
