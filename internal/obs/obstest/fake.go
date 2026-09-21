package obstest

import (
	"fmt"
	"sort"
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
	uuids  map[string]string // overridden source identities; see uuidOf
	nextID int
}

type scene struct {
	items []*sceneItem
	// A group is a scene with a flag, which is exactly how libobs stores one --
	// obs-websocket's own words are "groups in OBS are actually scenes, but
	// renamed and modified". Modelling it as a separate kind of thing would
	// make the fake disagree with OBS about what a group can do.
	isGroup bool
	// libobs: "a scene is a source which contains and renders other sources
	// using specific transforms and/or filtering" (reference-scenes.rst). So a
	// scene carries filters exactly as an input does, which is why
	// obs-websocket's filter requests take the generic sourceName rather than
	// inputName.
	filters []*filter
}

// newSceneItem creates a placement in the state OBS gives a fresh one.
//
// BoundsType matters: obs-websocket never reports an empty one, it reports
// OBS_BOUNDS_NONE for an item that has no bounding box. A fake that left it
// empty would let a capture record "" and then disagree with itself after an
// apply, because the client normalises "" to OBS_BOUNDS_NONE on the way out --
// which is exactly what FB-64 added it to do.
func newSceneItem(id int, source string, enabled bool) *sceneItem {
	return &sceneItem{
		id:      id,
		source:  source,
		enabled: enabled,
		transform: obs.SceneItemTransform{
			BoundsType: obs.BoundsNone,

			// Scale 1 and OBS_ALIGN_TOP|OBS_ALIGN_LEFT are what libobs gives a
			// new scene item. Starting from zeros made the fake describe an item
			// scaled to nothing and anchored at its centre, which is a state OBS
			// never produces -- and alignment in particular is the field FB-54
			// was about, where every transform write silently sent 0 and
			// re-anchored items.
			ScaleX:    1.0,
			ScaleY:    1.0,
			Alignment: obs.AlignTopLeft,
		},
	}
}

// sceneItem is one placement of an input in a scene. Its transform and enabled
// flag live together, so there is no way for two accessors to disagree.
type sceneItem struct {
	id        int
	source    string
	transform obs.SceneItemTransform
	enabled   bool
	locked    bool
	blendMode string
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

	// Copied, not kept: OBS receives settings as JSON over a socket and cannot
	// share a map with the caller. Storing the caller's map let an edit made
	// after the call change the source with no write, which is the kind of
	// difference between a double and the real thing that makes a test pass for
	// a reason unrelated to what it claims.
	f.world.inputs[sourceName] = &input{name: sourceName, kind: inputKind, settings: copySettings(settings)}

	f.world.nextID++
	it := newSceneItem(f.world.nextID, sourceName, true)
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
	// obs-websocket answers InvalidResourceType (602) here rather than listing
	// the group's contents: "The specified source is not a scene. (Is group)".
	// The two calls are exclusive in both directions.
	if sc.isGroup {
		return nil, fmt.Errorf("the specified source is not a scene. (is group): %q", name)
	}

	out := &obs.Scene{Name: name, Sources: f.renderItems(sc)}
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
	host, err := f.filterHost(sourceName)
	if err != nil {
		return nil, err
	}
	for _, flt := range *host {
		if flt.name == filterName {
			return flt, nil
		}
	}
	return nil, fmt.Errorf("filter %q not found on source %q", filterName, sourceName)
}

// filterHost returns the filter list of whichever source owns that name.
//
// Inputs and scenes are both obs_source_t, so both hold filters. Resolving
// inputs first matches OBS only in order, not in meaning: an input and a scene
// cannot share a name, so at most one lookup can hit.
func (f *Fake) filterHost(name string) (*[]*filter, error) {
	if in, ok := f.world.inputs[name]; ok {
		return &in.filters, nil
	}
	if sc, ok := f.world.scenes[name]; ok {
		return &sc.filters, nil
	}
	return nil, fmt.Errorf("source %q not found", name)
}

// sourceExists reports whether anything placeable answers to that name.
//
// A nested scene is the normal way to build a container in OBS -- obs-websocket
// recommends nested scenes over groups outright -- so a fake that accepts only
// inputs here cannot model a real collection. Every container in the collection
// this project drives is a nested scene.
func (f *Fake) sourceExists(name string) bool {
	if _, ok := f.world.inputs[name]; ok {
		return true
	}
	_, ok := f.world.scenes[name]
	return ok
}

// CreateGroup adds an empty group. Like CreateScene it is not part of the
// contract interface: obs-websocket has no CreateGroup request at all, so this
// exists only so the fake can offer the fixture a group the live side has to
// find rather than make.
func (f *Fake) CreateGroup(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.world.scenes[name]; exists {
		return fmt.Errorf("resource already exists: %q", name)
	}
	f.world.scenes[name] = &scene{isGroup: true}
	return nil
}

