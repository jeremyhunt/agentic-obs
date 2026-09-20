package obstest

import (
	"fmt"
	"sync"

	"github.com/andreykaipov/goobs/api/typedefs"
	"github.com/ironystock/agentic-obs/internal/obs"
)

// Fake is an in-memory stand-in for the OBS client.
//
// State lives in one world rather than in a map per accessor. The existing
// MockOBSClient keeps scene items, their transforms and their lock states in
// three unrelated maps, which lets two of its methods disagree about the same
// item -- a shape of bug the real client cannot have.
//
// The world is shaped the way OBS shapes it: an input is a global object keyed
// by name, and a scene item is a reference to one. Modelling those as one thing
// would make the fake agree with tests a real OBS would fail. (FB-60)
type Fake struct {
	mu    sync.Mutex
	world world
}

type world struct {
	scenes map[string]*scene
	inputs map[string]*input
	nextID int
}

type scene struct {
	items []*sceneItem
}

// sceneItem is one placement of an input in a scene. Its transform and enabled
// flag live together, so there is no way for two accessors to disagree.
type sceneItem struct {
	id        int
	source    string
	transform obs.SceneItemTransform
	enabled   bool
	locked    bool
}

// input is a source object. Filters hang off the input rather than off a
// placement, because OBS attaches them to the source: a filter added while the
// source sits in one scene is visible from every other scene showing it.
type input struct {
	name     string
	kind     string
	settings map[string]interface{}
	muted    bool
	filters  []*filter
}

type filter struct {
	name     string
	kind     string
	enabled  bool
	settings map[string]interface{}
}

// NewFake returns a Fake with an empty world.
func NewFake() *Fake {
	return &Fake{world: world{
		scenes: map[string]*scene{},
		inputs: map[string]*input{},
	}}
}

// findItem locates a scene item, or explains which part was missing. OBS answers
// ResourceNotFound for both cases; the fake says which, so a failing test names
// the actual problem.
func (f *Fake) findItem(sceneName string, sceneItemID int) (*sceneItem, error) {
	sc, ok := f.world.scenes[sceneName]
	if !ok {
		return nil, fmt.Errorf("scene %q not found", sceneName)
	}
	for _, it := range sc.items {
		if it.id == sceneItemID {
			return it, nil
		}
	}
	return nil, fmt.Errorf("scene item %d not found in scene %q", sceneItemID, sceneName)
}

// CreateScene adds an empty scene. It is not part of the contract interface --
// the contract fixture uses it to reach the same starting state the live suite
// builds in a real OBS.
func (f *Fake) CreateScene(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.world.scenes[name]; exists {
		return fmt.Errorf("scene %q already exists", name)
	}
	f.world.scenes[name] = &scene{}
	return nil
}

// CreateInput creates a source and places it in a scene.
//
// obs-websocket requires the scene (CreateInput calls AcquireScene and rejects a
// request without one) and rejects a name already in use with
// ResourceAlreadyExists, because input names are the global key.
func (f *Fake) CreateInput(sceneName, sourceName, inputKind string, settings map[string]interface{}) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sc, ok := f.world.scenes[sceneName]
	if !ok {
		return 0, fmt.Errorf("scene %q not found", sceneName)
	}
	if _, exists := f.world.inputs[sourceName]; exists {
		return 0, fmt.Errorf("resource already exists: an input named %q already exists", sourceName)
	}

	f.world.inputs[sourceName] = &input{name: sourceName, kind: inputKind, settings: settings}

	f.world.nextID++
	it := &sceneItem{id: f.world.nextID, source: sourceName, enabled: true}
	sc.items = append(sc.items, it)

	return it.id, nil
}

