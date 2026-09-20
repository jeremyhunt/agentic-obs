package obstest

import (
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// SceneItemClient is the slice of the OBS client the scene-item rows exercise.
// It is deliberately narrow: the contract grows a method at a time, as rows are
// added, rather than mirroring the whole 66-method client up front.
type SceneItemClient interface {
	GetSceneItemTransform(sceneName string, sceneItemID int) (*obs.SceneItemTransform, error)
	SetSceneItemTransform(sceneName string, sceneItemID int, transform *obs.SceneItemTransform) error
}

// Fixture names a scene item the contract may freely mutate.
type Fixture struct {
	SceneName   string
	SceneItemID int
}

// NewClient builds a client and a fixture for one contract row. Each row gets a
// fresh pair so rows cannot leak state into each other.
type NewClient func(t *testing.T) (SceneItemClient, Fixture)

// RunContract asserts the behaviour any OBS client must exhibit, whether it is
// the fake or a real obs-websocket connection.
//
// Rows are phrased as observable behaviour rather than as field lists, so a row
// catches a whole class of loss without having to know which fields exist. The
// transform round-trip below would have caught FB-54 -- alignment being dropped
// on write -- without mentioning alignment at all.
func RunContract(t *testing.T, newClient NewClient) {
	t.Helper()

	t.Run("transform round-trips every settable field", func(t *testing.T) {
		client, fx := newClient(t)

		want := &obs.SceneItemTransform{
			PositionX: 120, PositionY: 80,
			ScaleX: 1.25, ScaleY: 0.75,
			Rotation:        45,
			Alignment:       5,
			BoundsType:      "OBS_BOUNDS_SCALE_INNER",
			BoundsAlignment: 4,
			BoundsWidth:     800, BoundsHeight: 600,
			CropToBounds: true,
			CropTop:      1, CropBottom: 2, CropLeft: 3, CropRight: 4,
		}

		if err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, want); err != nil {
			t.Fatalf("SetSceneItemTransform: %v", err)
		}

		got, err := client.GetSceneItemTransform(fx.SceneName, fx.SceneItemID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}

		assertSettableFieldsEqual(t, want, got)
	})
	t.Run("derived fields are not settable", func(t *testing.T) {
		client, fx := newClient(t)

		// OBS computes Width, Height, SourceWidth and SourceHeight from the
		// source's own dimensions and ignores them on a write. A client that
		// stores whatever it was handed would be more agreeable than OBS, and any
		// test written against it would pass while the real thing behaved
		// differently.
		base := &obs.SceneItemTransform{ScaleX: 1, ScaleY: 1, BoundsType: "OBS_BOUNDS_NONE", BoundsWidth: 1, BoundsHeight: 1, Width: 999, Height: 999, SourceWidth: 999, SourceHeight: 999}
		if err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, base); err != nil {
			t.Fatalf("SetSceneItemTransform: %v", err)
		}
		first, err := client.GetSceneItemTransform(fx.SceneName, fx.SceneItemID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}

		other := &obs.SceneItemTransform{ScaleX: 1, ScaleY: 1, BoundsType: "OBS_BOUNDS_NONE", BoundsWidth: 1, BoundsHeight: 1, Width: 111, Height: 111, SourceWidth: 111, SourceHeight: 111}
		if err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, other); err != nil {
			t.Fatalf("SetSceneItemTransform: %v", err)
		}
		second, err := client.GetSceneItemTransform(fx.SceneName, fx.SceneItemID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}

		// Whatever the derived values are, writing different ones must not move
		// them.
		if first.Width != second.Width || first.Height != second.Height ||
			first.SourceWidth != second.SourceWidth || first.SourceHeight != second.SourceHeight {
			t.Errorf("derived fields changed after writing different values: "+
				"first {W:%v H:%v SW:%v SH:%v} second {W:%v H:%v SW:%v SH:%v}",
				first.Width, first.Height, first.SourceWidth, first.SourceHeight,
				second.Width, second.Height, second.SourceWidth, second.SourceHeight)
		}
	})
	t.Run("rejects an empty bounds type", func(t *testing.T) {
		client, fx := newClient(t)

		// Found by running this contract against a real OBS: obs-websocket
		// answers RequestFieldEmpty (403) "The field value of `boundsType` must
		// not be empty", while the fake accepted it silently. goobs marshals the
		// transform with no omitempty, so an unset BoundsType goes on the wire as
		// "" rather than being omitted.
		//
		// Production paths read-modify-write and so always carry a real bounds
		// type, but anything constructing a transform from scratch -- applying a
		// stored scene spec, for one -- will hit this. (FB-58)
		err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, &obs.SceneItemTransform{
			ScaleX: 1, ScaleY: 1, BoundsWidth: 1, BoundsHeight: 1,
		})
		if err == nil {
			t.Error("expected an error for an empty bounds type; OBS rejects it, so the fake must too")
		}
	})
	t.Run("rejects bounds dimensions below one", func(t *testing.T) {
		client, fx := newClient(t)

		// The second divergence this contract found against a real OBS:
		// RequestFieldOutOfRange (402) "The field value of `boundsWidth` is below
		// the minimum of `1.000000`". It applies even with OBS_BOUNDS_NONE, where
		// the dimensions are not used for anything -- because goobs sends every
		// field, so zero is transmitted rather than omitted.
		//
		// Together with the empty-boundsType rule this means a transform built
		// from scratch needs a bounds type and non-zero bounds dimensions, or the
		// write fails with a message that names a field the caller never set.
		// apply_scene_spec will construct transforms from scratch. (FB-58)
		err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, &obs.SceneItemTransform{
			ScaleX: 1, ScaleY: 1, BoundsType: "OBS_BOUNDS_NONE",
		})
		if err == nil {
			t.Error("expected an error for zero bounds dimensions; OBS requires at least 1")
		}
	})
}
