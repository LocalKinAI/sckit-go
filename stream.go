package sckit

import (
	"context"
	"fmt"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// Stream is a persistent ScreenCaptureKit capture session. A Stream is
// NOT safe for concurrent use by multiple goroutines; protect with your
// own mutex if you fan frames out.
//
// Always call Close when done — the underlying SCStream holds a
// connection to the WindowServer + ReplayKit daemon that will not
// release on its own.
type Stream struct {
	handle uintptr
	width  int
	height int
	buf    []byte // reused between NextFrame calls

	closeOnce sync.Once
	closed    bool
}

// NewStream opens a capture stream for the given [Target]. The call
// blocks until the underlying SCStream has started (or errored);
// subsequent frame retrieval is via [Stream.NextFrame].
//
// First frame typically arrives within ~150ms of this call returning.
//
// Targets: [Display] and [Window] work. [App], [Region], and [Exclude]
// return [ErrNotImplemented]; those arrive in v0.2.0.
func NewStream(ctx context.Context, target Target, opts ...Option) (*Stream, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := applyOptions(opts)
	f := target.filter()

	switch f.kind {
	case filterKindDisplay, filterKindRegion:
		return newDisplayStream(ctx, f.displayID, cfg, f)
	case filterKindWindow:
		return newWindowStream(ctx, f.windowID, cfg, f)
	case filterKindApp:
		return newAppStream(ctx, f.bundleID, cfg, f)
	default:
		return nil, fmt.Errorf("sckit: NewStream: unknown target kind %d", f.kind)
	}
}

func newWindowStream(ctx context.Context, windowID uint32, cfg config, f contentFilter) (*Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Window target cannot exclude other windows — silently ignore.
	c := cfg.toC(f, nil)
	errBuf := make([]byte, 256)
	h := windowStreamStartFn(windowID,
		unsafe.Pointer(&c),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if h == 0 {
		return nil, wrapDylibErr("window_stream_start", cstr(errBuf))
	}
	return finishStream(h), nil
}

func newAppStream(ctx context.Context, bundleID string, cfg config, f contentFilter) (*Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bundleID == "" {
		return nil, fmt.Errorf("sckit: NewStream(App): bundleID is required")
	}
	excludeIDs := windowsToIDs(f.excludeWindows)
	c := cfg.toC(f, excludeIDs)
	_ = excludeIDs // keep alive
	bidBytes := append([]byte(bundleID), 0)
	errBuf := make([]byte, 256)
	h := appStreamStartFn(unsafe.Pointer(&bidBytes[0]),
		0, // auto-pick display
		unsafe.Pointer(&c),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if h == 0 {
		return nil, wrapDylibErr("app_stream_start", cstr(errBuf))
	}
	return finishStream(h), nil
}

// finishStream reads dims from the dylib handle and wraps it in a
// *Stream with pre-sized frame buffer and finalizer.
func finishStream(h uintptr) *Stream {
	var w, hgt int32
	streamDimsFn(h, unsafe.Pointer(&w), unsafe.Pointer(&hgt))
	s := &Stream{
		handle: h,
		width:  int(w),
		height: int(hgt),
		buf:    make([]byte, int(w)*int(hgt)*4),
	}
	runtime.SetFinalizer(s, func(s *Stream) { _ = s.Close() })
	return s
}

func newDisplayStream(ctx context.Context, displayID uint32, cfg config, f contentFilter) (*Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	excludeIDs := windowsToIDs(f.excludeWindows)
	c := cfg.toC(f, excludeIDs)
	_ = excludeIDs // keep alive
	errBuf := make([]byte, 256)
	h := streamStartFn(displayID,
		unsafe.Pointer(&c),
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	if h == 0 {
		return nil, wrapDylibErr("stream_start", cstr(errBuf))
	}
	return finishStream(h), nil
}

// Width returns the effective frame width in pixels. May differ from a
// requested value if the target's native size was smaller.
func (s *Stream) Width() int { return s.width }

// Height returns the effective frame height in pixels.
func (s *Stream) Height() int { return s.height }

// NextFrame blocks until the next frame is available, then returns it
// as a freshly-allocated [image.Image] (concretely *[image.RGBA]).
//
// Cancellation: if ctx is canceled or its deadline elapses, NextFrame
// returns ctx.Err() as soon as the current underlying dylib call
// completes. v0.1 cannot abort an in-flight dylib call; mid-call
// cancellation lands with the sckit_stream_cancel dylib entry in v0.2.
func (s *Stream) NextFrame(ctx context.Context) (image.Image, error) {
	pix, w, h, err := s.nextFrameInternal(ctx)
	if err != nil {
		return nil, err
	}
	return bgraToRGBA(pix, w, h), nil
}

// Frame is a view into a Stream's internal BGRA buffer. It is valid
// only until the next call on the same Stream; do not retain.
//
// Pixel layout: tightly-packed 32-bit BGRA, top-down, no row padding.
// Pixels has length Width*Height*4.
type Frame struct {
	Pixels []byte
	Width  int
	Height int
}

// NextFrameBGRA returns the next frame as a zero-copy [Frame] pointing
// at the Stream's internal buffer. The buffer is overwritten on the
// next call — do not hold Frame.Pixels past the next NextFrame or
// NextFrameBGRA call.
//
// Use this path in hot loops where the per-frame [image.RGBA]
// allocation and BGRA→RGBA conversion of [NextFrame] is a bottleneck
// (e.g. real-time VLM ingestion where the next step is JPEG-encoding
// the frame anyway).
func (s *Stream) NextFrameBGRA(ctx context.Context) (Frame, error) {
	pix, w, h, err := s.nextFrameInternal(ctx)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Pixels: pix, Width: w, Height: h}, nil
}

// nextFrameInternal runs the actual dylib call, shared by NextFrame and
// NextFrameBGRA. Handles context cancellation via a goroutine that
// watches ctx.Done — on cancel we return ctx.Err() but the dylib call
// itself runs to completion on the current goroutine. True mid-call
// abort requires the sckit_stream_cancel dylib entry (v0.2).
func (s *Stream) nextFrameInternal(ctx context.Context) ([]byte, int, int, error) {
	if s.closed {
		return nil, 0, 0, ErrStreamClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, 0, err
	}

	// Translate ctx.Deadline into the dylib's timeout_ms parameter so
	// we wake up in time to honor the deadline even without an explicit
	// sckit_stream_cancel.
	timeoutMs := int32(0) // 0 = block forever
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, 0, 0, context.DeadlineExceeded
		}
		ms := remaining / time.Millisecond
		if ms < 1 {
			ms = 1
		}
		if ms > int32Max {
			ms = int32Max
		}
		timeoutMs = int32(ms)
	}

	errBuf := make([]byte, 256)
	n := streamNextFn(s.handle,
		unsafe.Pointer(&s.buf[0]), int32(len(s.buf)),
		timeoutMs,
		unsafe.Pointer(&errBuf[0]), int32(len(errBuf)),
	)
	switch {
	case n == -2:
		// Dylib timed out; map to ctx.Err() if ctx is done, else sentinel.
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		return nil, 0, 0, ErrTimeout
	case n <= 0:
		return nil, 0, 0, wrapDylibErr("next_frame", cstr(errBuf))
	}
	return s.buf[:n], s.width, s.height, nil
}

const int32Max = time.Duration(1<<31 - 1)

// Frames returns a convenience channel that delivers frames from the
// Stream until ctx is canceled or an error occurs. The returned error
// channel is closed after the frame channel; it yields at most one
// value (nil on clean ctx-cancel).
//
// A single goroutine is spawned to drive NextFrame; if the consumer
// falls behind, frames are dropped inside the dylib, not buffered here.
//
// Frames is built on top of [NextFrame] for the common producer pattern
// and is intentionally lightweight — for full control, call NextFrame
// directly.
func (s *Stream) Frames(ctx context.Context) (<-chan image.Image, <-chan error) {
	out := make(chan image.Image)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		for {
			img, err := s.NextFrame(ctx)
			if err != nil {
				// Clean cancel → nil on errCh.
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				errCh <- err
				return
			}
			select {
			case out <- img:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errCh
}

// Close shuts down the stream and releases all associated resources.
// Safe to call multiple times and from any goroutine.
func (s *Stream) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.closed = true
		runtime.SetFinalizer(s, nil)
		if s.handle != 0 {
			streamStopFn(s.handle)
			s.handle = 0
		}
	})
	return closeErr
}
