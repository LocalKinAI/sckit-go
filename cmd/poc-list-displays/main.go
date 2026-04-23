// poc-list-displays — end-to-end test: Go → purego → libsckit_sync.dylib →
// ScreenCaptureKit → back to Go as a flat [] of display structs.
//
// Proves the sync-wrapped-async pattern works. Next POC adds actual pixel
// capture on top of this same pattern.
//
// Run:
//
//	cd /Users/jackysun/Documents/Workspace/sckit-go
//	go run ./cmd/poc-list-displays
package main

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Must match sckit_display_t in objc/sckit_sync.m EXACTLY.
//
//	typedef struct {
//	    uint32_t display_id;
//	    int32_t  width;
//	    int32_t  height;
//	    int32_t  frame_x;
//	    int32_t  frame_y;
//	} sckit_display_t;
//
// sizeof = 20 bytes, 4-byte aligned.
type sckitDisplay struct {
	DisplayID uint32
	Width     int32
	Height    int32
	FrameX    int32
	FrameY    int32
}

func main() {
	dylibPath := "./libsckit_sync.dylib"
	lib, err := purego.Dlopen(dylibPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed loading %s: %v\n", dylibPath, err)
		os.Exit(1)
	}
	fmt.Printf("✅ loaded %s (lib handle = 0x%x)\n", dylibPath, lib)

	// int sckit_list_displays(sckit_display_t* out, int max, char* err_msg, int err_len)
	var sckitListDisplays func(out unsafe.Pointer, max int32, errMsg unsafe.Pointer, errLen int32) int32
	purego.RegisterLibFunc(&sckitListDisplays, lib, "sckit_list_displays")

	// Allocate output buffer for up to 8 displays.
	const maxDisplays = 8
	displays := make([]sckitDisplay, maxDisplays)
	errBuf := make([]byte, 256)

	// Warm-up call (first call pays framework-load cost).
	_ = sckitListDisplays(
		unsafe.Pointer(&displays[0]),
		int32(maxDisplays),
		unsafe.Pointer(&errBuf[0]),
		int32(len(errBuf)),
	)
	// Measured call.
	t0 := time.Now()
	count := sckitListDisplays(
		unsafe.Pointer(&displays[0]),
		int32(maxDisplays),
		unsafe.Pointer(&errBuf[0]),
		int32(len(errBuf)),
	)
	elapsed := time.Since(t0)

	if count < 0 {
		// Null-terminate safety and strip trailing zeros.
		errStr := string(errBuf)
		for i, b := range errBuf {
			if b == 0 {
				errStr = string(errBuf[:i])
				break
			}
		}
		fmt.Fprintf(os.Stderr, "❌ sckit_list_displays returned %d: %s\n", count, errStr)
		os.Exit(1)
	}

	fmt.Printf("✅ enumerated %d display(s) in %s\n\n", count, elapsed)
	fmt.Printf("  %-12s  %-12s  %-12s\n", "display_id", "size", "origin")
	fmt.Printf("  %-12s  %-12s  %-12s\n", "----------", "----", "------")
	for i := int32(0); i < count; i++ {
		d := displays[i]
		fmt.Printf("  %-12d  %4dx%-7d  (%d, %d)\n",
			d.DisplayID, d.Width, d.Height, d.FrameX, d.FrameY)
	}

	fmt.Println()
	fmt.Println("Sync-wrapped-async ScreenCaptureKit call works 🎉")
}
