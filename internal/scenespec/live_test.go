//go:build obslive

// The capture, run against the collection it was designed from.
//
// Every shape this package exists to handle is present in that collection and
// absent from a tidy fixture: a source placed twice in one scene, eight groups
// sitting beside ten nested-scene placements, containers carrying filters, and
// sources shared across five scenes. The unit tests pin the behaviour; this
// says the behaviour is the one a real OBS produces.
//
// It is read-only. It captures and asserts, and writes nothing.
package scenespec_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

func liveClient(t *testing.T) *obs.Client {
	t.Helper()

	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1")
	}

	host, port := envOr("OBS_HOST", "localhost"), envOr("OBS_PORT", "4455")
	client := obs.NewClient(obs.ConnectionConfig{
		Host: host, Port: port, Password: os.Getenv("OBS_PASSWORD"),
	})
	if err := client.Connect(); err != nil {
		t.Fatalf("could not reach OBS at %s:%s: %v", host, port, err)
	}
	t.Cleanup(func() { _ = client.Disconnect() })

	return client
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// operatorScenes drops the scratch scenes other live suites are creating and
// removing in the same OBS.
//
// The tests below are about the operator's collection, and a scene named
// agentic-obs-* belongs to a test run, not to it. This is not tidiness: `go
// test ./internal/...` builds packages in parallel and runs them concurrently,
// so internal/obs's contract suite is creating and deleting a scratch scene per
// row while this package is enumerating the collection. A scene that vanishes
// between the listing and the capture then reads as a failure of the capture.
//
// Running the live suites with -p 1 avoids the race as well, and the Makefile
// does, but a test that only passes when it has OBS to itself is a test that
// will fail mysteriously one day.
func operatorScenes(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "agentic-obs-") {
			continue
		}
		out = append(out, name)
	}
	return out
}

func TestLiveCaptureEveryScene(t *testing.T) {
	client := liveClient(t)

	scenes, _, err := client.GetSceneList()
	if err != nil {
		t.Fatalf("GetSceneList: %v", err)
	}
	scenes = operatorScenes(scenes)
	if len(scenes) == 0 {
		t.Skip("no scenes on the main canvas")
	}

	groups, err := client.GetGroupList()
	if err != nil {
		t.Fatalf("GetGroupList: %v", err)
	}
	isGroup := map[string]bool{}
	for _, g := range groups {
		isGroup[g] = true
	}

	var (
		totalSources, totalItems        int
		sawGroup, sawNested, sawRepeat  bool
		sawContainerFilter, sawSettings bool
	)

	for _, name := range scenes {
		spec, err := scenespec.Capture(client, name)
		if err != nil {
			t.Errorf("capturing %q: %v", name, err)
			continue
		}

		totalSources += len(spec.Sources)
		totalItems += len(spec.Items)

		// A source is captured once however often it is placed. This is the
		// property a flat model cannot have, and the collection exercises it.
		seen := map[string]int{}
		for _, s := range spec.Sources {
			seen[s.Name]++
		}
		for sourceName, n := range seen {
			if n > 1 {
				t.Errorf("%s: source %q captured %d times", name, sourceName, n)
			}
		}

		placements := map[string]int{}
		for _, item := range spec.Items {
			placements[item.Source]++
			if _, ok := seen[item.Source]; !ok {
				t.Errorf("%s: placement of %q has no source entry", name, item.Source)
			}
		}
		for sourceName, n := range placements {
			if n > 1 {
				sawRepeat = true
				// Repeated placements must be distinguishable.
				occ := map[int]bool{}
				for _, item := range spec.Items {
					if item.Source == sourceName {
						occ[item.Occurrence] = true
					}
				}
				if len(occ) != n {
					t.Errorf("%s: %q is placed %d times but has %d distinct occurrence "+
						"indices", name, sourceName, n, len(occ))
				}
			}
		}

		for _, s := range spec.Sources {
			switch s.Type {
			case scenespec.SourceGroup:
				sawGroup = true
				if !isGroup[s.Name] {
					t.Errorf("%s: %q captured as a group but GetGroupList does not list it",
						name, s.Name)
				}
				if s.Kind != "" || len(s.Settings) > 0 {
					t.Errorf("%s: group %q captured a kind or settings; it has neither",
						name, s.Name)
				}

			case scenespec.SourceScene:
				sawNested = true
				if isGroup[s.Name] {
					t.Errorf("%s: %q is a group but was captured as a nested scene -- "+
						"both report OBS_SOURCE_TYPE_SCENE and only isGroup separates them",
						name, s.Name)
				}
				if s.Kind != "" || len(s.Settings) > 0 {
					t.Errorf("%s: nested scene %q captured a kind or settings", name, s.Name)
				}

			case scenespec.SourceInput:
				if s.Kind == "" {
					t.Errorf("%s: input %q captured with no kind; an apply could not "+
						"create it", name, s.Name)
				}
				if len(s.Settings) > 0 {
					sawSettings = true
				}
			}

			if len(s.Filters) > 0 && s.Type != scenespec.SourceInput {
				sawContainerFilter = true
			}
		}

		if _, err := json.Marshal(spec); err != nil {
			t.Errorf("%s: spec does not encode: %v", name, err)
		}
	}

	t.Logf("captured %d scenes: %d source entries, %d placements",
		len(scenes), totalSources, totalItems)
	t.Logf("shapes exercised: group=%v nested=%v repeated placement=%v "+
		"filter on a container=%v input settings=%v",
		sawGroup, sawNested, sawRepeat, sawContainerFilter, sawSettings)

	// The collection is known to hold all of these. If one stops appearing, the
	// capture has started missing it rather than the collection having changed
	// -- and either way the assertion is the thing that says so.
	if !sawGroup {
		t.Error("no group was captured, though GetGroupList reports some")
	}
	if !sawNested {
		t.Error("no nested scene was captured")
	}
	if !sawRepeat {
		t.Error("no source was captured with more than one placement")
	}
}

