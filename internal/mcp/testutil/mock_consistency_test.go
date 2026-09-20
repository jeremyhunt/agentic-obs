package testutil

import (
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// A scene item's state is spread across three maps on this mock: sceneItems
// carries name, enabled, locked and a copy of the geometry; sceneItemTransforms
// carries the authoritative transform; sceneItemLocked carries the lock again.
// Nothing keeps them in step, so two accessors can describe the same item
// differently -- something the real client cannot do, because OBS holds one
// scene item and answers every question from it.
//
// These tests exist to pin the disagreements before the state is unified. A
// double that contradicts itself is not merely untidy: a tool that writes
// through one accessor and reads back through another passes its test and fails
// against OBS. (FB-64)

func item(t *testing.T, m *MockOBSClient, scene string, id int) (x float64, locked bool) {
	t.Helper()

	s, err := m.GetSceneByName(scene)
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	for _, src := range s.Sources {
		if src.ID == id {
			return src.X, src.Locked
		}
	}
	t.Fatalf("scene %q has no item %d", scene, id)
	return 0, false
}

// TestSceneListingReflectsATransformWrite: moving an item must move it
// everywhere. set_source_transform followed by a read of obs://scene/{name} is
// an ordinary sequence, and today the second call reports the old position.
func TestSceneListingReflectsATransformWrite(t *testing.T) {
	m := NewMockOBSClient()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	tr, err := m.GetSceneItemTransform("Scene 1", 1)
	if err != nil {
		t.Fatalf("GetSceneItemTransform: %v", err)
	}
	tr.PositionX = 640
	if err := m.SetSceneItemTransform("Scene 1", 1, tr); err != nil {
		t.Fatalf("SetSceneItemTransform: %v", err)
	}

	// The transform accessor agrees with itself.
	back, err := m.GetSceneItemTransform("Scene 1", 1)
	if err != nil {
		t.Fatalf("GetSceneItemTransform: %v", err)
	}
	if back.PositionX != 640 {
		t.Errorf("transform reads back x=%v, want 640", back.PositionX)
	}

	// ...and so must the scene listing, which serves obs://scene/{name}.
	if x, _ := item(t, m, "Scene 1", 1); x != 640 {
		t.Errorf("scene listing reports x=%v after moving the item to 640; "+
			"the listing and the transform describe the same item", x)
	}
}

// TestSceneListingReflectsALockWrite: the same split, with the lock state
// stored twice outright.
func TestSceneListingReflectsALockWrite(t *testing.T) {
	m := NewMockOBSClient()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := m.SetSceneItemLocked("Scene 1", 1, true); err != nil {
		t.Fatalf("SetSceneItemLocked: %v", err)
	}

	got, err := m.GetSceneItemLocked("Scene 1", 1)
	if err != nil {
		t.Fatalf("GetSceneItemLocked: %v", err)
	}
	if !got {
		t.Error("GetSceneItemLocked reports unlocked after locking")
	}

	if _, locked := item(t, m, "Scene 1", 1); !locked {
		t.Error("scene listing reports the item unlocked after locking it")
	}
}

// TestSceneListingReflectsAVisibilityWrite: obs.SceneSource carries the same
// fact under two names, and the real client sets both from sceneItemEnabled
// (FB-60). A double that updates only one reproduces exactly the defect FB-60
// fixed -- and, being a double, would let a test assert the broken behaviour is
// correct.
func TestSceneListingReflectsAVisibilityWrite(t *testing.T) {
	m := NewMockOBSClient()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := m.SetSceneItemEnabled("Scene 1", 1, false); err != nil {
		t.Fatalf("SetSceneItemEnabled: %v", err)
	}

	s, err := m.GetSceneByName("Scene 1")
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	for _, src := range s.Sources {
		if src.ID != 1 {
			continue
		}
		if src.Enabled {
			t.Error("scene listing reports the item enabled after hiding it")
		}
		if src.Visible {
			t.Error("scene listing reports the item visible after hiding it; " +
				"Visible and Enabled are the same fact")
		}
		return
	}
	t.Fatal("item 1 is missing from the scene listing")
}

// TestRemovingASceneItemRemovesItEverywhere: a removal that clears one map and
// not the others leaves an item that is gone from the listing but still
// addressable by transform -- so a test can go on mutating something OBS would
// say does not exist.
func TestRemovingASceneItemRemovesItEverywhere(t *testing.T) {
	m := NewMockOBSClient()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := m.RemoveSceneItem("Scene 1", 1); err != nil {
		t.Fatalf("RemoveSceneItem: %v", err)
	}

	s, err := m.GetSceneByName("Scene 1")
	if err != nil {
		t.Fatalf("GetSceneByName: %v", err)
	}
	for _, src := range s.Sources {
		if src.ID == 1 {
			t.Fatal("removed item is still in the scene listing")
		}
	}

	if _, err := m.GetSceneItemTransform("Scene 1", 1); err == nil {
		t.Error("a removed item still has a readable transform")
	}
	if err := m.SetSceneItemTransform("Scene 1", 1, &obs.SceneItemTransform{ScaleX: 1, ScaleY: 1, BoundsType: "OBS_BOUNDS_NONE", BoundsWidth: 1, BoundsHeight: 1}); err == nil {
		t.Error("a removed item can still be moved")
	}
	if _, err := m.GetSceneItemLocked("Scene 1", 1); err == nil {
		t.Error("a removed item still has a readable lock state")
	}
}

// TestCreatedInputIsListed: an input created through the mock must appear in the
// input list, as it does in OBS.
//
// It did not. CreateInput wrote to sceneItems, sceneItemTransforms,
// sceneItemLocked and sourceSettings, and never to the slice ListSources
// returns -- so anything asking "does this input already exist?" was told no,
// forever. ensure_input asks exactly that, and reported "created" on every call
// as a result.
//
// Same shape as the three disagreements FB-64 fixed: state spread across
// several fields with nothing keeping them in step. obstest.Fake keeps one
// world and cannot have this bug, which is the argument for folding this double
// onto it. (FB-71)
func TestCreatedInputIsListed(t *testing.T) {
	m := NewMockOBSClient()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if _, err := m.CreateInput("Scene 1", "Fresh Source", "color_source_v3", nil); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}

	inputs, err := m.ListSources()
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	for _, in := range inputs {
		if in != nil && in.InputName == "Fresh Source" {
			if in.InputKind != "color_source_v3" {
				t.Errorf("listed kind is %q, want color_source_v3", in.InputKind)
			}
			return
		}
	}
	t.Error("a created input is absent from ListSources; nothing can discover it exists")
}

// TestCreatedInputHasReadableSettings: creating with no settings must still
// leave the input readable, rather than storing a nil map that reads back as a
// missing source.
func TestCreatedInputHasReadableSettings(t *testing.T) {
	m := NewMockOBSClient()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if _, err := m.CreateInput("Scene 1", "Fresh Source", "color_source_v3", nil); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}

	if _, err := m.GetSourceSettings("Fresh Source"); err != nil {
		t.Errorf("settings for a freshly created input are unreadable: %v", err)
	}
}
