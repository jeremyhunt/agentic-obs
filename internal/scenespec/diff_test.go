package scenespec_test

import (
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// A diff is only useful if it is quiet when nothing changed. Every test below
// exists because a naive comparison reports drift that is not there, and a diff
// that cries wolf gets ignored exactly when it matters.

func TestDiffOfAnUnchangedSceneIsEmpty(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("capture then diff reports %d findings against the scene it came "+
			"from:\n%s", len(findings), render(findings))
	}
}

func TestDiffIgnoresFloatNoiseAndReportsRealMovement(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	item := spec.Items[0]

	// OBS stores transforms as float32, so a value that went out as a float64
	// comes back rounded. Reporting that as drift makes every capture-then-diff
	// noisy, and the noise is indistinguishable from a real nudge.
	spec.Items[0].Transform.PositionX += 0.0001

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a sub-float32 difference was reported as drift:\n%s", render(findings))
	}

	// A real move must still be caught.
	spec.Items[0].Transform.PositionX = item.Transform.PositionX + 12
	findings, err = scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !hasFinding(findings, scenespec.FindingDrift, item.Source) {
		t.Errorf("a 12px move was not reported:\n%s", render(findings))
	}
}

func TestDiffDoesNotReportAStoredDefaultAsDrift(t *testing.T) {
	f, scene := fixture(t)

	// A source of a kind the fake knows defaults for. GetInputSettings returns
	// only what *differs* from those defaults, so a spec that stored a value
	// equal to one reads as "spec says X, live says nothing". That is the single
	// most common false positive a settings diff can have: it fires on every
	// source whose document happens to mention a default.
	const name, kind = "BG_haze", "color_source_v3"
	if _, err := f.CreateInput(scene, name, kind, nil); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}
	defaults, err := f.GetInputDefaultSettings(kind)
	if err != nil {
		t.Fatalf("GetInputDefaultSettings: %v", err)
	}
	if len(defaults) == 0 {
		t.Fatalf("%s reports no defaults, so this test would prove nothing", kind)
	}

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Write every default into the spec explicitly. Live still reports none of
	// them, because they are defaults.
	for i := range spec.Sources {
		if spec.Sources[i].Name != name {
			continue
		}
		if spec.Sources[i].Settings == nil {
			spec.Sources[i].Settings = map[string]interface{}{}
		}
		for k, v := range defaults {
			spec.Sources[i].Settings[k] = v
		}
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if hasFinding(findings, scenespec.FindingDrift, name) {
		t.Errorf("a setting stored at its default was reported as drift:\n%s",
			render(findings))
	}

	// And a value that genuinely differs from a default is still caught.
	for i := range spec.Sources {
		if spec.Sources[i].Name == name {
			spec.Sources[i].Settings["width"] = 1234.0
		}
	}
	findings, err = scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !hasFinding(findings, scenespec.FindingDrift, name) {
		t.Errorf("a setting that differs from its default was not reported:\n%s",
			render(findings))
	}
}