func TestLiveCaptureReadsEachSourceOnce(t *testing.T) {
	client := liveClient(t)

	// The expensive calls are per source, not per placement. A scene with ten
	// placements of two sources must cost two settings reads, or capturing a
	// collection scales with placements and a layered stack becomes slow for
	// no reason.
	scenes, _, err := client.GetSceneList()
	if err != nil {
		t.Fatalf("GetSceneList: %v", err)
	}
	scenes = operatorScenes(scenes)
	if len(scenes) == 0 {
		t.Skip("no scenes on the main canvas")
	}

	// A fresh counter per scene. Sharing one across the search totals every
	// scene tried and reports a number that has nothing to do with the scene
	// finally measured.
	var target string
	var spec *scenespec.Spec
	var counter *countingReader
	for _, name := range scenes {
		c := &countingReader{Reader: client}
		s, err := scenespec.Capture(c, name)
		if err != nil {
			continue
		}
		if len(s.Items) > len(s.Sources) {
			target, spec, counter = name, s, c
			break
		}
	}
	if spec == nil {
		t.Skip("no scene on this collection places a source more than once")
	}

	if counter.settings > len(spec.Sources) {
		t.Errorf("%s: %d settings reads for %d sources (%d placements); sources must "+
			"be read once each", target, counter.settings, len(spec.Sources), len(spec.Items))
	}
	t.Logf("%s: %d placements, %d sources, %d settings reads",
		target, len(spec.Items), len(spec.Sources), counter.settings)
}

// countingReader counts the per-source reads a capture makes.
type countingReader struct {
	scenespec.Reader
	settings int
}

func (c *countingReader) GetSourceSettings(name string) (map[string]interface{}, error) {
	c.settings++
	return c.Reader.GetSourceSettings(name)
}

// TestLiveDiffOfEveryUnchangedSceneIsQuiet is the test the tolerance and
// defaults handling exist for.
//
// Capturing a scene and immediately diffing it must report nothing. Against the
// fake that is nearly free; against a real OBS it is the whole problem, because
// this is where float32 rounding, defaults absent from GetInputSettings, and
// JSON number types all actually happen. A single spurious finding here would
// appear on every scene an operator ever captured.
func TestLiveDiffOfEveryUnchangedSceneIsQuiet(t *testing.T) {
	client := liveClient(t)

	scenes, _, err := client.GetSceneList()
	if err != nil {
		t.Fatalf("GetSceneList: %v", err)
	}
	scenes = operatorScenes(scenes)

	noisy := 0
	for _, name := range scenes {
		spec, err := scenespec.Capture(client, name)
		if err != nil {
			t.Errorf("capturing %q: %v", name, err)
			continue
		}

		findings, err := scenespec.Diff(client, spec, name)
		if err != nil {
			t.Errorf("diffing %q: %v", name, err)
			continue
		}
		if len(findings) == 0 {
			continue
		}

		noisy++
		t.Errorf("%s: capture then diff reports %d findings against an unchanged scene",
			name, len(findings))
		for i, f := range findings {
			if i == 5 {
				t.Logf("    ... and %d more", len(findings)-5)
				break
			}
			t.Logf("    %s %s .%s: %s", f.Kind, f.Subject, f.Field, f.Detail)
		}
	}

	t.Logf("%d scenes diffed, %d reported spurious findings", len(scenes), noisy)
}

// TestLiveDiffSeesARealChange is the other half: quiet is only a virtue if the
// diff still speaks up. Read-only -- it edits the spec, never the scene.
func TestLiveDiffSeesARealChange(t *testing.T) {
	client := liveClient(t)

	scenes, _, err := client.GetSceneList()
	if err != nil {
		t.Fatalf("GetSceneList: %v", err)
	}
	scenes = operatorScenes(scenes)
	if len(scenes) == 0 {
		t.Skip("no scenes on the main canvas")
	}

	for _, name := range scenes {
		spec, err := scenespec.Capture(client, name)
		if err != nil || len(spec.Items) == 0 || spec.Items[0].Transform == nil {
			continue
		}

		// Move a placement in the document, not in OBS.
		spec.Items[0].Transform.PositionX += 37
		findings, err := scenespec.Diff(client, spec, name)
		if err != nil {
			t.Fatalf("Diff: %v", err)
		}
		for _, f := range findings {
			if f.Kind == scenespec.FindingDrift && f.Field == "transform.position_x" {
				t.Logf("%s: a 37px difference was reported as %s", name, f.Detail)
				return
			}
		}
		t.Fatalf("%s: a 37px difference was not reported; findings: %d", name, len(findings))
	}
	t.Skip("no scene with a placement carrying a transform")
}

