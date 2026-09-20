package obs

import (
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
func TestSceneSourceFromItemCopiesEveryField(t *testing.T) {
	got := sceneSourceFromItem(typedefs.SceneItem{
		SceneItemID:      42,
		SourceName:       "StartingSoon_Room",
		SourceType:       "OBS_SOURCE_TYPE_INPUT",
		SceneItemEnabled: true,
		SceneItemLocked:  true,
		SceneItemTransform: typedefs.SceneItemTransform{
			PositionX: 120, PositionY: 80,
			Width: 1920, Height: 1080,
			ScaleX: 1.5, ScaleY: 2.5,
			Rotation: 45,
		},
	})

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
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.field, c.got, c.want)
		}
	}
}
