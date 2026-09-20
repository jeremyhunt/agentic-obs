package obstest

import (
	"testing"

	"github.com/andreykaipov/goobs/api/typedefs"
	"github.com/ironystock/agentic-obs/internal/obs"
)

// The contract is split into roles rather than one flat list. Each row declares
// the role it needs, so a row cannot quietly depend on a method it does not
// exercise, and a new capability adds a method to one role instead of a line to
// a 66-method interface. The roles grow a method at a time, as rows are added.

// SceneItemClient covers a placement: where an item sits and whether it shows.
type SceneItemClient interface {
	GetSceneItemTransform(sceneName string, sceneItemID int) (*obs.SceneItemTransform, error)
	SetSceneItemTransform(sceneName string, sceneItemID int, transform *obs.SceneItemTransform) error
	GetSceneItemEnabled(sceneName string, sceneItemID int) (bool, error)
	SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error
}

// InputClient covers the source objects themselves and their placement in
// scenes. OBS models an input as a shared object that scenes reference, so the
// two lifecycles are separate -- which these rows exist to pin down.
type InputClient interface {
	CreateInput(sceneName, sourceName, inputKind string, settings map[string]interface{}) (int, error)
	DuplicateSceneItem(sceneName string, sceneItemID int, destScene string) (int, error)
	RemoveSceneItem(sceneName string, sceneItemID int) error
	GetSceneByName(name string) (*obs.Scene, error)
	ListSources() ([]*typedefs.Input, error)
}

// InputSettingsClient covers reading and writing a source's own settings, and
// the property buttons OBS exposes on its properties dialog.
type InputSettingsClient interface {
	GetSourceSettings(sourceName string) (map[string]interface{}, error)
	SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error
	GetInputDefaultSettings(inputKind string) (map[string]interface{}, error)
}

// AudioClient covers an input's mute state.
type AudioClient interface {
	GetInputMute(inputName string) (bool, error)
	SetInputMute(inputName string, muted bool) error
	ToggleInputMute(inputName string) error
}

// FilterClient covers filters attached to a source.
type FilterClient interface {
	CreateSourceFilter(sourceName, filterName, filterKind string, settings map[string]interface{}) error
	GetSourceFilterList(sourceName string) ([]obs.FilterInfo, error)
	GetSourceFilter(sourceName, filterName string) (*obs.FilterDetails, error)
	SetSourceFilterSettings(sourceName, filterName string, settings map[string]interface{}, overlay bool) error
	SetSourceFilterEnabled(sourceName, filterName string, enabled bool) error
	RemoveSourceFilter(sourceName, filterName string) error
}

// ContractClient is everything the contract exercises.
type ContractClient interface {
	SceneItemClient
	InputClient
	InputSettingsClient
	AudioClient
	FilterClient
}

// Fixture names a scene, and one input placed in it, that the contract may
// freely mutate.
type Fixture struct {
	SceneName   string
	SourceName  string
	SourceKind  string
	SceneItemID int
}

