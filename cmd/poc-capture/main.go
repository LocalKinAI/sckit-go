// poc-capture — one-shot screenshot via sckit_capture_display, saved as PNG.
//
// Run:
//
//	cd /Users/jackysun/Documents/Workspace/sckit-go
//	go run ./cmd/poc-capture [output.png]
//
// On first run macOS will prompt for Screen Recording permission in
// System Settings → Privacy & Security. After granting, rerun.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

type sckitDisplay struct {
	DisplayID uint32
	Width     int32
	Height    int32
	FrameX    int32
	FrameY    int32
}

func main() {
	outPath := "capture.png"
	if len(os.Args) > 1 {
		outPath = os.Args[1]
	}

	lib, err := purego.Dlopen("./libsckit_sync.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dlopen: %v\n", err)
		os.Exit(1)
	}

	var listDisplays func(out unsafe.Pointer, max int32, errMsg unsafe.Pointer, errLen int32) int32
	var capture func(displayID uint32, pixels unsafe.Pointer, cap int32,
		outW unsafe.Pointer, outH unsafe.Pointer, errMsg unsafe.Pointer, errLen int32) int32
	purego.RegisterLibFunc(&listDisplays, lib, "sckit_list_displays")
	purego.RegisterLibFunc(&capture, lib, "sckit_capture_display")

	// Enumerate to pick display 0.
	displays := make([]sckitDisplay, 4)
	errBuf := make([]byte, 256)
	n := listDisplays(unsafe.Pointer(&displays[0]), int32(len(displays)),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)))
	if n <= 0 {
		fmt.Fprintf(os.Stderr, "list failed: %s\n", cstr(errBuf))
		os.Exit(1)
	}
	d := displays[0]
	fmt.Printf("→ capturing display %d (%dx%d)\n", d.DisplayID, d.Width, d.Height)

	// Allocate tight BGRA buffer.
	bufSize := int(d.Width) * int(d.Height) * 4
	pixels := make([]byte, bufSize)
	var outW, outH int32

	t0 := time.Now()
	wrote := capture(d.DisplayID,
		unsafe.Pointer(&pixels[0]), int32(bufSize),
		unsafe.Pointer(&outW), unsafe.Pointer(&outH),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)))
	elapsed := time.Since(t0)

	if wrote <= 0 {
		fmt.Fprintf(os.Stderr, "capture failed: %s\n", cstr(errBuf))
		os.Exit(1)
	}
	fmt.Printf("✅ captured %d bytes (%dx%d) in %s\n", wrote, outW, outH, elapsed)

	// BGRA → RGBA for Go's image/png.
	rgba := image.NewRGBA(image.Rect(0, 0, int(outW), int(outH)))
	for i := 0; i < bufSize; i += 4 {
		rgba.Pix[i+0] = pixels[i+2] // R ← B channel position in BGRA
		rgba.Pix[i+1] = pixels[i+1] // G
		rgba.Pix[i+2] = pixels[i+0] // B ← R channel position
		rgba.Pix[i+3] = pixels[i+3] // A
	}

	var png_out bytes.Buffer
	if err := png.Encode(&png_out, rgba); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, png_out.Bytes(), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("💾 wrote %s (%d bytes PNG)\n", outPath, png_out.Len())
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
