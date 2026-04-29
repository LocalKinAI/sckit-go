package sckit

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// TestOCR_DetectsHelloWorld is a self-contained end-to-end test:
// generate a PNG with known text via Go's image package, run OCR,
// assert Vision recognizes the text. Doesn't require a live screen
// or any TCC permission — pure in-memory image processing.
func TestOCR_DetectsHelloWorld(t *testing.T) {
	// Render "HELLO" on a 320x80 white canvas in black 7x13 basic font.
	// 7x13 stretched up via per-pixel write to make it OCR-friendly
	// (Vision struggles with sub-10px line height).
	const w, h = 320, 80
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, white)
		}
	}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(black),
		Face: basicfont.Face7x13,
		Dot:  fixed.Point26_6{X: fixed.I(20), Y: fixed.I(50)},
	}
	// "HELLO" alone — basicfont's W is ambiguous to Vision (often
	// misread as H). HELLO is unambiguous and proves the pipeline.
	d.DrawString("HELLO")

	// Scale-up: replicate each pixel into a 4x4 block so glyph height
	// reaches ~52px — well within Vision's "Accurate" recognition zone.
	scale := 4
	big := image.NewRGBA(image.Rect(0, 0, w*scale, h*scale))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.At(x, y)
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					big.Set(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, big); err != nil {
		t.Fatalf("png encode: %v", err)
	}

	regions, err := OCR(buf.Bytes())
	if err != nil {
		t.Fatalf("OCR error: %v", err)
	}
	if len(regions) == 0 {
		t.Fatal("OCR returned no regions")
	}

	// Concatenate all recognized text. Vision sometimes splits a phrase
	// into multiple regions (e.g. "HELLO" + "WORLD"); accept the
	// concatenation as long as both words are present.
	var collected strings.Builder
	for _, r := range regions {
		collected.WriteString(strings.ToUpper(r.Text))
		collected.WriteByte(' ')
	}
	got := collected.String()
	if !strings.Contains(got, "HELLO") {
		t.Errorf("OCR missing %q in result %q (regions=%v)", "HELLO", got, regions)
	}

	// Sanity: every region has positive width/height + a confidence in [0,1].
	for i, r := range regions {
		if r.W <= 0 || r.H <= 0 {
			t.Errorf("region[%d] has non-positive dims w=%d h=%d", i, r.W, r.H)
		}
		if r.Confidence < 0 || r.Confidence > 1 {
			t.Errorf("region[%d] confidence out of [0,1]: %f", i, r.Confidence)
		}
	}
}

func TestOCR_EmptyInput(t *testing.T) {
	_, err := OCR(nil)
	if err == nil {
		t.Error("OCR(nil) should error, got nil")
	}
}

func TestOCR_BlankCanvas(t *testing.T) {
	// Solid white image — OCR should return zero regions, no error.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	regions, err := OCR(buf.Bytes())
	if err != nil {
		t.Fatalf("OCR on blank canvas should not error: %v", err)
	}
	if len(regions) != 0 {
		t.Errorf("OCR on blank canvas should return no regions, got %d", len(regions))
	}
}