func TestDiffComparesOrderByRelativeRankNotAbsoluteIndex(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// Something the spec does not manage is inserted at the front. Every managed
	// item's absolute index shifts by one, but nothing about their order
	// relative to each other has changed, so an index comparison would report
	// every single placement as drift.
	//
	// Adding it at the *end* would not shift anything, and a test that did that
	// would pass against an absolute-index comparison -- proving nothing.
	id, err := f.CreateInput(scene, "unmanaged-overlay", "color_source_v3", nil)
	if err != nil {
		t.Fatalf("CreateInput: %v", err)
	}
	if err := f.SetSceneItemIndex(scene, id, 0); err != nil {
		t.Fatalf("SetSceneItemIndex: %v", err)
	}

	// Guard the guard: if the insert did not move anything, this test is
	// checking nothing.
	live, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if live.Items[0].Source != "unmanaged-overlay" {
		t.Fatalf("the unmanaged item is at position %d, so no managed index shifted",
			indexOf(live, "unmanaged-overlay"))
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	for _, f := range findings {
		if f.Kind == scenespec.FindingDrift && f.Field == "order" {
			t.Errorf("order drift reported although only relative rank matters:\n%s",
				render(findings))
			break
		}
	}

	// The new source is not drift, it is something the spec does not describe.
	if !hasFinding(findings, scenespec.FindingUnmanaged, "unmanaged-overlay") {
		t.Errorf("an item the spec does not describe was not reported as unmanaged:\n%s",
			render(findings))
	}
}

func TestDiffReportsWhatIsMissing(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// One of jurmiey_avatar's two placements. The source survives, because the
	// other placement still references it, so this isolates a *placement* going
	// missing from a *source* going missing. Removing a source's only placement
	// would produce both findings, and the test would pass on either -- which is
	// how a placement-level check quietly stops checking anything.
	live, err := f.GetSceneByName(scene)
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	removed := false
	for _, item := range live.Sources {
		if item.Name == "jurmiey_avatar" {
			if err := f.RemoveSceneItem(scene, item.ID); err != nil {
				t.Fatalf("RemoveSceneItem: %v", err)
			}
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("fixture did not contain the placement this test removes")
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	found := false
	for _, f := range findings {
		if f.Kind == scenespec.FindingMissing && f.Subject == "jurmiey_avatar" &&
			f.Field == "placement" {
			found = true
		}
	}
	if !found {
		t.Errorf("a placement in the spec and not in the scene was not reported "+
			"missing:\n%s", render(findings))
	}

	// The source itself is still there, so it must not be reported missing.
	// Checked on the source-level finding specifically -- Field empty -- because
	// a check that ignored Field would match the placement finding above and
	// pass no matter what.
	sourceMissing := false
	for _, f := range findings {
		if f.Kind == scenespec.FindingMissing && f.Subject == "jurmiey_avatar" && f.Field == "" {
			sourceMissing = true
		}
	}
	if sourceMissing {
		t.Errorf("the source was reported missing although another placement still "+
			"references it:\n%s", render(findings))
	}
}

func TestDiffTreatsTheSameNumberAsTheSameValue(t *testing.T) {
	f, scene := fixture(t)

	const name, kind = "BG_haze", "color_source_v3"
	if _, err := f.CreateInput(scene, name, kind,
		map[string]interface{}{"width": 1920.0}); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// A spec that has been through JSON -- which every spec arriving over MCP
	// has -- carries its numbers as float64. A spec written in Go carries them
	// as int. They are the same value, and reporting drift between 1920 and
	// 1920.0 would make every hand-written spec permanently dirty.
	for i := range spec.Sources {
		if spec.Sources[i].Name == name {
			spec.Sources[i].Settings["width"] = 1920 // int, not float64
		}
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if hasFinding(findings, scenespec.FindingDrift, name) {
		t.Errorf("1920 and 1920.0 were reported as different:\n%s", render(findings))
	}
}

func indexOf(spec *scenespec.Spec, source string) int {
	for i, item := range spec.Items {
		if item.Source == source {
			return i
		}
	}
	return -1
}

func TestDiffReportsAKindMismatchRatherThanDrift(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// A source whose kind changed is not a source whose settings drifted: no
	// settings write reconciles it, and an apply has to remove and recreate,
	// which destroys the placements. It gets its own finding so a plan cannot
	// quietly treat it as fixable.
	for i := range spec.Sources {
		if spec.Sources[i].Name == "jurmiey_avatar" {
			spec.Sources[i].Kind = "browser_source"
		}
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !hasFinding(findings, scenespec.FindingKindMismatch, "jurmiey_avatar") {
		t.Errorf("a changed input kind was not reported as a kind mismatch:\n%s",
			render(findings))
	}
}

func TestDiffTellsARenameFromADeleteAndCreate(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// A renamed source keeps its uuid. Without checking it, a rename looks like
	// one source missing and another unmanaged -- and an apply built on that
	// would recreate the source and orphan the original.
	for i := range spec.Sources {
		if spec.Sources[i].Name == "jurmiey_avatar" {
			spec.Sources[i].Name = "avatar_old_name"
			spec.Sources[i].UUID = f.SourceUUID("jurmiey_avatar")
		}
	}
	for i := range spec.Items {
		if spec.Items[i].Source == "jurmiey_avatar" {
			spec.Items[i].Source = "avatar_old_name"
		}
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !hasFinding(findings, scenespec.FindingRenamed, "avatar_old_name") {
		t.Errorf("a source with the same uuid under a new name was not reported as "+
			"renamed:\n%s", render(findings))
	}
}

func TestDiffRefusesASpecForAnotherScene(t *testing.T) {
	f, scene := fixture(t)
	spec, _ := scenespec.Capture(f, scene)

	// Diffing a spec against a scene it was not captured from is almost always
	// a mistake, and the output would be a wall of missing and unmanaged that
	// buries it.
	if _, err := scenespec.Diff(f, spec, "Other"); err == nil {
		t.Error("diffing a spec against a different scene succeeded silently")
	}
}

func hasFinding(findings []scenespec.Finding, kind, subject string) bool {
	for _, f := range findings {
		if f.Kind == kind && f.Subject == subject {
			return true
		}
	}
	return false
}

func render(findings []scenespec.Finding) string {
	out := ""
	for _, f := range findings {
		out += "  " + f.Kind + " " + f.Subject
		if f.Field != "" {
			out += " ." + f.Field
		}
		out += ": " + f.Detail + "\n"
	}
	if out == "" {
		return "  (none)"
	}
	return out
}

// keep obs imported for the transform type used above
var _ = obs.SceneItemTransform{}