// GetSceneList returns the scenes, and the current one.
//
// Groups are excluded: obs-websocket keeps them in GetGroupList and a group is
// absent from GetSceneList, which is how a caller tells which of the two a
// container is without asking about any placement.
func (f *Fake) GetSceneList() ([]string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []string{}
	for name, sc := range f.world.scenes {
		if !sc.isGroup {
			out = append(out, name)
		}
	}
	sort.Strings(out)

	current := ""
	if len(out) > 0 {
		current = out[0]
	}
	return out, current, nil
}

func (f *Fake) GetGroupList() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []string{}
	for name, sc := range f.world.scenes {
		if sc.isGroup {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *Fake) GetGroupSceneItemList(groupName string) ([]obs.SceneSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sc, ok := f.world.scenes[groupName]
	if !ok {
		return nil, fmt.Errorf("group %q not found", groupName)
	}
	if !sc.isGroup {
		return nil, fmt.Errorf("the specified source is not a group. (is scene): %q", groupName)
	}
	return f.renderItems(sc), nil
}

// renderItems turns a container's placements into the shape both
// GetSceneByName and GetGroupSceneItemList return.
//
// Type carries OBS's source-type vocabulary, not the input kind: the real
// client reads it from sceneItem.sourceType, so a fake that put the kind there
// would quietly disagree with OBS about what obs://scene/{name} publishes.
func (f *Fake) renderItems(sc *scene) []obs.SceneSource {
	out := make([]obs.SceneSource, 0, len(sc.items))
	for _, it := range sc.items {
		sourceType, isGroup := f.sourceTypeOf(it.source)
		blend := it.blendMode
		if blend == "" {
			blend = "OBS_BLEND_NORMAL" // what OBS reports for an untouched item
		}
		out = append(out, obs.SceneSource{
			ID:        it.id,
			Name:      it.source,
			Type:      sourceType,
			IsGroup:   isGroup,
			UUID:      f.uuidOf(it.source),
			Kind:      f.kindOf(it.source),
			BlendMode: blend,
			Enabled:   it.enabled,
			Visible:   it.enabled,
			Locked:    it.locked,
			X:         it.transform.PositionX,
			Y:         it.transform.PositionY,
			Width:     it.transform.Width,
			Height:    it.transform.Height,
			ScaleX:    it.transform.ScaleX,
			ScaleY:    it.transform.ScaleY,
			Rotation:  it.transform.Rotation,
		})
	}
	return out
}

// kindOf reports an input's kind. Scenes and groups have none, which is the
// distinction the capture branches on.
func (f *Fake) kindOf(name string) string {
	if in, ok := f.world.inputs[name]; ok {
		return in.kind
	}
	return ""
}

// uuidOf hands out a stable identity per source.
//
// OBS assigns a real uuid; the fake derives one from the name, which is enough
// for the thing uuids are for here -- telling a rename from a delete-and-create
// -- as long as it survives a rename. SetSourceUUID is how a test simulates one.
func (f *Fake) uuidOf(name string) string {
	if id, ok := f.world.uuids[name]; ok {
		return id
	}
	return "fake-uuid-" + name
}

// SourceUUID reports a source's identity, so a test can rename it and keep it.
func (f *Fake) SourceUUID(sourceName string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.uuidOf(sourceName)
}

// SetSourceUUID pins a source's identity, so a test can rename a source and
// keep its uuid -- which is what a real rename does.
func (f *Fake) SetSourceUUID(sourceName, uuid string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.world.uuids == nil {
		f.world.uuids = map[string]string{}
	}
	f.world.uuids[sourceName] = uuid
}

// SetSceneItemBlendMode sets how a placement composites.
func (f *Fake) SetSceneItemBlendMode(sceneName string, sceneItemID int, blendMode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return err
	}
	it.blendMode = blendMode
	return nil
}

