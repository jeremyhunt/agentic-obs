// Package scenespec turns a scene into a document, and back.
//
// A scene spec separates what a source *is* from where it is *placed*, because
// OBS does. An input is a global object that scenes reference; a scene item is
// one reference to it. The collection this was written for places
// jurmiey_avatar twice in a single scene and shares eleven sources across up to
// five scenes, so a flat list of placements each carrying its own settings
// cannot round-trip: applying it would either write one source's settings
// several times, fighting itself, or drop a placement.
//
// So a spec has two lists. Sources are captured once each, however often they
// appear. Items are placements, and reference a source by name.
//
// The package is pure. It reads through a narrow interface and returns a
// document; it performs no I/O of its own and holds no OBS state.
package scenespec

import (
	"github.com/ironystock/agentic-obs/internal/layout"
	"github.com/ironystock/agentic-obs/internal/obs"
)

// SpecVersion is the schema version written into every captured document.
//
// It exists so a stored spec can be migrated later. internal/storage's
// migrate() forbids ALTER TABLE, so a spec is stored as a versioned JSON
// document rather than as columns, and this is what makes that safe.
const SpecVersion = 1

// Source types. A source is one of exactly three things, and which one decides
// what can be captured and what it takes to recreate it.
const (
	// SourceInput is an ordinary source: it has a kind and settings, and
	// CreateInput can make one.
	SourceInput = "input"

	// SourceScene is a scene placed inside another scene. It has no kind and no
	// settings -- obs-websocket answers InvalidResourceType (602) to
	// GetInputSettings for one -- so a spec references it and the scene is
	// captured in its own right.
	SourceScene = "scene"

	// SourceGroup is a group. It reports the same sourceType as a nested scene
	// and is distinguished only by isGroup, its contents come from
	// GetGroupSceneItemList rather than GetSceneItemList, and **it cannot be
	// created over obs-websocket at all** -- there is CreateScene and no
	// CreateGroup. So a spec can capture a group and an apply can never
	// materialise a missing one.
	SourceGroup = "group"
)

// Spec is a captured scene.
type Spec struct {
	Version int    `json:"version"`
	Scene   string `json:"scene"`

	// Sources holds each distinct source the scene references, once.
	Sources []SourceSpec `json:"sources"`

	// Items holds the placements, in render order.
	Items []ItemSpec `json:"items"`

	// Omitted names what this capture deliberately skipped, and is empty for a
	// full one.
	//
	// A lighter document is genuinely useful for reading a layout -- one
	// browser source's settings can dwarf every placement in the scene -- but a
	// spec captured without settings cannot be applied. The recurring defect in
	// this project is output that looks complete while omitting half its
	// subject, so a partial document says so rather than being indistinguishable
	// from a scene that happens to have no filters.
	Omitted []string `json:"omitted,omitempty"`
}

// Options controls how much of a scene a capture reads.
//
// The zero value omits everything optional, so callers state what they want.
// FullCapture is the value to use when the document is going to be applied.
type Options struct {
	IncludeSettings bool
	IncludeFilters  bool
}

// FullCapture reads everything. This is what Capture uses.
func FullCapture() Options {
	return Options{IncludeSettings: true, IncludeFilters: true}
}

// SourceSpec is what a source is, independent of where it sits.
type SourceSpec struct {
	Name string `json:"name"`

	// UUID is the source's stable identity, captured so a diff can tell a
	// rename from a delete-and-create. It is never used to *find* a source when
	// applying -- a spec applied to a different machine will match on name.
	UUID string `json:"uuid,omitempty"`

	// Type is SourceInput, SourceScene or SourceGroup.
	Type string `json:"type"`

	// Kind is the OBS input kind ("browser_source", "image_source", ...).
	// Inputs only: scenes and groups have none.
	Kind string `json:"kind,omitempty"`

	// Settings is the source's own configuration. Inputs only.
	Settings map[string]interface{} `json:"settings,omitempty"`

	// PreserveURLParams names query parameters of Settings["url"] that belong
	// to somebody else and must survive a write.
	//
	// A browser source's URL can have two writers. The "Starting Soon" overlay
	// here is the case: this spec owns the page and its geometry, and the
	// streaming dashboard owns "text" and "until". Settings are applied with
	// overlay=false so the live source matches the document, which is right for
	// every key the document owns and is exactly what drops a key it does not.
	// Naming those keys makes a diff ignore them and an apply carry them over.
	//
	// A capture never fills this in: only the author knows which parameters are
	// foreign, and guessing would be worse than asking.
	PreserveURLParams []string `json:"preserve_url_params,omitempty"`

	// Filters hang off the source, not off a placement, so a filter added while
	// a source sits in one scene is visible from every scene showing it. All
	// three source types carry them: a scene and a group are both obs_source_t.
	Filters []FilterSpec `json:"filters,omitempty"`

	// Items is a group's contents. They belong to the group rather than to any
	// placement of it, so a group placed in two scenes shows the same children
	// in both. Empty for inputs and for nested scenes, whose own contents are
	// captured by capturing that scene.
	Items []ItemSpec `json:"items,omitempty"`
}

// FilterSpec is one filter on a source.
type FilterSpec struct {
	Name     string                 `json:"name"`
	Kind     string                 `json:"kind"`
	Enabled  bool                   `json:"enabled"`
	Index    int                    `json:"index"`
	Settings map[string]interface{} `json:"settings,omitempty"`
}

// ItemSpec is one placement of a source.
type ItemSpec struct {
	Source string `json:"source"`

	// Occurrence distinguishes repeated placements of one source in one
	// container, counted in render order from zero. jurmiey_avatar is placed
	// twice in Game, so addressing a placement by source name alone would
	// collapse the two into one and lose a placement.
	Occurrence int `json:"occurrence"`

	// SceneItemID is the placement's identity in the live scene. Ids are unique
	// within a scene and reused across scenes, so it is only meaningful
	// alongside the scene that holds it, and it is captured for diffing rather
	// than for applying -- a created item gets whatever id OBS hands out.
	SceneItemID int `json:"scene_item_id"`

	// Order is the render position, ascending. In this collection it is often
	// the only thing distinguishing a stack of full-canvas layers, so losing it
	// renders the scene wrong while every placement still looks right.
	Order int `json:"order"`

	Enabled bool `json:"enabled"`
	Locked  bool `json:"locked"`

	// BlendMode is how the placement composites, e.g. OBS_BLEND_NORMAL.
	BlendMode string `json:"blend_mode,omitempty"`

	// Transform is the full placement geometry, including the bounds fields
	// that OBS reports as zero and then refuses on write. obs.NormaliseBounds
	// is what makes writing one back possible.
	Transform *obs.SceneItemTransform `json:"transform,omitempty"`

	// Layout states a placement as intent instead of coordinates, and is
	// resolved against the live canvas every time this spec is applied or
	// compared.
	//
	// A transform is an answer about one canvas and has no way to say so, which
	// is how a spec quietly becomes wrong when the base resolution changes. The
	// scenes this was built for are stacks of full-canvas layers, and
	//
	//	"layout": {"mode": "stretch"}
	//
	// expresses one without a single pixel coordinate.
	//
	// It overwrites the seven fields it owns -- position, alignment, bounds
	// type, bounds alignment and bounds size -- and leaves scale, rotation and
	// crop to Transform, or to the live item when Transform is absent.
	//
	// A capture never emits one: only the author knows a placement means "the
	// whole canvas" rather than "these particular numbers".
	Layout *layout.Spec `json:"layout,omitempty"`
}
