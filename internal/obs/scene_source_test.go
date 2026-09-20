package obs

import (
	"reflect"
	"testing"

	"github.com/andreykaipov/goobs/api/typedefs"
)

// TestSceneSourceFromItemCarriesVisibility pins the field that was silently
// dropped: GetSceneByName built a SceneSource without ever setting Visible, so
// the obs://scene/{name} resource reported "visible": false for every source in
// every scene, sitting directly beneath an "enabled" field that told the truth.
//
// obs-websocket has one notion of showing -- sceneItemEnabled -- so the two
// fields are the same fact under two names, and both must carry it. (FB-60)
func TestSceneSourceFromItemCarriesVisibility(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		got := sceneSourceFromItem(typedefs.SceneItem{
			SceneItemID:      7,
			SourceName:       "OVERLAY_NowPlaying",
			SourceType:       "OBS_SOURCE_TYPE_INPUT",
			SceneItemEnabled: enabled,
		})

		if got.Enabled != enabled {
			t.Errorf("enabled=%v: Enabled is %v", enabled, got.Enabled)
		}
		if got.Visible != enabled {
			t.Errorf("enabled=%v: Visible is %v; it reports the same fact as Enabled", enabled, got.Visible)
		}
	}
}

// TestSceneSourceFromItemCopiesEveryField is the guard against the next dropped
// field. It sets everything to a distinct non-zero value, so a field left behind
// in the conversion shows up as a zero the test names -- rather than waiting for
// someone to notice the resource is wrong.
//
// It walks SceneSource by reflection rather than listing the fields. The list
// version did not do what this comment claims: IsGroup was added to the struct
// and left out of the conversion, and this test passed, because a hand-written
// list only guards the fields someone remembered to add to it. A reflective
// walk fails the moment a new field is not carried, which is the whole point.
func TestSceneSourceFromItemCopiesEveryField(t *testing.T) {
	got := sceneSourceFromItem(typedefs.SceneItem{
		SceneItemID:        42,
		SourceName:         "StartingSoon_Room",
		SourceType:         "OBS_SOURCE_TYPE_INPUT",
		SceneItemEnabled:   true,
		SceneItemLocked:    true,
		IsGroup:            true,
		SourceUuid:         "7f3b6da3-9dc8-47a1-a544-e950ef997f4f",
		InputKind:          "browser_source",
		SceneItemBlendMode: "OBS_BLEND_MULTIPLY",
		SceneItemTransform: typedefs.SceneItemTransform{
			PositionX: 120, PositionY: 80,
			Width: 1920, Height: 1080,
			ScaleX: 1.5, ScaleY: 2.5,
			Rotation: 45,
		},
	})

	// Every input above is non-zero, so every field of the result must be too.
	// A zero here means the conversion dropped it.
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("%s is the zero value; sceneSourceFromItem dropped it, and every "+
				"field of the scene item it was built from was set",
				v.Type().Field(i).Name)
		}
	}

	checks := []struct {
		field string
		got   interface{}
		want  interface{}
	}{
		{"ID", got.ID, 42},
		{"Name", got.Name, "StartingSoon_Room"},
		{"Type", got.Type, "OBS_SOURCE_TYPE_INPUT"},
		{"Enabled", got.Enabled, true},
		{"Visible", got.Visible, true},
		{"Locked", got.Locked, true},
		{"X", got.X, 120.0},
		{"Y", got.Y, 80.0},
		{"Width", got.Width, 1920.0},
		{"Height", got.Height, 1080.0},
		{"ScaleX", got.ScaleX, 1.5},
		{"ScaleY", got.ScaleY, 2.5},
		{"Rotation", got.Rotation, 45.0},
		{"IsGroup", got.IsGroup, true},
		{"UUID", got.UUID, "7f3b6da3-9dc8-47a1-a544-e950ef997f4f"},
		{"Kind", got.Kind, "browser_source"},
		{"BlendMode", got.BlendMode, "OBS_BLEND_MULTIPLY"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.field, c.got, c.want)
		}
	}
}

// TestSceneSourceCarriesWhatTheItemListAlreadyKnows pins three fields that were
// available for free and being dropped.
//
// GetSceneItemList returns sourceUuid, inputKind and sceneItemBlendMode on every
// item. Without them a capture had to call ListSources separately just to learn
// an input's kind, a rename was undetectable, and blend mode was invisible.
//
// The reflective guard above cannot catch this class: it checks that every field
// SceneSource *has* was carried, not that SceneSource has every field the item
// offers. That is a real limit, and this test is the answer to it.
func TestSceneSourceCarriesWhatTheItemListAlreadyKnows(t *testing.T) {
	got := sceneSourceFromItem(typedefs.SceneItem{
		SceneItemID:        9,
		SourceName:         "OVERLAY_NowPlaying",
		SourceType:         "OBS_SOURCE_TYPE_INPUT",
		SourceUuid:         "a1b2c3d4-e5f6-7890-1234-567890abcdef",
		InputKind:          "browser_source",
		SceneItemBlendMode: "OBS_BLEND_MULTIPLY",
	})

	if got.UUID != "a1b2c3d4-e5f6-7890-1234-567890abcdef" {
		t.Errorf("UUID is %q; without it a renamed source is indistinguishable from "+
			"one deleted and another created", got.UUID)
	}
	if got.Kind != "browser_source" {
		t.Errorf("Kind is %q; it arrives with every item, so fetching it separately "+
			"is a round trip for something already in hand", got.Kind)
	}
	if got.BlendMode != "OBS_BLEND_MULTIPLY" {
		t.Errorf("BlendMode is %q", got.BlendMode)
	}
}
