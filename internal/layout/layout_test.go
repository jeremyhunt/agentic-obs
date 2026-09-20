package layout

import (
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Layout resolves an intent -- "fit this to the canvas", "cover the lower
// third" -- into the transform fields OBS needs.
//
// It never asks how big the source is, and that is the design rather than a
// limitation. OBS already implements fit, fill and stretch as bounds types, so
// computing a scale factor from a source's own dimensions would duplicate work
// OBS does better, divide by zero before a browser source has loaded, and go
// stale the moment the asset is replaced. Resolving to a bounding box needs the
// canvas and nothing else. (FB-75)

func canvas() Canvas { return Canvas{Width: 2560, Height: 1440} }

func TestModesMapToBoundsTypes(t *testing.T) {
	// Each mode is one of OBS's bounds types under a plainer name. The mapping
	// is from obs_transform_info in obs-studio's scene reference, not inferred
	// from behaviour.
	tests := []struct {
		mode string
		want string
		why  string
	}{
		{"fit", obs.BoundsScaleInner, "the whole source visible inside the box, letterboxed"},
		{"fill", obs.BoundsScaleOuter, "covers the box, overflowing edges cropped"},
		{"stretch", obs.BoundsStretch, "fills the box, aspect ratio ignored"},
		{"width", obs.BoundsScaleToWidth, "matches the box width, height follows"},
		{"height", obs.BoundsScaleToHeight, "matches the box height, width follows"},
		{"shrink", obs.BoundsMaxOnly, "scales down to fit but never up"},
	}

	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			got, err := Resolve(Spec{Mode: tc.mode}, canvas())
			if err != nil {
				t.Fatalf("Resolve(%s): %v", tc.mode, err)
			}
			if got.BoundsType != tc.want {
				t.Errorf("mode %q gave bounds type %q, want %q (%s)", tc.mode, got.BoundsType, tc.want, tc.why)
			}
		})
	}
}

