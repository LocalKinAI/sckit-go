package sckit

import "image"

// Target describes what to capture: a [Display], [Window], [App],
// [Region], or an [Exclude] composition. Target is a sealed interface —
// only types declared in this package can implement it. Construct
// targets with struct literals:
//
//	sckit.Display{ID: displayID}
//	sckit.Window{ID: windowID}
//	sckit.App{BundleID: "com.google.Chrome"}
//	sckit.Region{Display: d, Bounds: image.Rect(100, 100, 600, 400)}
//	sckit.Exclude{Target: t, Windows: toHide}
//
// Target corresponds 1:1 to Apple's SCContentFilter abstraction.
type Target interface {
	// filter is unexported to seal the interface. Implementations return
	// a private descriptor that sckit marshals to the dylib.
	filter() contentFilter
}

// contentFilter is the internal descriptor passed down to the dylib. It
// is unexported so the wire format can evolve without breaking external
// Target implementors (there can be none, by design).
type contentFilter struct {
	kind           filterKind
	displayID      uint32
	windowID       uint32
	bundleID       string
	pid            int32
	region         image.Rectangle // empty for full-target capture
	excludeWindows []Window
}

type filterKind int8

const (
	filterKindDisplay filterKind = iota
	filterKindWindow
	filterKindApp
	filterKindRegion
)

// ─── Display ─────────────────────────────────────────────────

// Display describes an attached physical display. The ID field is a
// stable CGDirectDisplayID; positions (X, Y) are in the global
// coordinate space.
type Display struct {
	ID     uint32
	Width  int
	Height int
	X, Y   int
}

func (d Display) filter() contentFilter {
	return contentFilter{kind: filterKindDisplay, displayID: d.ID}
}

// ─── Window ──────────────────────────────────────────────────

// Window describes an individual on-screen window. Enumerate via
// [ListWindows]. Capture via [Capture] with a Window target; stream via
// [NewStream] with a Window target.
type Window struct {
	ID       uint32
	App      string          // owning application name, e.g. "Google Chrome"
	BundleID string          // e.g. "com.google.Chrome"
	Title    string          // window title, may be empty
	Frame    image.Rectangle // in global point coordinates
	OnScreen bool
	Layer    int
	PID      int32 // owning process ID
}

func (w Window) filter() contentFilter {
	return contentFilter{kind: filterKindWindow, windowID: w.ID}
}

// ─── App ─────────────────────────────────────────────────────

// App describes a running application as a capture target. Capturing
// an App records all of its on-screen windows composed together on a
// single display (auto-picked as the display owning the largest share
// of the app's windows).
type App struct {
	BundleID string // e.g. "com.google.Chrome" — required for capture
	Name     string // display name, e.g. "Google Chrome"
	PID      int32
}

func (a App) filter() contentFilter {
	return contentFilter{kind: filterKindApp, bundleID: a.BundleID, pid: a.PID}
}

// ─── Region ──────────────────────────────────────────────────

// Region is a sub-rectangle of a Display, specified in display-local
// points.
//
// Region target capture arrives in v0.2.0. For v0.1 use full-display
// capture and crop in Go.
type Region struct {
	Display Display
	Bounds  image.Rectangle
}

func (r Region) filter() contentFilter {
	return contentFilter{
		kind:      filterKindRegion,
		displayID: r.Display.ID,
		region:    r.Bounds,
	}
}

// ─── Exclude ─────────────────────────────────────────────────

// Exclude wraps any Target and masks out a list of Windows from the
// captured output. Common use case: screenshotting your own app without
// including your own capture window in the result.
type Exclude struct {
	Target  Target
	Windows []Window
}

func (e Exclude) filter() contentFilter {
	f := e.Target.filter()
	f.excludeWindows = append([]Window(nil), e.Windows...)
	return f
}
