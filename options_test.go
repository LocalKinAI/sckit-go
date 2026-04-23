package sckit

import "testing"

func TestDefaultConfig(t *testing.T) {
	c := defaultConfig()
	if c.width != 0 || c.height != 0 {
		t.Errorf("default resolution should be native (0,0), got %dx%d", c.width, c.height)
	}
	if c.frameRate != 60 {
		t.Errorf("default frame rate = %d, want 60", c.frameRate)
	}
	if !c.showCursor {
		t.Error("cursor should default to shown")
	}
	if c.colorSpace != ColorSpaceSRGB {
		t.Errorf("default color space = %v, want sRGB", c.colorSpace)
	}
	if c.queueDepth != 3 {
		t.Errorf("default queue depth = %d, want 3", c.queueDepth)
	}
}

func TestWithResolution(t *testing.T) {
	c := applyOptions([]Option{WithResolution(1280, 720)})
	if c.width != 1280 || c.height != 720 {
		t.Errorf("got %dx%d, want 1280x720", c.width, c.height)
	}
}

func TestWithResolutionZeroKeepsNative(t *testing.T) {
	c := applyOptions([]Option{WithResolution(0, 0)})
	if c.width != 0 || c.height != 0 {
		t.Errorf("WithResolution(0,0) should mean native; got %dx%d", c.width, c.height)
	}
}

func TestWithFrameRate(t *testing.T) {
	c := applyOptions([]Option{WithFrameRate(30)})
	if c.frameRate != 30 {
		t.Errorf("frame rate = %d, want 30", c.frameRate)
	}
}

func TestWithFrameRateIgnoresNonPositive(t *testing.T) {
	// Non-positive values should be ignored, keeping the default.
	c := applyOptions([]Option{WithFrameRate(-5)})
	if c.frameRate != 60 {
		t.Errorf("frame rate = %d after WithFrameRate(-5), want 60 (default)", c.frameRate)
	}
	c = applyOptions([]Option{WithFrameRate(0)})
	if c.frameRate != 60 {
		t.Errorf("frame rate = %d after WithFrameRate(0), want 60 (default)", c.frameRate)
	}
}

func TestWithCursorFalse(t *testing.T) {
	c := applyOptions([]Option{WithCursor(false)})
	if c.showCursor {
		t.Error("WithCursor(false) did not take effect")
	}
	if !c._setCursor {
		t.Error("_setCursor marker not set")
	}
}

func TestWithCursorTrue(t *testing.T) {
	c := applyOptions([]Option{WithCursor(true)})
	if !c.showCursor {
		t.Error("WithCursor(true) should keep cursor shown")
	}
	if !c._setCursor {
		t.Error("_setCursor marker not set")
	}
}

func TestWithColorSpace(t *testing.T) {
	for _, tc := range []struct {
		name string
		cs   ColorSpace
	}{
		{"sRGB", ColorSpaceSRGB},
		{"P3", ColorSpaceDisplayP3},
		{"BT709", ColorSpaceBT709},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := applyOptions([]Option{WithColorSpace(tc.cs)})
			if c.colorSpace != tc.cs {
				t.Errorf("got %v, want %v", c.colorSpace, tc.cs)
			}
		})
	}
}

func TestWithQueueDepthInRange(t *testing.T) {
	c := applyOptions([]Option{WithQueueDepth(5)})
	if c.queueDepth != 5 {
		t.Errorf("queue depth = %d, want 5", c.queueDepth)
	}
}

func TestWithQueueDepthOutOfRange(t *testing.T) {
	// Values outside [1, 8] should be silently clamped (dropped).
	for _, n := range []int{0, -1, 9, 100} {
		c := applyOptions([]Option{WithQueueDepth(n)})
		if c.queueDepth != 3 {
			t.Errorf("WithQueueDepth(%d) should not change default; got %d", n, c.queueDepth)
		}
	}
}

func TestOptionsComposeInOrder(t *testing.T) {
	// Later options override earlier ones.
	c := applyOptions([]Option{
		WithFrameRate(30),
		WithFrameRate(120),
	})
	if c.frameRate != 120 {
		t.Errorf("later override failed, got %d", c.frameRate)
	}
}

func TestOptionsIndependent(t *testing.T) {
	c := applyOptions([]Option{
		WithResolution(800, 600),
		WithFrameRate(30),
		WithCursor(false),
		WithColorSpace(ColorSpaceDisplayP3),
		WithQueueDepth(5),
	})
	if c.width != 800 || c.height != 600 {
		t.Error("resolution")
	}
	if c.frameRate != 30 {
		t.Error("frame rate")
	}
	if c.showCursor {
		t.Error("cursor")
	}
	if c.colorSpace != ColorSpaceDisplayP3 {
		t.Error("color space")
	}
	if c.queueDepth != 5 {
		t.Error("queue depth")
	}
}
