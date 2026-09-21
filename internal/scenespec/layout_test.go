package scenespec_test

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/layout"
	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/obs/obstest"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// A placement captured as an absolute transform is correct for exactly one
// canvas. The collection this was written for is 2560x1440 and its scenes are
// stacks of full-canvas layers, so a spec of absolute numbers silently becomes
// wrong the day the canvas changes -- and the document still looks fine while
// it does.
//
// A layout states the intent instead, and is resolved against the live canvas
// every time it is applied or compared.

func itemIn(t *testing.T, spec *scenespec.Spec, source string) *scenespec.ItemSpec {
	t.Helper()
	for i := range spec.Items {
		if spec.Items[i].Source == source {
			return &spec.Items[i]
		}
	}
	t.Fatalf("no placement of %q in the spec", source)
	return nil
}

func liveTransform(t *testing.T, f *obstest.Fake, scene, source string) *obs.SceneItemTransform {
	t.Helper()
	sc, err := f.GetSceneByName(scene)
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	for _, item := range sc.Sources {
		if item.Name == source {
			tr, err := f.GetSceneItemTransform(scene, item.ID)
			if err != nil {
				t.Fatalf("GetSceneItemTransform: %v", err)
			}
			return tr
		}
	}
	t.Fatalf("no placement of %q in scene %q", source, scene)
	return nil
}

func TestApplyResolvesALayoutAgainstTheCanvas(t *testing.T) {
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// The full-canvas layer: no region, no coordinates, no dependence on the
	// resolution the author happened to be using.
	item := itemIn(t, spec, sharedOverlay)
	item.Transform = nil
	item.Layout = &layout.Spec{Mode: "stretch"}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := liveTransform(t, f, scene, sharedOverlay)
	if got.BoundsType != obs.BoundsStretch {
		t.Errorf("bounds type is %q, want %q", got.BoundsType, obs.BoundsStretch)
	}
	if got.BoundsWidth != 2560 || got.BoundsHeight != 1440 {
		t.Errorf("bounds are %vx%v, want the canvas 2560x1440", got.BoundsWidth, got.BoundsHeight)
	}
	if got.PositionX != 0 || got.PositionY != 0 {
		t.Errorf("position is %v,%v, want 0,0", got.PositionX, got.PositionY)
	}
	if got.Alignment != obs.AlignTopLeft {
		t.Errorf("alignment is %d, want top-left %d", got.Alignment, obs.AlignTopLeft)
	}
}

func TestDiffIsQuietAfterALayoutHasBeenApplied(t *testing.T) {
	// A second apply must have nothing to do. A layout that resolved to a
	// slightly different transform each time would drift forever.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	item := itemIn(t, spec, sharedOverlay)
	item.Transform = nil
	item.Layout = &layout.Spec{Mode: "fit", Anchor: "bottom-right"}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, finding := range findings {
		if finding.Subject == sharedOverlay {
			t.Errorf("a layout that was just applied still reports %s: %s",
				finding.Field, finding.Detail)
		}
	}
}

func TestALayoutFollowsTheCanvasWhereATransformCannot(t *testing.T) {
	// The whole reason the field exists.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	item := itemIn(t, spec, sharedOverlay)
	item.Transform = nil
	item.Layout = &layout.Spec{Mode: "stretch"}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// The operator changes OBS's base resolution.
	f.SetCanvas(1920, 1080)

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("a canvas change left the layout reporting nothing to do")
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	got := liveTransform(t, f, scene, sharedOverlay)
	if got.BoundsWidth != 1920 || got.BoundsHeight != 1080 {
		t.Errorf("bounds are %vx%v after the canvas became 1920x1080",
			got.BoundsWidth, got.BoundsHeight)
	}
}

func TestAnAbsoluteTransformDoesNotFollowTheCanvas(t *testing.T) {
	// The control. A captured transform is not wrong -- it is an answer to a
	// question about a different canvas, and it has no way to say so.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	f.SetCanvas(1920, 1080)

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, finding := range findings {
		if finding.Subject == sharedOverlay {
			t.Errorf("this test no longer shows what it exists to show: %s", finding.Detail)
		}
	}
}

func TestALayoutLeavesCropAlone(t *testing.T) {
	// Crop is not a placement concern. A layout that reset it would be the same
	// silent loss as FB-54's dropped alignment.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	item := itemIn(t, spec, sharedOverlay)
	item.Transform.CropLeft = 12
	item.Transform.CropTop = 34
	item.Layout = &layout.Spec{Mode: "fill"}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := liveTransform(t, f, scene, sharedOverlay)
	if got.CropLeft != 12 || got.CropTop != 34 {
		t.Errorf("crop is %d/%d, want 12/34 -- the layout overwrote it",
			got.CropLeft, got.CropTop)
	}
	if got.BoundsType != obs.BoundsScaleOuter {
		t.Errorf("bounds type is %q, want the layout's %q", got.BoundsType, obs.BoundsScaleOuter)
	}
}

func TestAnUnknownLayoutModeIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	// A malformed document is a caller error, not a runtime failure against a
	// live OBS, so it is refused whole rather than half-applied.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	item := itemIn(t, spec, sharedOverlay)
	item.Layout = &layout.Spec{Mode: "diagonal"}

	if _, err := scenespec.Diff(f, spec, scene); err == nil {
		t.Error("Diff accepted an unknown layout mode")
	}
	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err == nil {
		t.Error("Apply accepted an unknown layout mode")
	}
}

func TestCaptureNeverEmitsALayout(t *testing.T) {
	// Only the author knows a placement is meant to be "the whole canvas"
	// rather than "these particular numbers". A capture reports what is there.
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	for _, item := range spec.Items {
		if item.Layout != nil {
			t.Errorf("capture invented a layout for %q", item.Source)
		}
	}
}

func TestEachFakeHasItsOwnCanvas(t *testing.T) {
	// The canvas used to be a package-level var shared by every fake, so one
	// test resizing it would have changed the canvas under all the others.
	a := obstest.NewFake()
	b := obstest.NewFake()
	a.SetCanvas(1280, 720)

	got, err := b.GetVideoSettings()
	if err != nil {
		t.Fatalf("GetVideoSettings: %v", err)
	}
	if got.BaseWidth != 2560 {
		t.Errorf("a second fake reports %v wide; the canvas is shared", got.BaseWidth)
	}
}

func TestALayoutInsideAGroupIsRefusedRatherThanResolvedWrongly(t *testing.T) {
	// A group's children are positioned within the group, but a layout resolves
	// against the canvas. Resolving one here would put the child somewhere the
	// author never asked for, and silence would be worse still.
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	var placed bool
	for i := range spec.Sources {
		if spec.Sources[i].Type != scenespec.SourceGroup || len(spec.Sources[i].Items) == 0 {
			continue
		}
		spec.Sources[i].Items[0].Layout = &layout.Spec{Mode: "stretch"}
		placed = true
		break
	}
	if !placed {
		t.Fatal("the fixture has no group with children, so this test proves nothing")
	}

	_, err = scenespec.Diff(f, spec, scene)
	if err == nil {
		t.Error("Diff resolved a layout inside a group")
	}
}
