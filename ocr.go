package sckit

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

// TextRegion is one piece of text Vision recognized in an image. The
// rectangle is in image-pixel coordinates, top-left origin (the
// convention CGImage / drawing systems use; Vision's native
// bottom-left coords have been converted).
type TextRegion struct {
	Text       string  `json:"text"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	W          int     `json:"w"`
	H          int     `json:"h"`
	Confidence float64 `json:"conf"`
}

// OCR runs macOS Vision framework's VNRecognizeTextRequest against the
// provided image bytes (PNG / JPEG / TIFF / BMP — anything NSImage can
// decode), returning recognized text regions.
//
// Why use this instead of routing screenshots through a vision LLM:
//   - Local, offline, free (no API call cost)
//   - Fast: ~50-200ms per screen-sized image
//   - Deterministic
//   - Returns precise pixel-coord bounding boxes for each text region
//
// When you need it: extracting the value displayed in a calculator,
// reading static labels in a canvas-rendered UI, dumping screen text
// for fuzzy match before deciding which UI element to click.
//
// When NOT to use it: if you need *understanding* of the screen
// content (intent / structure / what to do next) — that's still a
// vision LLM job. OCR returns text + boxes, nothing more.
//
// Recognition level is set to Accurate (slower but higher quality on
// noisy screen captures); language correction is on. There's no knob
// for these in v0.2.0 — opinionated default for the agent use case.
//
// Requires macOS 11+ (Vision framework). Returns ([]TextRegion, nil)
// on success; (nil, error) on decode/recognize failure. An empty slice
// means no text was recognized.
func OCR(imageBytes []byte) ([]TextRegion, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if len(imageBytes) == 0 {
		return nil, errors.New("sckit: OCR called with empty image bytes")
	}

	// Initial output buffer. Most recognized-text JSON for a typical
	// screen is well under 64KB; resize if the dylib reports otherwise.
	buf := make([]byte, 64*1024)
	errBuf := make([]byte, 1024)

	rc := ocrImageFn(
		unsafe.Pointer(&imageBytes[0]), int32(len(imageBytes)),
		unsafe.Pointer(&buf[0]), int32(len(buf)),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)

	switch {
	case rc == 0:
		// Success. Trim at first NUL.
		return parseOCRResult(buf)
	case rc > 0:
		// Buffer too small — rc is the required size. Resize and retry.
		buf = make([]byte, rc)
		rc2 := ocrImageFn(
			unsafe.Pointer(&imageBytes[0]), int32(len(imageBytes)),
			unsafe.Pointer(&buf[0]), int32(len(buf)),
			unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
		)
		if rc2 != 0 {
			return nil, fmt.Errorf("sckit: OCR retry rc=%d (%s)", rc2, cstrFromBuf(errBuf))
		}
		return parseOCRResult(buf)
	default:
		return nil, fmt.Errorf("sckit: OCR failed: %s", cstrFromBuf(errBuf))
	}
}

func parseOCRResult(buf []byte) ([]TextRegion, error) {
	// Trim at first NUL — the dylib writes a C-string then NUL.
	end := len(buf)
	for i, b := range buf {
		if b == 0 {
			end = i
			break
		}
	}
	if end == 0 {
		return nil, nil
	}
	var regions []TextRegion
	if err := json.Unmarshal(buf[:end], &regions); err != nil {
		return nil, fmt.Errorf("sckit: OCR result parse: %w", err)
	}
	return regions, nil
}

func cstrFromBuf(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