// NewClient builds a client and a fixture for one contract row. Each row gets a
// fresh pair so rows cannot leak state into each other.
type NewClient func(t *testing.T) (ContractClient, Fixture)

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
	t.Run("unset bounds are normalised rather than rejected", func(t *testing.T) {
		client, fx := newClient(t)

		// FB-58 found that obs-websocket rejects an empty boundsType
		// (RequestFieldEmpty, 403) and any bounds dimension below 1
		// (RequestFieldOutOfRange, 402) -- the latter even under
		// OBS_BOUNDS_NONE, where the dimensions are inert. goobs marshals
		// without omitempty, so unset fields are transmitted as zero rather than
		// omitted, and both rules bite.
		//
		// Those rows asserted the rejection. FB-64 showed that was the wrong
		// place to stop: OBS reports zero bounds for any item that never had a
		// bounding box, so enforcing the wire rule meant OBS would not accept
		// its own output. The client normalises instead, and this row is the
		// statement of that -- a transform with nothing said about bounds is
		// writable.
		if err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, &obs.SceneItemTransform{
			ScaleX: 1, ScaleY: 1,
		}); err != nil {
			t.Fatalf("a transform with unset bounds was rejected: %v", err)
		}

		got, err := client.GetSceneItemTransform(fx.SceneName, fx.SceneItemID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}
		if got.BoundsType != obs.BoundsTypeNone {
			t.Errorf("bounds type read back as %q, want %q", got.BoundsType, obs.BoundsTypeNone)
		}
	})
	t.Run("a real bounds mode with an impossible size is still rejected", func(t *testing.T) {
		client, fx := newClient(t)

		// Normalisation applies only where bounds are unused. Asking for a
		// bounding box of zero width is a caller error, not an unset field, and
		// must not be silently turned into a one-pixel box.
		err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, &obs.SceneItemTransform{
			ScaleX: 1, ScaleY: 1,
			BoundsType: "OBS_BOUNDS_SCALE_INNER", BoundsWidth: 0, BoundsHeight: 600,
		})
		if err == nil {
			t.Error("expected an error for a zero-width bounding box under a real bounds mode")
		}
	})
	t.Run("a transform read back can be written back unchanged", func(t *testing.T) {
		client, fx := newClient(t)

		// Read-modify-write is what every transform tool does: get_source_transform,
		// change one field, set_source_transform. That only works if what OBS hands
		// back is something OBS will accept.
		//
		// It is not obviously true. goobs sends every field with no omitempty, and
		// obs-websocket rejects boundsWidth below 1 -- so if a freshly created item
		// reports bounds of zero, reading its transform and writing it straight
		// back is rejected for a field the caller never touched. This row is the
		// cheapest possible statement of the invariant the tools rely on. (FB-64)
		got, err := client.GetSceneItemTransform(fx.SceneName, fx.SceneItemID)
		if err != nil {
			t.Fatalf("GetSceneItemTransform: %v", err)
		}

		if err := client.SetSceneItemTransform(fx.SceneName, fx.SceneItemID, got); err != nil {
			t.Fatalf("a transform read straight from OBS was rejected on write: %v\n"+
				"read-modify-write is the pattern every transform tool uses, so this "+
				"breaks set_source_transform, set_source_crop and set_source_bounds "+
				"on any item whose transform reports this shape:\n  %+v", err, got)
		}
	})
	t.Run("setting enabled is idempotent, not a toggle", func(t *testing.T) {
		client, fx := newClient(t)

		// The distinction a toggle cannot honour: asking twice for the same state
		// must land on that state, not flip back. This is the behaviour FB-55 was
		// about -- the automation executor called a toggle when asked to set, so a
		// rule demanding visible=true flipped the item every time it fired.
		//
		// Written as "ask twice, read once" rather than "one call writes once" on
		// purpose: it is a statement about the item's state, so it holds for any
		// implementation, including one that legitimately skips a redundant write.
		for _, want := range []bool{true, false} {
			if err := client.SetSceneItemEnabled(fx.SceneName, fx.SceneItemID, want); err != nil {
				t.Fatalf("SetSceneItemEnabled(%v): %v", want, err)
			}
			if err := client.SetSceneItemEnabled(fx.SceneName, fx.SceneItemID, want); err != nil {
				t.Fatalf("SetSceneItemEnabled(%v) second call: %v", want, err)
			}

			got, err := client.GetSceneItemEnabled(fx.SceneName, fx.SceneItemID)
			if err != nil {
				t.Fatalf("GetSceneItemEnabled: %v", err)
			}
			if got != want {
				t.Errorf("set enabled=%v twice, read back %v", want, got)
			}
		}
	})
	t.Run("a created input is listed in its scene under the id it returned", func(t *testing.T) {
		client, fx := newClient(t)

		// The fixture itself was built with CreateInput, so this row checks that
		// the id it handed back addresses the item that actually landed. An id
		// that does not round-trip would make every later call target the wrong
		// item, or nothing at all.
		scene, err := client.GetSceneByName(fx.SceneName)
		if err != nil {
			t.Fatalf("GetSceneByName: %v", err)
		}

		found := findSource(scene, fx.SceneItemID)
		if found == nil {
			t.Fatalf("scene %q does not list item %d; it has %v", fx.SceneName, fx.SceneItemID, sourceIDs(scene))
		}
		if found.Name != fx.SourceName {
			t.Errorf("item %d is backed by source %q, want %q", fx.SceneItemID, found.Name, fx.SourceName)
		}
		if !found.Enabled {
			t.Error("a newly created input should be enabled")
		}
	})
	t.Run("removing one placement leaves an input another placement still holds", func(t *testing.T) {
		client, fx := newClient(t)

		// OBS models an input as a shared object and a scene item as a reference
		// to it -- OVERLAY_NowPlaying is one input placed in two scenes. Removing
		// one placement must not disturb the other, or removing a source from one
		// scene would blank it in the rest.
		//
		// apply_scene_spec's `on_unmanaged: remove` is specified to remove scene
		// items and never inputs, and this row is what makes that distinction
		// testable rather than merely intended.
		//
		// The complementary case -- removing the *last* placement, after which OBS
		// refcounting frees the input -- is deliberately not asserted here.
		// Destruction is deferred to the graphics thread, so a read straight after
		// the removal is a race, and a flaky row in a suite that gates a release is
		// worse than an unwritten one.
		second, err := client.DuplicateSceneItem(fx.SceneName, fx.SceneItemID, fx.SceneName)
		if err != nil {
			t.Fatalf("DuplicateSceneItem: %v", err)
		}

		if err := client.RemoveSceneItem(fx.SceneName, fx.SceneItemID); err != nil {
			t.Fatalf("RemoveSceneItem: %v", err)
		}

		scene, err := client.GetSceneByName(fx.SceneName)
		if err != nil {
			t.Fatalf("GetSceneByName: %v", err)
		}
		if found := findSource(scene, fx.SceneItemID); found != nil {
			t.Errorf("item %d is still listed in scene %q after removal", fx.SceneItemID, fx.SceneName)
		}
		surviving := findSource(scene, second)
		if surviving == nil {
			t.Fatalf("the duplicate placement %d vanished along with item %d; scene holds %v",
				second, fx.SceneItemID, sourceIDs(scene))
		}
		if surviving.Name != fx.SourceName {
			t.Errorf("the surviving placement is backed by %q, want %q -- a duplicate must "+
				"reference the same input, not copy it", surviving.Name, fx.SourceName)
		}

		inputs, err := client.ListSources()
		if err != nil {
			t.Fatalf("ListSources: %v", err)
		}
		if !containsInput(inputs, fx.SourceName) {
			t.Errorf("input %q disappeared while a placement still referenced it", fx.SourceName)
		}
	})
	t.Run("creating an input with a name already in use fails", func(t *testing.T) {
		client, fx := newClient(t)

		// obs-websocket answers ResourceAlreadyExists (601). Input names are the
		// global key, so this holds even when the second create targets a
		// different scene.
		//
		// This is the behaviour ensure_input has to be built on: create-or-update
		// cannot be "try create, ignore the error" unless the error is reliable.
		if _, err := client.CreateInput(fx.SceneName, fx.SourceName, fx.SourceKind, nil); err == nil {
			t.Errorf("expected an error creating a second input named %q", fx.SourceName)
		}
	})
	t.Run("setting mute is idempotent, not a toggle", func(t *testing.T) {
		client, fx := newClient(t)
		audio := newAudioInput(t, client, fx)

		// The FB-55 argument again, on audio, where getting it wrong is worse: a
		// rule that asks for muted=true and gets a flip leaves the stream silent,
		// and nobody notices until the VOD.
		for _, want := range []bool{true, false} {
			if err := client.SetInputMute(audio, want); err != nil {
				t.Fatalf("SetInputMute(%v): %v", want, err)
			}
			if err := client.SetInputMute(audio, want); err != nil {
				t.Fatalf("SetInputMute(%v) second call: %v", want, err)
			}

			got, err := client.GetInputMute(audio)
			if err != nil {
				t.Fatalf("GetInputMute: %v", err)
			}
			if got != want {
				t.Errorf("set muted=%v twice, read back %v", want, got)
			}
		}
	})
	t.Run("toggling mute flips whatever the state is", func(t *testing.T) {
		client, fx := newClient(t)
		audio := newAudioInput(t, client, fx)

		// The toggle still has to work: it is what a bare toggle_input_mute call
		// does, and its result must depend on the current state rather than
		// landing on a fixed one.
		if err := client.SetInputMute(audio, false); err != nil {
			t.Fatalf("SetInputMute: %v", err)
		}
		if err := client.ToggleInputMute(audio); err != nil {
			t.Fatalf("ToggleInputMute: %v", err)
		}

		got, err := client.GetInputMute(audio)
		if err != nil {
			t.Fatalf("GetInputMute: %v", err)
		}
		if !got {
			t.Error("toggling from unmuted left the input unmuted")
		}
	})
	t.Run("muting a source with no audio is rejected", func(t *testing.T) {
		client, fx := newClient(t)

		// Found by running this contract against a real OBS, after the first
		// draft of the rows above assumed the opposite and muted the colour
		// source fixture. obs-websocket answers InvalidResourceState (604), "The
		// specified input does not support audio."
		//
		// It matters for automation: a rule muting by source name will fail
		// outright if the name is a colour source or an image, rather than
		// quietly doing nothing, so the error has to reach the caller. (FB-68)
		if err := client.SetInputMute(fx.SourceName, true); err == nil {
			t.Errorf("muting %q (%s) succeeded; that kind has no audio", fx.SourceName, fx.SourceKind)
		}
	})
	t.Run("source settings merge when overlay is on and replace when it is off", func(t *testing.T) {
		client, fx := newClient(t)

		// The same distinction filters have, on the source itself. It is the one
		// thing every bypassing script in the workspace needs: obs_wire.py and
		// obs_starting_soon.py both call SetInputSettings directly because
		// agentic-obs has no way to write a source's settings at all. (FB-67)
		//
		// The colour source used as the fixture takes a colour and dimensions, so
		// these are real keys rather than a probe.
		if err := client.SetSourceSettings(fx.SourceName, map[string]interface{}{
			"width": 640.0, "height": 480.0,
		}, true); err != nil {
			t.Fatalf("SetSourceSettings(overlay=true): %v", err)
		}

		got, err := client.GetSourceSettings(fx.SourceName)
		if err != nil {
			t.Fatalf("GetSourceSettings: %v", err)
		}
		if got["width"] != 640.0 || got["height"] != 480.0 {
			t.Errorf("settings read back as width=%#v height=%#v, want 640/480",
				got["width"], got["height"])
		}

		// overlay=false resets to defaults before applying, so a key left out is
		// no longer whatever it was set to.
		if err := client.SetSourceSettings(fx.SourceName, map[string]interface{}{
			"width": 1280.0,
		}, false); err != nil {
			t.Fatalf("SetSourceSettings(overlay=false): %v", err)
		}

		got, err = client.GetSourceSettings(fx.SourceName)
		if err != nil {
			t.Fatalf("GetSourceSettings: %v", err)
		}
		if got["width"] != 1280.0 {
			t.Errorf("width is %#v after a replacing write, want 1280", got["width"])
		}
		if got["height"] == 480.0 {
			t.Error("height survived an overlay=false write; that write replaces, it does not merge")
		}
	})
	t.Run("default settings are reported per input kind", func(t *testing.T) {
		client, fx := newClient(t)

		// Settings-key discovery without guessing. It is also what makes a
		// meaningful diff possible: a stored setting equal to the default must not
		// read as drift, and the only way to know is to ask.
		defaults, err := client.GetInputDefaultSettings(fx.SourceKind)
		if err != nil {
			t.Fatalf("GetInputDefaultSettings(%q): %v", fx.SourceKind, err)
		}
		if len(defaults) == 0 {
			t.Errorf("%s reports no default settings at all", fx.SourceKind)
		}
	})
	t.Run("defaults are not affected by one input's settings", func(t *testing.T) {
		client, fx := newClient(t)

		// Defaults belong to the kind, not to an instance. Reporting an instance's
		// current settings as the kind's defaults would make every comparison
		// against them vacuous.
		before, err := client.GetInputDefaultSettings(fx.SourceKind)
		if err != nil {
			t.Fatalf("GetInputDefaultSettings: %v", err)
		}

		if err := client.SetSourceSettings(fx.SourceName, map[string]interface{}{
			"width": 4242.0,
		}, true); err != nil {
			t.Fatalf("SetSourceSettings: %v", err)
		}

		after, err := client.GetInputDefaultSettings(fx.SourceKind)
		if err != nil {
			t.Fatalf("GetInputDefaultSettings: %v", err)
		}
		if after["width"] != before["width"] {
			t.Errorf("default width changed from %#v to %#v after setting one input's width",
				before["width"], after["width"])
		}
	})
	t.Run("a created filter is listed with its kind, enabled, and its settings", func(t *testing.T) {
		client, fx := newClient(t)

		if err := client.CreateSourceFilter(fx.SourceName, "probe", filterKind, map[string]interface{}{
			"brightness": 0.25,
		}); err != nil {
			t.Fatalf("CreateSourceFilter: %v", err)
		}

		list, err := client.GetSourceFilterList(fx.SourceName)
		if err != nil {
			t.Fatalf("GetSourceFilterList: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("expected exactly one filter, got %d: %+v", len(list), list)
		}
		if list[0].Name != "probe" || list[0].Kind != filterKind {
			t.Errorf("listed filter is %q/%q, want %q/%q", list[0].Name, list[0].Kind, "probe", filterKind)
		}
		if !list[0].Enabled {
			t.Error("a newly created filter should be enabled")
		}

		got, err := client.GetSourceFilter(fx.SourceName, "probe")
		if err != nil {
			t.Fatalf("GetSourceFilter: %v", err)
		}
		if got.Settings["brightness"] != 0.25 {
			t.Errorf("brightness read back as %#v, want 0.25 -- settings must survive creation",
				got.Settings["brightness"])
		}
	})
	t.Run("filter settings replace rather than merge when overlay is off", func(t *testing.T) {
		client, fx := newClient(t)

		// obs_source_reset_settings clears the data before applying, so a key left
		// out of an overlay=false write goes back to its default. A client that
		// merged in both cases would make a scene spec look applied while stale
		// keys from the previous state were still in force.
		if err := client.CreateSourceFilter(fx.SourceName, "probe", filterKind, map[string]interface{}{
			"brightness": 0.25,
			"saturation": 0.5,
		}); err != nil {
			t.Fatalf("CreateSourceFilter: %v", err)
		}

		if err := client.SetSourceFilterSettings(fx.SourceName, "probe", map[string]interface{}{
			"brightness": 0.75,
		}, false); err != nil {
			t.Fatalf("SetSourceFilterSettings(overlay=false): %v", err)
		}

		got, err := client.GetSourceFilter(fx.SourceName, "probe")
		if err != nil {
			t.Fatalf("GetSourceFilter: %v", err)
		}
		if got.Settings["brightness"] != 0.75 {
			t.Errorf("brightness is %#v, want 0.75", got.Settings["brightness"])
		}
		// Whether the key vanishes or reverts to a default is OBS's business; what
		// matters is that the value it was given earlier is gone.
		if got.Settings["saturation"] == 0.5 {
			t.Error("saturation survived an overlay=false write; that write replaces, it does not merge")
		}

		// ...and the merging case still merges.
		if err := client.SetSourceFilterSettings(fx.SourceName, "probe", map[string]interface{}{
			"saturation": 0.9,
		}, true); err != nil {
			t.Fatalf("SetSourceFilterSettings(overlay=true): %v", err)
		}
		got, err = client.GetSourceFilter(fx.SourceName, "probe")
		if err != nil {
			t.Fatalf("GetSourceFilter: %v", err)
		}
		if got.Settings["saturation"] != 0.9 {
			t.Errorf("saturation is %#v, want 0.9", got.Settings["saturation"])
		}
		if got.Settings["brightness"] != 0.75 {
			t.Errorf("brightness is %#v after an overlay=true write, want it untouched at 0.75",
				got.Settings["brightness"])
		}
	})
	t.Run("disabling a filter shows up in the filter list", func(t *testing.T) {
		client, fx := newClient(t)

		if err := client.CreateSourceFilter(fx.SourceName, "probe", filterKind, nil); err != nil {
			t.Fatalf("CreateSourceFilter: %v", err)
		}
		if err := client.SetSourceFilterEnabled(fx.SourceName, "probe", false); err != nil {
			t.Fatalf("SetSourceFilterEnabled: %v", err)
		}

		// Read it back through both accessors: they must agree about one filter.
		list, err := client.GetSourceFilterList(fx.SourceName)
		if err != nil {
			t.Fatalf("GetSourceFilterList: %v", err)
		}
		if len(list) != 1 || list[0].Enabled {
			t.Errorf("filter list reports %+v, want one disabled filter", list)
		}
		got, err := client.GetSourceFilter(fx.SourceName, "probe")
		if err != nil {
			t.Fatalf("GetSourceFilter: %v", err)
		}
		if got.Enabled {
			t.Error("GetSourceFilter still reports the filter enabled")
		}
	})
	t.Run("a removed filter is gone from the source", func(t *testing.T) {
		client, fx := newClient(t)

		if err := client.CreateSourceFilter(fx.SourceName, "probe", filterKind, nil); err != nil {
			t.Fatalf("CreateSourceFilter: %v", err)
		}
		if err := client.RemoveSourceFilter(fx.SourceName, "probe"); err != nil {
			t.Fatalf("RemoveSourceFilter: %v", err)
		}

		list, err := client.GetSourceFilterList(fx.SourceName)
		if err != nil {
			t.Fatalf("GetSourceFilterList: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("source still has filters after removal: %+v", list)
		}
		if _, err := client.GetSourceFilter(fx.SourceName, "probe"); err == nil {
			t.Error("GetSourceFilter still finds a removed filter")
		}
	})
	t.Run("creating a filter with a name already on the source fails", func(t *testing.T) {
		client, fx := newClient(t)

		if err := client.CreateSourceFilter(fx.SourceName, "probe", filterKind, nil); err != nil {
			t.Fatalf("CreateSourceFilter: %v", err)
		}
		if err := client.CreateSourceFilter(fx.SourceName, "probe", filterKind, nil); err == nil {
			t.Error("expected an error creating a second filter named \"probe\" on the same source")
		}
	})
}

// audioInputKind is the kind the mute rows create.
//
// A media source rather than a capture device: it supports audio, and it needs
// no hardware, so a live run does not have to bind a real microphone on the
// operator's machine to test muting.
const audioInputKind = "ffmpeg_source"

// newAudioInput adds an audio-capable input to the fixture's scene and returns
// its name. The fixture's own source is a colour source, which OBS refuses to
// mute.
func newAudioInput(t *testing.T, client ContractClient, fx Fixture) string {
	t.Helper()

	name := fx.SceneName + "-audio"
	if _, err := client.CreateInput(fx.SceneName, name, audioInputKind, nil); err != nil {
		t.Fatalf("CreateInput(%s): %v", audioInputKind, err)
	}
	return name
}

// filterKind is the kind the filter rows attach. Colour correction is part of
// core OBS rather than a plugin, so it is present on any install the live suite
// might run against, and its settings are plain floats.
const filterKind = "color_filter_v2"

// findSource returns the scene's entry for a scene item id, or nil.

// findSource returns the scene's entry for a scene item id, or nil.
func findSource(scene *obs.Scene, sceneItemID int) *obs.SceneSource {
	for i := range scene.Sources {
		if scene.Sources[i].ID == sceneItemID {
			return &scene.Sources[i]
		}
	}
	return nil
}

// sourceIDs lists the ids a scene holds, for failure messages.
func sourceIDs(scene *obs.Scene) []int {
	ids := make([]int, 0, len(scene.Sources))
	for _, s := range scene.Sources {
		ids = append(ids, s.ID)
	}
	return ids
}

func containsInput(inputs []*typedefs.Input, name string) bool {
	for _, in := range inputs {
		if in != nil && in.InputName == name {
			return true
		}
	}
	return false
}