// RemoveSceneItem removes a placement. The input survives, because other scenes
// may still reference it.
func (f *Fake) RemoveSceneItem(sceneName string, sceneItemID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	sc, ok := f.world.scenes[sceneName]
	if !ok {
		return fmt.Errorf("scene %q not found", sceneName)
	}
	for i, it := range sc.items {
		if it.id == sceneItemID {
			sc.items = append(sc.items[:i], sc.items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("scene item %d not found in scene %q", sceneItemID, sceneName)
}

// GetSceneByName lists a scene's items in order.
func (f *Fake) GetSceneByName(name string) (*obs.Scene, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sc, ok := f.world.scenes[name]
	if !ok {
		return nil, fmt.Errorf("scene %q not found", name)
	}

	out := &obs.Scene{Name: name, Sources: make([]obs.SceneSource, 0, len(sc.items))}
	for _, it := range sc.items {
		kind := ""
		if in, ok := f.world.inputs[it.source]; ok {
			kind = in.kind
		}
		out.Sources = append(out.Sources, obs.SceneSource{
			ID:       it.id,
			Name:     it.source,
			Type:     kind,
			Enabled:  it.enabled,
			Visible:  it.enabled,
			Locked:   it.locked,
			X:        it.transform.PositionX,
			Y:        it.transform.PositionY,
			Width:    it.transform.Width,
			Height:   it.transform.Height,
			ScaleX:   it.transform.ScaleX,
			ScaleY:   it.transform.ScaleY,
			Rotation: it.transform.Rotation,
		})
	}
	return out, nil
}

// ListSources returns every input, whatever scene it sits in.
func (f *Fake) ListSources() ([]*typedefs.Input, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*typedefs.Input, 0, len(f.world.inputs))
	for _, in := range f.world.inputs {
		out = append(out, &typedefs.Input{
			InputName:            in.name,
			InputKind:            in.kind,
			UnversionedInputKind: in.kind,
		})
	}
	return out, nil
}

func (f *Fake) GetSceneItemTransform(sceneName string, sceneItemID int) (*obs.SceneItemTransform, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return nil, err
	}
	// Return a copy: a caller mutating what it read must not alter stored state.
	t := it.transform
	return &t, nil
}

func (f *Fake) SetSceneItemTransform(sceneName string, sceneItemID int, transform *obs.SceneItemTransform) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// obs-websocket rejects an empty boundsType (RequestFieldEmpty, 403) and any
	// bounds dimension below 1 (RequestFieldOutOfRange, 402), even under
	// OBS_BOUNDS_NONE where the dimensions are inert. The client normalises
	// those away, because OBS reports zero bounds for an item that never had a
	// bounding box and would otherwise refuse to accept its own output. (FB-64)
	//
	// The fake stands in for the client, not for the wire, so it applies exactly
	// the same normalisation -- sharing the function rather than restating the
	// rule, so the two cannot drift.
	boundsType, boundsWidth, boundsHeight := obs.NormaliseBounds(
		transform.BoundsType, transform.BoundsWidth, transform.BoundsHeight)

	// A real bounds mode with an impossible size is still an error: the caller
	// asked for something OBS cannot do, rather than leaving a field unset.
	if boundsWidth < 1 {
		return fmt.Errorf("the field value of `boundsWidth` is below the minimum of `1.000000`")
	}
	if boundsHeight < 1 {
		return fmt.Errorf("the field value of `boundsHeight` is below the minimum of `1.000000`")
	}

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return err
	}

	prev := it.transform
	stored := *transform
	stored.BoundsType, stored.BoundsWidth, stored.BoundsHeight = boundsType, boundsWidth, boundsHeight

	// OBS derives these from the source and ignores them on a write, so keep
	// whatever the item already had rather than accepting the values passed in.
	stored.Width, stored.Height = prev.Width, prev.Height
	stored.SourceWidth, stored.SourceHeight = prev.SourceWidth, prev.SourceHeight

	it.transform = stored
	return nil
}

func (f *Fake) GetSceneItemEnabled(sceneName string, sceneItemID int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return false, err
	}
	return it.enabled, nil
}

func (f *Fake) SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return err
	}
	it.enabled = enabled
	return nil
}

// findFilter locates a filter on a source by name.
func (f *Fake) findFilter(sourceName, filterName string) (*filter, error) {
	in, ok := f.world.inputs[sourceName]
	if !ok {
		return nil, fmt.Errorf("source %q not found", sourceName)
	}
	for _, flt := range in.filters {
		if flt.name == filterName {
			return flt, nil
		}
	}
	return nil, fmt.Errorf("filter %q not found on source %q", filterName, sourceName)
}

// copySettings defends the stored map from a caller that keeps mutating the one
// it handed over. The real client serialises to JSON, so it gets this for free.
func copySettings(settings map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(settings))
	for k, v := range settings {
		out[k] = v
	}
	return out
}

