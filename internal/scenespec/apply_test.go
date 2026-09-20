package scenespec_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs/obstest"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// An apply is the only part of this package that writes, and it writes to a
// scene that may be on air. Every test here is about a way it could do damage
// or lie about what it did.

func TestApplyDryRunChangesNothing(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// Knock the scene out of shape so there is real work to plan.
	live, _ := f.GetSceneByName(scene)
	if err := f.RemoveSceneItem(scene, live.Sources[0].ID); err != nil {
		t.Fatalf("RemoveSceneItem: %v", err)
	}

	before, _ := scenespec.Capture(f, scene)

	report, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !report.DryRun {
		t.Error("the report does not say it was a dry run")
	}
	if len(report.Ops) == 0 {
		t.Error("a dry run against a changed scene planned no operations")
	}

	after, _ := scenespec.Capture(f, scene)
	if len(after.Items) != len(before.Items) {
		t.Errorf("a dry run changed the scene: %d placements before, %d after",
			len(before.Items), len(after.Items))
	}
}

func TestApplyRestoresARemovedPlacement(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	live, _ := f.GetSceneByName(scene)
	var removed string
	for _, item := range live.Sources {
		if item.Name == "OVERLAY_NowPlaying" {
			removed = item.Name
			if err := f.RemoveSceneItem(scene, item.ID); err != nil {
				t.Fatalf("RemoveSceneItem: %v", err)
			}
		}
	}
	if removed == "" {
		t.Fatal("fixture did not contain the placement this test removes")
	}

	report, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after, _ := scenespec.Capture(f, scene)
	if countItems(after, removed) != 1 {
		t.Errorf("%q was not put back; report:\n%s", removed, renderOps(report))
	}
}

