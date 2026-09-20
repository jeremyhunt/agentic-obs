package scenespec

import (
	"fmt"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Reader is the read side of OBS that a capture needs.
//
// It is declared here rather than imported from internal/mcp so the package
// depends on the handful of calls it actually makes. *obs.Client and
// obstest.Fake both satisfy it, which is what lets the same capture run against
// a real OBS and against the fake.
type Reader interface {
	GetSceneByName(name string) (*obs.Scene, error)
	GetGroupSceneItemList(groupName string) ([]obs.SceneSource, error)
	GetSceneItemTransform(sceneName string, sceneItemID int) (*obs.SceneItemTransform, error)
	GetSourceSettings(sourceName string) (map[string]interface{}, error)
	GetSourceFilterList(sourceName string) ([]obs.FilterInfo, error)
	GetSourceFilter(sourceName, filterName string) (*obs.FilterDetails, error)
}

// Capture reads a scene and returns it as a document.
//
// The walk is: list the scene's placements, and for each one record the
// placement and, the first time that source is seen, the source itself. A
// source referenced by several placements is read once -- the expensive calls
// are per source, not per placement, and a scene with nine placements of two
// sources should cost two settings reads, not nine.
//
// Groups are recursed one level, because their children belong to the group
// rather than to the scene. Nested scenes are not recursed: a nested scene is
// captured by capturing that scene, and following it here would duplicate it
// into every spec that references it and recurse forever on a collection with
// a cycle.
func Capture(client Reader, sceneName string) (*Spec, error) {
	return CaptureWith(client, sceneName, FullCapture())
}

// CaptureWith reads a scene, skipping what the options exclude.
//
// Placements are never optional: they are the document. Only the per-source
// reads -- settings and filters -- can be skipped, because those are the calls
// that dominate both the time and the size of a capture.
func CaptureWith(client Reader, sceneName string, opts Options) (*Spec, error) {
	scene, err := client.GetSceneByName(sceneName)
	if err != nil {
		// GetSceneItemList answers InvalidResourceType (602) for a group, so
		// this is also the path a capture aimed at a group takes. Saying which
		// scene failed matters more than guessing why.
		return nil, fmt.Errorf("reading scene %q: %w", sceneName, err)
	}

	spec := &Spec{
		Version: SpecVersion,
		Scene:   sceneName,
		Sources: []SourceSpec{},
		Items:   []ItemSpec{},
	}
	if !opts.IncludeSettings {
		spec.Omitted = append(spec.Omitted, "settings")
	}
	if !opts.IncludeFilters {
		spec.Omitted = append(spec.Omitted, "filters")
	}

	captured := map[string]bool{}
	occurrences := map[string]int{}

	for order, placed := range scene.Sources {
		spec.Items = append(spec.Items, placement(client, sceneName, placed, order, occurrences))

		if captured[placed.Name] {
			continue
		}
		captured[placed.Name] = true

		source, err := captureSource(client, placed, opts)
		if err != nil {
			return nil, err
		}
		spec.Sources = append(spec.Sources, source)
	}

	return spec, nil
}

// placement records one scene item.
func placement(client Reader, sceneName string, placed obs.SceneSource, order int, occurrences map[string]int) ItemSpec {
	item := ItemSpec{
		Source:      placed.Name,
		Occurrence:  occurrences[placed.Name],
		SceneItemID: placed.ID,
		Order:       order,
		Enabled:     placed.Enabled,
		Locked:      placed.Locked,
		BlendMode:   placed.BlendMode,
	}
	occurrences[placed.Name]++

	// The transform is read separately because the scene listing carries only
	// position, scale and rotation, and a spec needs crop, bounds and the
	// alignment fields too -- the ones FB-54 proved get silently dropped.
	//
	// A placement whose transform cannot be read is still a placement worth
	// recording, so a failure here leaves the field empty rather than failing
	// the capture: a document missing one transform is more useful than no
	// document at all.
	if transform, err := client.GetSceneItemTransform(sceneName, placed.ID); err == nil {
		item.Transform = transform
	}

	return item
}

// captureSource records what a source is, branching on which of the three kinds
// of thing it turns out to be.
func captureSource(client Reader, placed obs.SceneSource, opts Options) (SourceSpec, error) {
	source := SourceSpec{Name: placed.Name, UUID: placed.UUID, Type: sourceTypeOf(placed)}

	switch source.Type {
	case SourceInput:
		// The kind is not optional -- without it an apply cannot create the
		// input -- and it costs nothing, because GetSceneItemList already
		// returned it on the item.
		source.Kind = placed.Kind

		if opts.IncludeSettings {
			settings, err := client.GetSourceSettings(placed.Name)
			if err != nil {
				return SourceSpec{}, fmt.Errorf("reading settings of input %q: %w", placed.Name, err)
			}
			source.Settings = settings
		}

	case SourceGroup:
		// A group opens only through GetGroupSceneItemList; GetSceneItemList
		// refuses it outright. Its children are part of the group, so they are
		// recorded here and not as placements in the scene.
		children, err := client.GetGroupSceneItemList(placed.Name)
		if err != nil {
			return SourceSpec{}, fmt.Errorf("reading contents of group %q: %w", placed.Name, err)
		}
		occurrences := map[string]int{}
		source.Items = make([]ItemSpec, 0, len(children))
		for order, child := range children {
			source.Items = append(source.Items, ItemSpec{
				Source:      child.Name,
				Occurrence:  occurrences[child.Name],
				SceneItemID: child.ID,
				Order:       order,
				Enabled:     child.Enabled,
				Locked:      child.Locked,
				BlendMode:   child.BlendMode,
			})
			occurrences[child.Name]++
		}

	case SourceScene:
		// Nothing more to record. A nested scene has no kind and no settings,
		// and its contents are that scene's own spec.
	}

	if opts.IncludeFilters {
		filters, err := captureFilters(client, placed.Name)
		if err != nil {
			return SourceSpec{}, err
		}
		source.Filters = filters
	}

	return source, nil
}

// captureFilters reads the filters on any source.
//
// All three source types carry filters, because a scene and a group are both
// obs_source_t and obs_source_filter_add takes any of them. A capture that read
// filters only for inputs would silently drop them from every container in the
// collection -- which is most of them.
func captureFilters(client Reader, sourceName string) ([]FilterSpec, error) {
	listed, err := client.GetSourceFilterList(sourceName)
	if err != nil {
		// A source with no filters and a source that refuses the call are
		// different, but obs-websocket reports an empty list for the former, so
		// an error here is a real failure and is reported as one.
		return nil, fmt.Errorf("reading filters of %q: %w", sourceName, err)
	}
	if len(listed) == 0 {
		return nil, nil
	}

	out := make([]FilterSpec, 0, len(listed))
	for _, f := range listed {
		spec := FilterSpec{Name: f.Name, Kind: f.Kind, Enabled: f.Enabled, Index: f.Index}

		// The list carries no settings, so each filter is read again for them.
		if details, err := client.GetSourceFilter(sourceName, f.Name); err == nil && details != nil {
			spec.Settings = details.Settings
		}
		out = append(out, spec)
	}
	return out, nil
}

// sourceTypeOf decides which of the three kinds of source a placement holds.
//
// The order matters. A group and a nested scene both report
// OBS_SOURCE_TYPE_SCENE, so isGroup has to be checked first; branching on the
// type alone sends a group down the scene path, where GetSceneItemList refuses
// it.
func sourceTypeOf(placed obs.SceneSource) string {
	if placed.IsGroup {
		return SourceGroup
	}
	if placed.Type == "OBS_SOURCE_TYPE_SCENE" {
		return SourceScene
	}
	return SourceInput
}
