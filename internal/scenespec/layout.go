package scenespec

import (
	"fmt"

	"github.com/ironystock/agentic-obs/internal/layout"
	"github.com/ironystock/agentic-obs/internal/obs"
)

// A layout is resolved once, before anything is compared or written.
//
// Making it a pre-pass rather than a branch inside the diff and another inside
// the apply is the whole reason it is safe: both work on a spec whose
// layout-bearing items already carry a concrete transform, so they cannot
// disagree about where a layout put something. It is also why a layout costs
// nothing for a document that has none.

// hasLayout reports whether any placement states an intent rather than numbers.
func hasLayout(spec *Spec) bool {
	for _, item := range spec.Items {
		if item.Layout != nil {
			return true
		}
	}
	for _, source := range spec.Sources {
		for _, item := range source.Items {
			if item.Layout != nil {
				return true
			}
		}
	}
	return false
}

// resolveLayouts returns the spec with every layout resolved against the live
// canvas, or an error if any of them cannot be.
//
// An unresolvable layout is refused whole rather than reported per-op. A
// failing write against a live OBS is a normal partial result -- a locked
// source, a moved file -- but an unknown mode is a malformed document, and
// applying the rest of it would leave the scene in a state the author never
// described.
func resolveLayouts(client DiffReader, spec, live *Spec) (*Spec, error) {
	if !hasLayout(spec) {
		return spec, nil
	}

	// A layout inside a group would resolve against the canvas, but a group's
	// children are positioned within the group. Answering with the wrong
	// coordinates is worse than declining, and saying nothing would be worse
	// than both.
	for _, source := range spec.Sources {
		for _, item := range source.Items {
			if item.Layout != nil {
				return nil, fmt.Errorf(
					"placement of %q inside group %q has a layout: a layout resolves "+
						"against the canvas, and a group's children are positioned "+
						"within the group, so use a transform here",
					item.Source, source.Name)
			}
		}
	}

	video, err := client.GetVideoSettings()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve a layout without the canvas: %w", err)
	}
	canvas := layout.Canvas{Width: video.BaseWidth, Height: video.BaseHeight}

	liveByKey := map[string]ItemSpec{}
	for _, item := range live.Items {
		liveByKey[itemKey(item)] = item
	}

	// A copy: the caller's document is read again after an apply, and a spec
	// that rewrote itself would report the resolved numbers as if the author
	// had written them.
	out := *spec
	out.Items = make([]ItemSpec, len(spec.Items))
	copy(out.Items, spec.Items)

	for i := range out.Items {
		item := &out.Items[i]
		if item.Layout == nil {
			continue
		}

		placed, err := layout.Resolve(*item.Layout, canvas)
		if err != nil {
			return nil, fmt.Errorf("placement of %q: %w", item.Source, err)
		}

		// The base is what the layout does not own: scale, rotation and crop.
		// The spec's own transform if it has one, otherwise the live item's --
		// so a placement that says only "fill the canvas" keeps the crop it
		// already had instead of silently losing it. (FB-54 was that class of
		// loss, on alignment.)
		base := obs.SceneItemTransform{}
		if item.Transform != nil {
			base = *item.Transform
		} else if got, ok := liveByKey[itemKey(*item)]; ok && got.Transform != nil {
			base = *got.Transform
		}

		base.PositionX = placed.PositionX
		base.PositionY = placed.PositionY
		base.Alignment = placed.Alignment
		base.BoundsType = placed.BoundsType
		base.BoundsAlignment = placed.BoundsAlignment
		base.BoundsWidth = placed.BoundsWidth
		base.BoundsHeight = placed.BoundsHeight

		item.Transform = &base
	}

	return &out, nil
}