func TestDefaultRegionIsTheWholeCanvas(t *testing.T) {
	got, err := Resolve(Spec{Mode: "fit"}, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.PositionX != 0 || got.PositionY != 0 {
		t.Errorf("position is %v,%v; a region-less spec starts at the canvas origin", got.PositionX, got.PositionY)
	}
	if got.BoundsWidth != 2560 || got.BoundsHeight != 1440 {
		t.Errorf("bounds are %vx%v, want the canvas 2560x1440", got.BoundsWidth, got.BoundsHeight)
	}
}

func TestRegionPlacesTheBoxWithinTheCanvas(t *testing.T) {
	// A lower third: full width, bottom quarter.
	got, err := Resolve(Spec{
		Mode:   "fill",
		Region: &Region{X: 0, Y: 1080, Width: 2560, Height: 360},
	}, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.PositionX != 0 || got.PositionY != 1080 {
		t.Errorf("position is %v,%v, want 0,1080", got.PositionX, got.PositionY)
	}
	if got.BoundsWidth != 2560 || got.BoundsHeight != 360 {
		t.Errorf("bounds are %vx%v, want 2560x360", got.BoundsWidth, got.BoundsHeight)
	}
}

func TestItemAlignmentIsAlwaysTopLeft(t *testing.T) {
	// alignment says which point of the item sits at positionX/Y;
	// boundsAlignment says where the source sits inside the box. Conflating
	// them is the easy mistake, so the item anchor is pinned to top-left and
	// the position is simply the region's top-left corner. Everything
	// expressive happens through boundsAlignment.
	got, err := Resolve(Spec{
		Mode:   "fit",
		Anchor: "bottom-right",
		Region: &Region{X: 100, Y: 200, Width: 400, Height: 300},
	}, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Alignment != obs.AlignTopLeft {
		t.Errorf("item alignment is %d, want AlignTopLeft (%d); position is the box's top-left corner",
			got.Alignment, obs.AlignTopLeft)
	}
	if got.PositionX != 100 || got.PositionY != 200 {
		t.Errorf("position is %v,%v, want the region corner 100,200", got.PositionX, got.PositionY)
	}
}

func TestAnchorSetsBoundsAlignment(t *testing.T) {
	tests := []struct {
		anchor string
		want   int
	}{
		{"", obs.AlignCenter},
		{"center", obs.AlignCenter},
		{"top-left", obs.AlignTop | obs.AlignLeft},
		{"top", obs.AlignTop},
		{"top-right", obs.AlignTop | obs.AlignRight},
		{"left", obs.AlignLeft},
		{"right", obs.AlignRight},
		{"bottom-left", obs.AlignBottom | obs.AlignLeft},
		{"bottom", obs.AlignBottom},
		{"bottom-right", obs.AlignBottom | obs.AlignRight},
	}

	for _, tc := range tests {
		name := tc.anchor
		if name == "" {
			name = "(unset)"
		}
		t.Run(name, func(t *testing.T) {
			got, err := Resolve(Spec{Mode: "fit", Anchor: tc.anchor}, canvas())
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.BoundsAlignment != tc.want {
				t.Errorf("anchor %q gave bounds alignment %d, want %d", tc.anchor, got.BoundsAlignment, tc.want)
			}
		})
	}
}

func TestCentreIsZeroNotAFlag(t *testing.T) {
	// OBS_ALIGN_CENTER is 0 -- the absence of edge flags. So "center" must
	// resolve to exactly 0, and combining it with an edge would be meaningless
	// rather than additive. Worth its own test because ORing a zero silently
	// does nothing, and a wrong anchor looks like a layout bug, not a constant
	// bug.
	got, err := Resolve(Spec{Mode: "fit", Anchor: "center"}, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.BoundsAlignment != 0 {
		t.Errorf("centre resolved to %d, want 0", got.BoundsAlignment)
	}
}

func TestRejectsWhatOBSWouldReject(t *testing.T) {
	tests := []struct {
		name   string
		spec   Spec
		canvas Canvas
	}{
		{"unknown mode", Spec{Mode: "cover"}, canvas()},
		{"unknown anchor", Spec{Mode: "fit", Anchor: "middle"}, canvas()},
		{"no canvas", Spec{Mode: "fit"}, Canvas{}},
		{"zero-width region", Spec{Mode: "fit", Region: &Region{Width: 0, Height: 100}}, canvas()},
		{"negative region", Spec{Mode: "fit", Region: &Region{Width: -10, Height: 100}}, canvas()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Resolve(tc.spec, tc.canvas); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestBoundsAreNeverBelowOne(t *testing.T) {
	// obs-websocket rejects a bounds dimension below 1, even under
	// OBS_BOUNDS_NONE where the value is inert (FB-64). A region smaller than a
	// pixel is a caller mistake, but a resolved transform must never carry a
	// value OBS will refuse.
	got, err := Resolve(Spec{
		Mode:   "fit",
		Region: &Region{X: 0, Y: 0, Width: 0.4, Height: 0.4},
	}, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.BoundsWidth < 1 || got.BoundsHeight < 1 {
		t.Errorf("bounds are %vx%v; OBS rejects anything below 1", got.BoundsWidth, got.BoundsHeight)
	}
}

func TestNoneClearsTheBoundingBox(t *testing.T) {
	// The layer-stack convention: assets authored at canvas size, placed at the
	// origin with no scaling. Mode "none" expresses that without pretending a
	// bounding box is involved.
	got, err := Resolve(Spec{Mode: "none"}, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.BoundsType != obs.BoundsNone {
		t.Errorf("bounds type is %q, want %q", got.BoundsType, obs.BoundsNone)
	}
	// Still at least 1, because OBS validates the field whether or not it uses
	// it.
	if got.BoundsWidth < 1 || got.BoundsHeight < 1 {
		t.Errorf("bounds are %vx%v; they are inert here but still validated", got.BoundsWidth, got.BoundsHeight)
	}
}

func TestResolveIsPure(t *testing.T) {
	// No source dimensions in the signature, and none needed. If this ever
	// requires them, the design has drifted back to arithmetic OBS already does.
	spec := Spec{Mode: "fit", Anchor: "top-left", Region: &Region{X: 10, Y: 20, Width: 30, Height: 40}}

	first, err := Resolve(spec, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	second, err := Resolve(spec, canvas())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first != second {
		t.Errorf("same input gave %+v then %+v", first, second)
	}
}
