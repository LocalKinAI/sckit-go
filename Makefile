# sckit-go — build the ObjC companion dylib (universal arm64+x86_64),
# then Go tests & examples.
#
# Requirements:
#   - macOS 14+ (Sonoma) — ScreenCaptureKit + SCScreenshotManager
#   - Xcode Command Line Tools (for clang + frameworks)
#   - Go 1.22+
#
# The dylib is committed at internal/dylib/libsckit_sync.dylib so
# downstream users get a working `go get` without running this Makefile.
# Contributors rerun `make dylib` after editing objc/sckit_sync.m.

EMBEDDED_DYLIB := internal/dylib/libsckit_sync.dylib
OBJC_SRC       := objc/sckit_sync.m

# Default arch list. Override for local-only debug builds:
#   make dylib ARCHES="arm64"
ARCHES  ?= arm64 x86_64
ARCH_FLAGS := $(foreach a,$(ARCHES),-arch $(a))

CLANG_FLAGS := -dynamiclib -fobjc-arc -O2
FRAMEWORKS  := -framework ScreenCaptureKit \
               -framework CoreMedia \
               -framework CoreVideo \
               -framework Foundation \
               -framework CoreGraphics \
               -framework AppKit

.PHONY: all dylib build test vet lint clean examples verify help

all: dylib build

$(EMBEDDED_DYLIB): $(OBJC_SRC)
	@echo "→ Building universal dylib ($(ARCHES))"
	@mkdir -p $(@D)
	clang $(CLANG_FLAGS) $(ARCH_FLAGS) $(OBJC_SRC) $(FRAMEWORKS) -o $@
	@echo "→ Verifying architectures"
	@file $@

dylib: $(EMBEDDED_DYLIB)   ## Build + commit the embedded universal dylib

build: dylib               ## Build all Go packages
	go build ./...

test: dylib                ## Run Go tests (require Screen Recording permission on first run)
	go test ./...

vet:                       ## go vet ./...
	go vet ./...

lint: vet                  ## vet + staticcheck if available
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "skip staticcheck (not installed)"
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "skip golangci-lint (not installed)"

examples: dylib            ## Run the example programs
	@echo "── example-capture ───────────────"
	go run ./cmd/example-capture /tmp/sckit-example.png
	@echo ""
	@echo "── example-stream (30 frames) ────"
	go run ./cmd/example-stream

verify: dylib build vet    ## CI-style smoke check: build + vet + one capture
	go run ./cmd/example-capture /tmp/sckit-verify.png
	@echo "✅ verify passed"

stability-test: dylib      ## Run a short (10 min) stability test; override with DURATION=24h
	DURATION=$${DURATION:-10m}; \
	echo "→ stability test, duration=$$DURATION"; \
	go run ./cmd/stability-test -duration $$DURATION

stability-24h: dylib       ## Required pre-release gate: 24h continuous run
	go run ./cmd/stability-test -duration 24h -reopen 1h -sample 5m

cli: dylib                 ## Build the sckit CLI binary into ./sckit
	go build -o sckit ./cmd/sckit
	@echo "→ built ./sckit ($(shell du -h sckit | cut -f1))"

install-cli: dylib         ## Install sckit CLI to \$$GOBIN (usually ~/go/bin)
	go install ./cmd/sckit
	@echo "→ installed sckit to $$(go env GOBIN || echo \"$$(go env GOPATH)/bin\")"

clean:                     ## Remove build artifacts (keeps committed embedded dylib)
	rm -f libsckit_sync.dylib
	rm -rf ~/Library/Caches/sckit-go
	go clean ./...

clean-all: clean           ## Also delete the committed embedded dylib (rebuild with make dylib)
	rm -f $(EMBEDDED_DYLIB)

help:                      ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?##"}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
