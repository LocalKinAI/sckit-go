package sckit

// Option configures a [Capture] or [NewStream] call. Options are applied
// in the order given; later options override earlier ones.
//
// Options use the functional-options pattern so the API can evolve
// without breaking existing callers.
type Option func(*config)

// config holds the effective settings after all Options are applied.
// Zero-value is "sane defaults": native resolution, 60fps, cursor shown,
// sRGB color space, queue depth 3.
type config struct {
	width      int
	height     int
	frameRate  int
	showCursor bool
	colorSpace ColorSpace
	queueDepth int
	_setCursor bool // distinguishes "default true" from "caller set true"
}

func defaultConfig() config {
	return config{
		width:      0, // 0 = native
		height:     0,
		frameRate:  60,
		showCursor: true,
		colorSpace: ColorSpaceSRGB,
		queueDepth: 3,
	}
}

func applyOptions(opts []Option) config {
	c := defaultConfig()
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// WithResolution sets the output frame dimensions in pixels. Zero
// values (the default) mean "use the target's native resolution",
// which for a Display target means the CGDirectDisplay's pixel width
// and height.
//
// Non-native values let the dylib downsample server-side, saving
// bandwidth over the Go/C boundary. Upsampling is not recommended.
func WithResolution(width, height int) Option {
	return func(c *config) {
		c.width = width
		c.height = height
	}
}

// WithFrameRate caps the maximum frame delivery rate. Applies to
// streams only; ignored by [Capture]. The effective rate is also
// bounded above by the display's refresh rate.
//
// Default: 60.
func WithFrameRate(fps int) Option {
	return func(c *config) {
		if fps > 0 {
			c.frameRate = fps
		}
	}
}

// WithCursor controls whether the hardware cursor is rendered into the
// captured frames. Default: true.
func WithCursor(show bool) Option {
	return func(c *config) {
		c.showCursor = show
		c._setCursor = true
	}
}

// WithColorSpace selects the output color space. See [ColorSpace].
// Default: [ColorSpaceSRGB].
func WithColorSpace(cs ColorSpace) Option {
	return func(c *config) {
		c.colorSpace = cs
	}
}

// WithQueueDepth sets the number of frame buffers the underlying
// SCStream keeps queued. Higher values tolerate consumer lag at the
// cost of memory; lower values reduce latency.
//
// Default: 3. Valid range: 1–8.
func WithQueueDepth(n int) Option {
	return func(c *config) {
		if n >= 1 && n <= 8 {
			c.queueDepth = n
		}
	}
}

// ColorSpace identifies a color space for captured frames.
type ColorSpace int

const (
	// ColorSpaceSRGB is the standard sRGB color space. Default.
	ColorSpaceSRGB ColorSpace = iota
	// ColorSpaceDisplayP3 is Apple's wide-gamut Display P3.
	ColorSpaceDisplayP3
	// ColorSpaceBT709 is the Rec. 709 HD video color space.
	ColorSpaceBT709
)