func TestApplyTwiceReportsNothingLeftToDo(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	live, _ := f.GetSceneByName(scene)
	if err := f.RemoveSceneItem(scene, live.Sources[0].ID); err != nil {
		t.Fatalf("RemoveSceneItem: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// The second apply is the real test. An apply that writes unconditionally
	// looks identical to one that converges, until something is watching the
	// scene -- every write is an event, and a rule reacting to it fires again.
	second, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	for _, op := range second.Ops {
		if op.Result != scenespec.OpUnchanged && op.Result != scenespec.OpSkipped {
			t.Errorf("the second apply still had work to do: %s %s -> %s (%s)",
				op.Op, op.Subject, op.Result, op.Detail)
		}
	}
}

func TestApplyReturnsThePreviousStateSoItCanBeUndone(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	live, _ := f.GetSceneByName(scene)
	if err := f.RemoveSceneItem(scene, live.Sources[0].ID); err != nil {
		t.Fatalf("RemoveSceneItem: %v", err)
	}
	damaged, _ := scenespec.Capture(f, scene)

	report, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// An apply to a live scene is not undoable unless the state it replaced
	// comes back with it. Nothing else records what the scene looked like.
	if report.Before == nil {
		t.Fatal("the report carries no pre-apply capture, so the apply cannot be undone")
	}
	if len(report.Before.Items) != len(damaged.Items) {
		t.Errorf("the pre-apply capture has %d placements, the scene had %d",
			len(report.Before.Items), len(damaged.Items))
	}
}

func TestApplyLeavesUnmanagedThingsAlone(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	if _, err := f.CreateInput(scene, "operator-overlay", "color_source_v3", nil); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The default has to be keep. A scene almost always holds things the spec
	// was never meant to own, and removing them is the most destructive thing
	// an apply can do -- it is also unrecoverable if the source was configured
	// by hand.
	after, _ := scenespec.Capture(f, scene)
	if countItems(after, "operator-overlay") != 1 {
		t.Error("an apply removed something the spec does not describe")
	}
}

func TestApplyRemovesUnmanagedPlacementsButNeverInputs(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	if _, err := f.CreateInput(scene, "operator-overlay", "color_source_v3", nil); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}
	// The same input placed in another scene, which must survive.
	if err := f.CreateScene("Elsewhere"); err != nil {
		t.Fatalf("CreateScene: %v", err)
	}
	if _, err := f.CreateSceneItem("Elsewhere", "operator-overlay", true); err != nil {
		t.Fatalf("CreateSceneItem: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{OnUnmanaged: scenespec.UnmanagedRemove}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after, _ := scenespec.Capture(f, scene)
	if countItems(after, "operator-overlay") != 0 {
		t.Error("on_unmanaged=remove left the placement in the scene")
	}

	// And everything the spec *does* describe has to survive. Checking only
	// that the unmanaged item went would pass just as happily if the prune had
	// emptied the scene, which is the failure that would actually hurt.
	for _, want := range spec.Items {
		if countItems(after, want.Source) == 0 {
			t.Errorf("prune removed %q, which the spec describes", want.Source)
		}
	}
	if len(after.Items) != len(spec.Items) {
		t.Errorf("the scene has %d placements after pruning, the spec has %d",
			len(after.Items), len(spec.Items))
	}

	// RemoveSceneItem, never RemoveInput. An input is a shared object: removing
	// it blanks it in every other scene showing it.
	elsewhere, err := scenespec.Capture(f, "Elsewhere")
	if err != nil {
		t.Fatalf("capturing Elsewhere: %v", err)
	}
	if countItems(elsewhere, "operator-overlay") != 1 {
		t.Error("removing an unmanaged placement destroyed the input, blanking it " +
			"in another scene")
	}
}

func TestApplyCannotCreateAGroupAndSaysSo(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// obs-websocket has CreateScene and no CreateGroup, so a spec naming a group
	// that does not exist is not appliable. Reporting it as failed would suggest
	// retrying; reporting success would be a lie.
	f2 := obstest.NewFake()
	if err := f2.CreateScene(scene); err != nil {
		t.Fatalf("CreateScene: %v", err)
	}

	report, err := scenespec.Apply(context.Background(), f2, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	found := false
	for _, op := range report.Ops {
		if op.Subject == "MERCH" && op.Result == scenespec.OpSkipped {
			found = true
			if !strings.Contains(op.Detail, "group") {
				t.Errorf("the skip does not say why: %q", op.Detail)
			}
		}
	}
	if !found {
		t.Errorf("a missing group was not reported as skipped:\n%s", renderOps(report))
	}
}

func TestApplyRefusesAKindMismatchByDefault(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	for i := range spec.Sources {
		if spec.Sources[i].Name == "jurmiey_avatar" {
			spec.Sources[i].Kind = "browser_source"
		}
	}

	// Reconciling this means removing the input and recreating it, which
	// destroys every placement of it in every scene. An apply does not do that
	// on its own.
	report, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	found := false
	for _, op := range report.Ops {
		if op.Subject == "jurmiey_avatar" && op.Result == scenespec.OpFailed {
			found = true
		}
	}
	if !found {
		t.Errorf("a kind mismatch was not reported as failed:\n%s", renderOps(report))
	}

	// And the source must be untouched.
	after, _ := scenespec.Capture(f, scene)
	for _, s := range after.Sources {
		if s.Name == "jurmiey_avatar" && s.Kind != "image_source" {
			t.Errorf("the source kind changed to %q; an apply must not recreate a "+
				"source to satisfy a kind mismatch", s.Kind)
		}
	}
}

func TestApplyReportsAFailedOpAndKeepsGoing(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	live, _ := f.GetSceneByName(scene)
	for _, item := range live.Sources {
		if item.Name == "OVERLAY_NowPlaying" || item.Name == "AI_AVATARS" {
			if err := f.RemoveSceneItem(scene, item.ID); err != nil {
				t.Fatalf("RemoveSceneItem: %v", err)
			}
		}
	}

	// One operation fails. A partial apply is the normal case against a live
	// OBS -- a source can be locked, a file can be missing -- so a failure has
	// to be a per-op result, not an aborted run that leaves the scene half done
	// with no record of how far it got.
	failing := &failOn{Fake: f, source: "OVERLAY_NowPlaying"}

	report, err := scenespec.Apply(context.Background(), failing, spec, scene,
		scenespec.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply returned an error rather than a report: %v", err)
	}

	sawFailure, sawSuccess, sawLater := false, false, false
	for _, op := range report.Ops {
		if op.Result == scenespec.OpFailed && op.Subject == "OVERLAY_NowPlaying" {
			sawFailure = true
		}
		if op.Result == scenespec.OpCreated && op.Subject == "AI_AVATARS" {
			sawSuccess = true
		}
		// Sources are processed in name order, and "jurmiey_avatar" sorts after
		// "OVERLAY_NowPlaying". An op for it is therefore proof the run carried
		// on *past* the failure -- where AI_AVATARS only proves it got that far
		// before failing, which any abort would also manage.
		if op.Op == "placement" && op.Subject == "jurmiey_avatar" {
			sawLater = true
		}
	}
	if !sawFailure {
		t.Errorf("the failing operation was not reported:\n%s", renderOps(report))
	}
	if !sawSuccess {
		t.Errorf("work before the failure was lost:\n%s", renderOps(report))
	}
	if !sawLater {
		t.Errorf("one failure stopped everything after it:\n%s", renderOps(report))
	}
}

func TestApplyDoesNotDisturbAnotherSceneSharingASource(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// OVERLAY_NowPlaying is in this scene and in Other. Applying here writes the
	// shared input's settings, which is correct -- there is one object -- but it
	// must not touch Other's placement of it.
	otherBefore, err := scenespec.Capture(f, "Other")
	if err != nil {
		t.Fatalf("capturing Other: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{OnUnmanaged: scenespec.UnmanagedRemove}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	otherAfter, err := scenespec.Capture(f, "Other")
	if err != nil {
		t.Fatalf("capturing Other: %v", err)
	}
	if len(otherAfter.Items) != len(otherBefore.Items) {
		t.Errorf("applying %q changed scene Other: %d placements before, %d after",
			scene, len(otherBefore.Items), len(otherAfter.Items))
	}
}

func TestApplyRefusesAPartialSpec(t *testing.T) {
	f, scene := fixture(t)

	// A spec captured without settings describes a layout, not a scene. Applying
	// one would write empty settings over every source in it.
	spec, err := scenespec.CaptureWith(f, scene, scenespec.Options{IncludeFilters: true})
	if err != nil {
		t.Fatalf("CaptureWith: %v", err)
	}
	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err == nil {
		t.Error("applying a spec captured without settings was allowed")
	}
}

// failOn makes one source's writes fail, so a partial apply can be tested.
type failOn struct {
	*obstest.Fake
	source string
}

func (f *failOn) CreateSceneItem(sceneName, sourceName string, enabled bool) (int, error) {
	if sourceName == f.source {
		return 0, fmt.Errorf("simulated failure placing %q", sourceName)
	}
	return f.Fake.CreateSceneItem(sceneName, sourceName, enabled)
}

func renderOps(r *scenespec.Report) string {
	out := ""
	for _, op := range r.Ops {
		out += fmt.Sprintf("  %-10s %-22s %-10s %s\n", op.Op, op.Subject, op.Result, op.Detail)
	}
	if out == "" {
		return "  (no operations)"
	}
	return out
}

func TestCaptureDamageApplyRoundTrips(t *testing.T) {
	f, scene := fixture(t)
	original, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Damage the scene several ways at once, the way an operator would over a
	// session: something removed, something hidden, something moved, a setting
	// changed.
	live, _ := f.GetSceneByName(scene)
	for _, item := range live.Sources {
		switch item.Name {
		case "OVERLAY_NowPlaying":
			if err := f.RemoveSceneItem(scene, item.ID); err != nil {
				t.Fatalf("RemoveSceneItem: %v", err)
			}
		case "AI_AVATARS":
			if err := f.SetSceneItemEnabled(scene, item.ID, false); err != nil {
				t.Fatalf("SetSceneItemEnabled: %v", err)
			}
		case "MERCH":
			transform, err := f.GetSceneItemTransform(scene, item.ID)
			if err != nil {
				t.Fatalf("GetSceneItemTransform: %v", err)
			}
			transform.PositionX += 250
			if err := f.SetSceneItemTransform(scene, item.ID, transform); err != nil {
				t.Fatalf("SetSceneItemTransform: %v", err)
			}
		}
	}
	if err := f.SetSourceSettings("jurmiey_avatar",
		map[string]interface{}{"file": "wrong.png"}, false); err != nil {
		t.Fatalf("SetSourceSettings: %v", err)
	}

	if _, err := scenespec.Apply(context.Background(), f, original, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The proof is a diff rather than a field-by-field comparison. The diff
	// already knows which differences are real and which are float noise or
	// stored defaults, and restating that judgement here would be a second
	// implementation of it and a second chance to get it wrong.
	findings, err := scenespec.Diff(f, original, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the scene does not match the spec after applying it:\n%s",
			render(findings))
	}
}