func (f *Fake) CreateSourceFilter(sourceName, filterName, filterKind string, settings map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.world.inputs[sourceName]
	if !ok {
		return fmt.Errorf("source %q not found", sourceName)
	}
	for _, flt := range in.filters {
		if flt.name == filterName {
			return fmt.Errorf("resource already exists: source %q already has a filter named %q", sourceName, filterName)
		}
	}

	in.filters = append(in.filters, &filter{
		name:     filterName,
		kind:     filterKind,
		enabled:  true, // OBS creates filters enabled
		settings: copySettings(settings),
	})
	return nil
}

func (f *Fake) GetSourceFilterList(sourceName string) ([]obs.FilterInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.world.inputs[sourceName]
	if !ok {
		return nil, fmt.Errorf("source %q not found", sourceName)
	}

	out := make([]obs.FilterInfo, 0, len(in.filters))
	for i, flt := range in.filters {
		out = append(out, obs.FilterInfo{Name: flt.name, Kind: flt.kind, Index: i, Enabled: flt.enabled})
	}
	return out, nil
}

func (f *Fake) GetSourceFilter(sourceName, filterName string) (*obs.FilterDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	flt, err := f.findFilter(sourceName, filterName)
	if err != nil {
		return nil, err
	}

	index := 0
	for i, other := range f.world.inputs[sourceName].filters {
		if other == flt {
			index = i
			break
		}
	}

	return &obs.FilterDetails{
		Name:     flt.name,
		Kind:     flt.kind,
		Index:    index,
		Enabled:  flt.enabled,
		Settings: copySettings(flt.settings),
	}, nil
}

// SetSourceFilterSettings merges when overlay is true and replaces when it is
// false, mirroring obs_source_update against obs_source_reset_settings -- the
// latter clears the data object first, so keys left out revert to default.
func (f *Fake) SetSourceFilterSettings(sourceName, filterName string, settings map[string]interface{}, overlay bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	flt, err := f.findFilter(sourceName, filterName)
	if err != nil {
		return err
	}

	if !overlay {
		flt.settings = copySettings(settings)
		return nil
	}
	if flt.settings == nil {
		flt.settings = map[string]interface{}{}
	}
	for k, v := range settings {
		flt.settings[k] = v
	}
	return nil
}

func (f *Fake) SetSourceFilterEnabled(sourceName, filterName string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	flt, err := f.findFilter(sourceName, filterName)
	if err != nil {
		return err
	}
	flt.enabled = enabled
	return nil
}

