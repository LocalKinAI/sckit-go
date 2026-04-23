package sckit

import (
	"image"
	"testing"
)

// ─── Target.filter() correctness ─────────────────────────────

func TestDisplayFilter(t *testing.T) {
	d := Display{ID: 42, Width: 1920, Height: 1080}
	f := d.filter()
	if f.kind != filterKindDisplay {
		t.Errorf("kind = %v, want Display", f.kind)
	}
	if f.displayID != 42 {
		t.Errorf("displayID = %d, want 42", f.displayID)
	}
	if len(f.excludeWindows) != 0 {
		t.Errorf("excludeWindows should be empty, got %d", len(f.excludeWindows))
	}
}

func TestWindowFilter(t *testing.T) {
	w := Window{ID: 100, App: "Chrome", BundleID: "com.google.Chrome"}
	f := w.filter()
	if f.kind != filterKindWindow {
		t.Errorf("kind = %v, want Window", f.kind)
	}
	if f.windowID != 100 {
		t.Errorf("windowID = %d, want 100", f.windowID)
	}
}

func TestAppFilter(t *testing.T) {
	a := App{BundleID: "com.apple.Safari", PID: 12345}
	f := a.filter()
	if f.kind != filterKindApp {
		t.Errorf("kind = %v, want App", f.kind)
	}
	if f.bundleID != "com.apple.Safari" {
		t.Errorf("bundleID = %q, want com.apple.Safari", f.bundleID)
	}
	if f.pid != 12345 {
		t.Errorf("pid = %d, want 12345", f.pid)
	}
}

func TestRegionFilter(t *testing.T) {
	d := Display{ID: 7, Width: 1920, Height: 1080}
	r := Region{Display: d, Bounds: image.Rect(100, 50, 900, 650)}
	f := r.filter()
	if f.kind != filterKindRegion {
		t.Errorf("kind = %v, want Region", f.kind)
	}
	if f.displayID != 7 {
		t.Errorf("displayID = %d, want 7", f.displayID)
	}
	if f.region.Dx() != 800 || f.region.Dy() != 600 {
		t.Errorf("region = %v, want 800x600", f.region)
	}
}

func TestExcludeFilterWrapsDisplay(t *testing.T) {
	d := Display{ID: 1}
	w1 := Window{ID: 10}
	w2 := Window{ID: 20}
	e := Exclude{Target: d, Windows: []Window{w1, w2}}
	f := e.filter()
	if f.kind != filterKindDisplay {
		t.Errorf("Exclude should preserve wrapped kind; got %v", f.kind)
	}
	if f.displayID != 1 {
		t.Errorf("displayID lost through Exclude: %d", f.displayID)
	}
	if len(f.excludeWindows) != 2 {
		t.Fatalf("excludeWindows count = %d, want 2", len(f.excludeWindows))
	}
	if f.excludeWindows[0].ID != 10 || f.excludeWindows[1].ID != 20 {
		t.Errorf("excludeWindows IDs wrong: %v", f.excludeWindows)
	}
}

func TestExcludeFilterWrapsApp(t *testing.T) {
	a := App{BundleID: "com.google.Chrome"}
	w := Window{ID: 77}
	f := Exclude{Target: a, Windows: []Window{w}}.filter()
	if f.kind != filterKindApp {
		t.Errorf("kind = %v, want App", f.kind)
	}
	if f.bundleID != "com.google.Chrome" {
		t.Errorf("bundleID lost: %q", f.bundleID)
	}
	if len(f.excludeWindows) != 1 || f.excludeWindows[0].ID != 77 {
		t.Errorf("exclude windows not threaded: %v", f.excludeWindows)
	}
}

func TestExcludeMutationDoesNotAffectSource(t *testing.T) {
	// Exclude should deep-copy the Windows slice so later mutations
	// don't corrupt the filter.
	windows := []Window{{ID: 1}, {ID: 2}}
	e := Exclude{Target: Display{ID: 1}, Windows: windows}
	f := e.filter()
	windows[0].ID = 999
	if f.excludeWindows[0].ID != 1 {
		t.Errorf("external mutation reached filter: %d", f.excludeWindows[0].ID)
	}
}

func TestFilterKindString(t *testing.T) {
	cases := []struct {
		k    filterKind
		want string
	}{
		{filterKindDisplay, "Display"},
		{filterKindWindow, "Window"},
		{filterKindApp, "App"},
		{filterKindRegion, "Region"},
		{filterKind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("%d.String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}
