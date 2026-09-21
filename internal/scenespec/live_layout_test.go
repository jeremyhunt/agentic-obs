//go:build obslive

package scenespec_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/layout"
	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// A layout resolves to a bounding box, and obs-websocket has opinions about
// bounding boxes that the fake had to be taught -- FB-64 found it rejecting a
// bounds dimension below 1 even where the bounds type makes it inert. So the
// resolution being arithmetically right is not the same as OBS accepting it,
// and only a live OBS can say.
func TestLiveALayoutResolvesToATransformOBSAccepts(t *testing.T) {
	client := liveClient(t)

	scene := fmt.Sprintf("agentic-obs-layout-%d", time.Now().UnixNano())
	if err := client.CreateScene(scene); err != nil {
		t.Fatalf("CreateScene: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveScene(scene); err != nil {
			t.Logf("warning: could not remove scratch scene %q: %v", scene, err)
		}
	})

	source := scene + "-layer"
	if _, err := client.CreateInput(scene, source, "color_source_v3",
		map[string]interface{}{"width": 320, "height": 240}); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}

	video, err := client.GetVideoSettings()
	if err != nil {
		t.Fatalf("GetVideoSettings: %v", err)
	}

	spec, err := scenespec.Capture(client, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// The full-canvas layer, stated as intent. Dropping the captured transform
	// is the point: nothing in this document mentions a resolution.
	for i := range spec.Items {
		if spec.Items[i].Source == source {
			spec.Items[i].Transform = nil
			spec.Items[i].Layout = &layout.Spec{Mode: "stretch"}
		}
	}

	report, err := scenespec.Apply(context.Background(), client, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, op := range report.Ops {
		if op.Result == scenespec.OpFailed {
			t.Errorf("op failed: %s %s: %s", op.Op, op.Subject, op.Detail)
		}
	}

	live, err := client.GetSceneByName(scene)
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	var placed bool
	for _, item := range live.Sources {
		if item.Name != source {
			continue
		}
		placed = true
		got, err := client.GetSceneItemTransform(scene, item.ID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}
		if got.BoundsType != obs.BoundsStretch {
			t.Errorf("OBS reports bounds type %q, want %q", got.BoundsType, obs.BoundsStretch)
		}
		if got.BoundsWidth != video.BaseWidth || got.BoundsHeight != video.BaseHeight {
			t.Errorf("OBS reports bounds %vx%v, want the canvas %vx%v",
				got.BoundsWidth, got.BoundsHeight, video.BaseWidth, video.BaseHeight)
		}
	}
	if !placed {
		t.Fatalf("the layer is not in %q after the apply", scene)
	}

	// And the second apply has nothing to do, which is the property that makes
	// a layout usable in a document rather than a one-shot command.
	findings, err := scenespec.Diff(client, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, finding := range findings {
		if finding.Subject == source {
			t.Errorf("a layout OBS just accepted still reports %s: %s",
				finding.Field, finding.Detail)
		}
	}
}