// TestLiveCaptureDamageApplyRoundTrips is the acceptance test for the whole
// arc, run against a real OBS.
//
// Unlike every other test in this file it WRITES, so it builds its own scratch
// scene and works only inside it. The operator's scenes are never touched, and
// the scene is removed whether the test passes or fails.
func TestLiveCaptureDamageApplyRoundTrips(t *testing.T) {
	client := liveClient(t)

	scene := fmt.Sprintf("agentic-obs-apply-%d", time.Now().UnixNano())
	if err := client.CreateScene(scene); err != nil {
		t.Fatalf("CreateScene: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveScene(scene); err != nil {
			t.Logf("warning: could not remove scratch scene %q: %v", scene, err)
		}
	})

	// A stack of layers, which is the shape this collection actually uses.
	for i, spec := range []struct {
		name     string
		kind     string
		settings map[string]interface{}
	}{
		{scene + "-back", "color_source_v3", map[string]interface{}{"width": 800, "height": 600}},
		{scene + "-mid", "color_source_v3", map[string]interface{}{"width": 400, "height": 300}},
		{scene + "-front", "color_source_v3", map[string]interface{}{"width": 200, "height": 150}},
	} {
		id, err := client.CreateInput(scene, spec.name, spec.kind, spec.settings)
		if err != nil {
			t.Fatalf("CreateInput %s: %v", spec.name, err)
		}
		transform, err := client.GetSceneItemTransform(scene, id)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}
		transform.PositionX = float64(100 * (i + 1))
		transform.PositionY = float64(50 * (i + 1))
		if err := client.SetSceneItemTransform(scene, id, transform); err != nil {
			t.Fatalf("SetSceneItemTransform: %v", err)
		}
	}

	original, err := scenespec.Capture(client, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	t.Logf("captured %d sources, %d placements", len(original.Sources), len(original.Items))

	// A dry run must change nothing, even against a scene that needs work.
	live, _ := client.GetSceneByName(scene)
	if err := client.RemoveSceneItem(scene, live.Sources[0].ID); err != nil {
		t.Fatalf("RemoveSceneItem: %v", err)
	}
	if _, err := scenespec.Apply(context.Background(), client, original, scene,
		scenespec.ApplyOptions{DryRun: true}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	afterDry, _ := scenespec.Capture(client, scene)
	if len(afterDry.Items) != len(original.Items)-1 {
		t.Errorf("a dry run changed the scene: %d placements, expected %d",
			len(afterDry.Items), len(original.Items)-1)
	}

	// More damage: hide one, move another, change a setting.
	live, _ = client.GetSceneByName(scene)
	for i, item := range live.Sources {
		switch i {
		case 0:
			if err := client.SetSceneItemEnabled(scene, item.ID, false); err != nil {
				t.Fatalf("SetSceneItemEnabled: %v", err)
			}
		case 1:
			transform, err := client.GetSceneItemTransform(scene, item.ID)
			if err != nil {
				t.Fatalf("GetSceneItemTransform: %v", err)
			}
			transform.PositionX += 321
			transform.Rotation = 17
			if err := client.SetSceneItemTransform(scene, item.ID, transform); err != nil {
				t.Fatalf("SetSceneItemTransform: %v", err)
			}
			if err := client.SetSourceSettings(item.Name,
				map[string]interface{}{"width": 77, "height": 88}, false); err != nil {
				t.Fatalf("SetSourceSettings: %v", err)
			}
		}
	}

	report, err := scenespec.Apply(context.Background(), client, original, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	counts := map[string]int{}
	for _, op := range report.Ops {
		counts[op.Result]++
		if op.Result == scenespec.OpFailed {
			t.Errorf("op failed: %s %s: %s", op.Op, op.Subject, op.Detail)
		}
	}
	t.Logf("apply: %v", counts)

	// The proof is a diff: it already knows which differences are real.
	findings, err := scenespec.Diff(client, original, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the scene does not match the spec after applying it:")
		for _, f := range findings {
			t.Errorf("    %s %s .%s: %s", f.Kind, f.Subject, f.Field, f.Detail)
		}
	}

	// And a second apply has nothing to do.
	second, err := scenespec.Apply(context.Background(), client, original, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	for _, op := range second.Ops {
		if op.Result != scenespec.OpUnchanged && op.Result != scenespec.OpSkipped {
			t.Errorf("the second apply still had work: %s %s -> %s (%s)",
				op.Op, op.Subject, op.Result, op.Detail)
		}
	}
}
