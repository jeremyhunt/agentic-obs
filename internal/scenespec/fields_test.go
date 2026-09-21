package scenespec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs/obstest"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// A spec describes a whole scene, but a caller often owns only part of one. A
// scene preset is the case already in this repo: it is a list of which sources
// are visible and nothing else, and reconciling it must not touch a transform
// somebody moved on purpose.
//
// Fields is that restriction. The dangerous version of this feature is one that
// silently ignores a name it does not recognise, because the result is an apply
// that does nothing and reports success.

// damage moves a source, hides a placement and rewrites a setting, so a diff has
// something to say about three different aspects at once.
func damage(t *testing.T, f *obstest.Fake, scene, source string) {
	t.Helper()

	sc, err := f.GetSceneByName(scene)
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	for _, item := range sc.Sources {
		if item.Name != source {
			continue
		}
		transform, err := f.GetSceneItemTransform(scene, item.ID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}
		transform.PositionX += 321
		if err := f.SetSceneItemTransform(scene, item.ID, transform); err != nil {
			t.Fatalf("SetSceneItemTransform: %v", err)
		}
		if err := f.SetSceneItemEnabled(scene, item.ID, false); err != nil {
			t.Fatalf("SetSceneItemEnabled: %v", err)
		}
		if err := f.SetSourceSettings(source,
			map[string]interface{}{"url": "http://localhost:8791/moved"}, true); err != nil {
			t.Fatalf("SetSourceSettings: %v", err)
		}
		return
	}
	t.Fatalf("no placement of %q in %q", source, scene)
}

func aspects(findings []scenespec.Finding) map[string]int {
	seen := map[string]int{}
	for _, f := range findings {
		key := f.Field
		if i := strings.IndexByte(key, '.'); i >= 0 {
			key = key[:i]
		}
		if key == "" {
			key = "source"
		}
		seen[key]++
	}
	return seen
}

func TestDiffWithoutFieldsReportsEveryAspect(t *testing.T) {
	// The control. Without it, the test below could pass because the damage
	// never happened.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)
	damage(t, f, scene, sharedOverlay)

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	got := aspects(findings)
	for _, want := range []string{"transform", "enabled", "settings"} {
		if got[want] == 0 {
			t.Errorf("no %s finding; the damage did not land. got %v", want, got)
		}
	}
}

func TestDiffWithFieldsReportsOnlyThose(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)
	damage(t, f, scene, sharedOverlay)

	findings, err := scenespec.DiffWith(f, spec, scene,
		scenespec.DiffOptions{Fields: []string{"enabled"}})
	if err != nil {
		t.Fatalf("DiffWith: %v", err)
	}
	got := aspects(findings)
	if got["enabled"] == 0 {
		t.Error("the aspect that was asked for is missing")
	}
	delete(got, "enabled")
	if len(got) != 0 {
		t.Errorf("aspects nobody asked about were reported: %v", got)
	}
}

func TestApplyWithFieldsWritesOnlyThose(t *testing.T) {
	// What a scene preset is: restore visibility, leave the geometry alone,
	// because somebody moved that on purpose.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)
	damage(t, f, scene, sharedOverlay)

	before := liveTransform(t, f, scene, sharedOverlay)
	moved := before.PositionX

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{Fields: []string{"enabled"}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sc, _ := f.GetSceneByName(scene)
	for _, item := range sc.Sources {
		if item.Name == sharedOverlay && !item.Enabled {
			t.Error("visibility was asked for and not restored")
		}
	}
	if after := liveTransform(t, f, scene, sharedOverlay); after.PositionX != moved {
		t.Errorf("the transform was rewritten at %v; only enabled was asked for", after.PositionX)
	}
	settings, _ := f.GetSourceSettings(sharedOverlay)
	if url, _ := settings["url"].(string); !strings.Contains(url, "moved") {
		t.Errorf("the settings were rewritten to %q; only enabled was asked for", url)
	}
}

func TestApplyWithFieldsDoesNotCreateWhatItWasNotAskedFor(t *testing.T) {
	// Restricting to visibility must not quietly put a deleted layer back.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	live, _ := f.GetSceneByName(scene)
	before := len(live.Sources)
	if err := f.RemoveSceneItem(scene, live.Sources[0].ID); err != nil {
		t.Fatalf("RemoveSceneItem: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{Fields: []string{"enabled"}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after, _ := f.GetSceneByName(scene)
	if len(after.Sources) != before-1 {
		t.Errorf("the scene has %d placements, want %d: a placement was restored "+
			"by an apply restricted to visibility", len(after.Sources), before-1)
	}
}

func TestAnUnknownFieldIsRefusedRatherThanIgnored(t *testing.T) {
	// The failure this exists to prevent: a typo that silently matches nothing,
	// so the diff reports no differences and the apply writes nothing, and both
	// look like success.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)
	damage(t, f, scene, sharedOverlay)

	_, err := scenespec.DiffWith(f, spec, scene,
		scenespec.DiffOptions{Fields: []string{"enbaled"}})
	if err == nil {
		t.Fatal("DiffWith accepted a field name that matches nothing")
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Errorf("the error does not name the valid fields: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{Fields: []string{"enbaled"}}); err == nil {
		t.Error("Apply accepted a field name that matches nothing")
	}
}

func TestFieldsAreCaseInsensitive(t *testing.T) {
	// The value arrives from JSON written by an agent, and rejecting "Enabled"
	// would be a refusal with no reason behind it.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)
	damage(t, f, scene, sharedOverlay)

	findings, err := scenespec.DiffWith(f, spec, scene,
		scenespec.DiffOptions{Fields: []string{"Enabled"}})
	if err != nil {
		t.Fatalf("DiffWith: %v", err)
	}
	if len(findings) == 0 {
		t.Error("no findings for a field that differs only by case")
	}
}

func TestAReportSaysWhatItLookedAt(t *testing.T) {
	// A report from a restricted apply that did not say it was restricted reads
	// as "the scene now matches the spec". It does not, and nothing else in the
	// report would show the difference.
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)
	damage(t, f, scene, sharedOverlay)

	report, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{Fields: []string{"Enabled"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Fields) != 1 || report.Fields[0] != "enabled" {
		t.Errorf("the report says it looked at %v, want [enabled] normalised", report.Fields)
	}

	// An unrestricted apply says nothing rather than listing all ten, because
	// "everything" is the default and naming it adds noise to every report.
	full, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(full.Fields) != 0 {
		t.Errorf("an unrestricted report lists %v", full.Fields)
	}
}