func (f *Fake) RemoveSourceFilter(sourceName, filterName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.world.inputs[sourceName]
	if !ok {
		return fmt.Errorf("source %q not found", sourceName)
	}
	for i, flt := range in.filters {
		if flt.name == filterName {
			in.filters = append(in.filters[:i], in.filters[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("filter %q not found on source %q", filterName, sourceName)
}

// DuplicateSceneItem adds a second placement of the same input.
//
// It duplicates the item, not the source: obs-websocket hands
// obs_sceneitem_get_source straight to obs_scene_add, so both placements point
// at one input and a settings change on either is a change to both.
func (f *Fake) DuplicateSceneItem(sceneName string, sceneItemID int, destScene string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	src, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return 0, err
	}
	dest, ok := f.world.scenes[destScene]
	if !ok {
		return 0, fmt.Errorf("scene %q not found", destScene)
	}

	f.world.nextID++
	copied := &sceneItem{
		id:        f.world.nextID,
		source:    src.source,
		transform: src.transform,
		enabled:   src.enabled,
		locked:    src.locked,
	}
	dest.items = append(dest.items, copied)

	return copied.id, nil
}

// kindDefaults are the default settings the fake reports per input kind.
//
// Real OBS gets these from the source type's own defaults callback, so the fake
// can only approximate. It carries the kinds the contract uses; an unknown kind
// reports an error rather than an empty map, because "this kind has no settings"
// and "I have never heard of this kind" are different answers and OBS
// distinguishes them.
var kindDefaults = map[string]map[string]interface{}{
	"color_source_v3": {"color": 4278190080.0, "width": 0.0, "height": 0.0},
	"ffmpeg_source":   {"local_file": "", "looping": false, "restart_on_activate": true},
}

func (f *Fake) GetSourceSettings(sourceName string) (map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.world.inputs[sourceName]
	if !ok {
		return nil, fmt.Errorf("source %q not found", sourceName)
	}
	return copySettings(in.settings), nil
}

// SetSourceSettings merges when overlay is true and replaces when it is false.
//
// obs_source_reset_settings clears the data object before applying, so a key
// left out of a replacing write goes back to its default rather than keeping the
// value it had -- which is why the defaults are merged in here rather than the
// map simply being emptied.
func (f *Fake) SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, ok := f.world.inputs[sourceName]
	if !ok {
		return fmt.Errorf("source %q not found", sourceName)
	}

	if !overlay {
		in.settings = copySettings(kindDefaults[in.kind])
	}
	if in.settings == nil {
		in.settings = map[string]interface{}{}
	}
	for k, v := range settings {
		in.settings[k] = v
	}
	return nil
}

func (f *Fake) GetInputDefaultSettings(inputKind string) (map[string]interface{}, error) {
	defaults, ok := kindDefaults[inputKind]
	if !ok {
		return nil, fmt.Errorf("no such input kind %q", inputKind)
	}
	// A copy, so a caller cannot edit the defaults every other caller reads.
	return copySettings(defaults), nil
}

// audioCapableKinds are the input kinds that have an audio track.
//
// obs-websocket answers InvalidResourceState (604) "The specified input does not
// support audio" for anything else, and the fake has to as well: accepting a
// mute on a colour source is precisely the kind of over-permissiveness that lets
// a test certify behaviour a real OBS rejects. Found by running the contract
// live after the first draft assumed any input could be muted. (FB-68)
var audioCapableKinds = map[string]bool{
	"ffmpeg_source":                 true,
	"vlc_source":                    true,
	"wasapi_input_capture":          true,
	"wasapi_output_capture":         true,
	"wasapi_process_output_capture": true,
	"coreaudio_input_capture":       true,
	"coreaudio_output_capture":      true,
	"pulse_input_capture":           true,
	"pulse_output_capture":          true,
	"browser_source":                true,
	"game_capture":                  true,
}

// audioInput looks up an input and checks it has audio at all.
func (f *Fake) audioInput(inputName string) (*input, error) {
	in, ok := f.world.inputs[inputName]
	if !ok {
		return nil, fmt.Errorf("input %q not found", inputName)
	}
	if !audioCapableKinds[in.kind] {
		return nil, fmt.Errorf("the specified input does not support audio: %q is a %s", inputName, in.kind)
	}
	return in, nil
}

func (f *Fake) GetInputMute(inputName string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, err := f.audioInput(inputName)
	if err != nil {
		return false, err
	}
	return in.muted, nil
}

func (f *Fake) SetInputMute(inputName string, muted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, err := f.audioInput(inputName)
	if err != nil {
		return err
	}
	in.muted = muted
	return nil
}

func (f *Fake) ToggleInputMute(inputName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	in, err := f.audioInput(inputName)
	if err != nil {
		return err
	}
	in.muted = !in.muted
	return nil
}

// canvas is the fake's video configuration.
//
// Not 1920x1080, and downscaled, on purpose: a fixture that matches the common
// assumption lets code pass while reading hardcoded numbers instead of reported
// ones. The fractional frame rate catches anything that reads the numerator and
// calls it a rate. (FB-69)
var canvas = obs.VideoSettings{
	BaseWidth:      2560,
	BaseHeight:     1440,
	OutputWidth:    1920,
	OutputHeight:   1080,
	FPSNumerator:   60000,
	FPSDenominator: 1001,
}

func (f *Fake) GetVideoSettings() (*obs.VideoSettings, error) {
	v := canvas
	return &v, nil
}

// GetOBSStatus reports enough of a status to carry the canvas, which is the
// only part of it the contract covers.
func (f *Fake) GetOBSStatus() (*obs.OBSStatus, error) {
	v := canvas
	return &obs.OBSStatus{
		Version:          "32.2.2",
		WebSocketVersion: "5.7.4",
		Platform:         "windows",
		Video:            &v,
	}, nil
}