// sourceTypeOf reports how OBS types a placed source. A group and a nested
// scene are both OBS_SOURCE_TYPE_SCENE -- only isGroup separates them.
func (f *Fake) sourceTypeOf(name string) (sourceType string, isGroup bool) {
	if sc, ok := f.world.scenes[name]; ok {
		return "OBS_SOURCE_TYPE_SCENE", sc.isGroup
	}
	if _, ok := f.world.inputs[name]; ok {
		return "OBS_SOURCE_TYPE_INPUT", false
	}
	return "", false
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

	host, err := f.filterHost(sourceName)
	if err != nil {
		return err
	}
	for _, flt := range *host {
		if flt.name == filterName {
			return fmt.Errorf("resource already exists: source %q already has a filter named %q", sourceName, filterName)
		}
	}

	*host = append(*host, &filter{
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

	host, err := f.filterHost(sourceName)
	if err != nil {
		return nil, err
	}

	out := make([]obs.FilterInfo, 0, len(*host))
	for i, flt := range *host {
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

	host, err := f.filterHost(sourceName)
	if err != nil {
		return nil, err
	}
	index := 0
	for i, other := range *host {
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

	host, err := f.filterHost(sourceName)
	if err != nil {
		return err
	}
	for i, flt := range *host {
		if flt.name == filterName {
			*host = append((*host)[:i], (*host)[i+1:]...)
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
	"text_gdiplus_v3": {"text": "", "font": map[string]interface{}{"face": "Arial", "size": 36.0}, "color": 16777215.0},
	"browser_source":  {"url": "https://obsproject.com/browser-source", "width": 800.0, "height": 600.0, "shutdown": false, "restart_when_active": false, "css": ""},
	"image_source":    {"file": "", "unload": false},
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

		// take_screenshot validates the requested format against this list
		// rather than a hard-coded one, because a real build reports seventeen
		// formats and the guess that started as png/jpg/bmp refused a webp OBS
		// had just accepted (FB-73). A fake that reported none would make that
		// validation untestable.
		SupportedImageFormats: []string{"bmp", "jpeg", "jpg", "png", "webp"},
	}, nil
}

// CreateSceneItem places an existing input into a scene.
//
// A reference, not a copy: the input map is untouched, so both placements point
// at one object and a settings write through either is visible from both.
// (FB-71)
func (f *Fake) CreateSceneItem(sceneName, sourceName string, enabled bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sc, ok := f.world.scenes[sceneName]
	if !ok {
		return 0, fmt.Errorf("scene %q not found", sceneName)
	}
	if !f.sourceExists(sourceName) {
		return 0, fmt.Errorf("source %q not found", sourceName)
	}
	if sourceName == sceneName {
		// OBS refuses to place a scene inside itself: the render would recurse.
		return 0, fmt.Errorf("cannot place scene %q inside itself", sceneName)
	}

	f.world.nextID++
	it := newSceneItem(f.world.nextID, sourceName, enabled)
	sc.items = append(sc.items, it)

	return it.id, nil
}

// GetSceneItemLocked reports whether a placement is locked.
//
// The setter existed without it, which was survivable only while nothing asked.
// MockOBSClient kept the answer in a map of its own, so once the write went to
// the world the two halves of one fact lived in two places -- the exact shape
// of bug one world exists to make impossible.
func (f *Fake) GetSceneItemLocked(sceneName string, sceneItemID int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return false, err
	}
	return it.locked, nil
}

// SetSceneItemLocked locks or unlocks a placement.
//
// The fake stores locked but had no setter, which only mattered once something
// tried to reconcile a scene rather than read one.
func (f *Fake) SetSceneItemLocked(sceneName string, sceneItemID int, locked bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	it, err := f.findItem(sceneName, sceneItemID)
	if err != nil {
		return err
	}
	it.locked = locked
	return nil
}

// SetSceneItemIndex moves a placement to a position in its container.
//
// Position here means the index in the list GetSceneByName returns, which is
// what the capture records as Order -- stated that way rather than in terms of
// which end OBS calls the top, because the fake only has to be self-consistent
// with its own listing.
func (f *Fake) SetSceneItemIndex(sceneName string, sceneItemID int, index int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	sc, ok := f.world.scenes[sceneName]
	if !ok {
		return fmt.Errorf("scene %q not found", sceneName)
	}
	if index < 0 || index >= len(sc.items) {
		return fmt.Errorf("the field value of `sceneItemIndex` is out of range: %d", index)
	}

	at := -1
	for i, it := range sc.items {
		if it.id == sceneItemID {
			at = i
			break
		}
	}
	if at < 0 {
		return fmt.Errorf("scene item %d not found in scene %q", sceneItemID, sceneName)
	}

	moved := sc.items[at]
	sc.items = append(sc.items[:at], sc.items[at+1:]...)
	rest := append([]*sceneItem(nil), sc.items[index:]...)
	sc.items = append(append(sc.items[:index], moved), rest...)
	return nil
}

// RemoveScene deletes a scene and its placements. The inputs survive, as they do
// in OBS while anything else references them.
func (f *Fake) RemoveScene(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.world.scenes[name]; !ok {
		return fmt.Errorf("scene %q not found", name)
	}
	delete(f.world.scenes, name)
	return nil
}
