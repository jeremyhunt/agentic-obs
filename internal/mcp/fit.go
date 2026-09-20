package mcp

import (
	"fmt"

	"github.com/ironystock/agentic-obs/internal/layout"
	"github.com/ironystock/agentic-obs/internal/obs"
)

// applyFit resolves a placement against the live canvas and writes the result
// into a transform, leaving every field the layout does not own untouched.
//
// Crop in particular: it is not a placement concern, and a fit that reset it
// would be the same class of silent loss as FB-54's dropped alignment. (FB-75)
func (s *Server) applyFit(transform *obs.SceneItemTransform, fit *FitInput) error {
	// Read the canvas rather than take it as a parameter. A caller placing
	// something has no business needing to know the resolution first, and the
	// scene-designer skill assuming 1920x1080 is what made this necessary.
	video, err := s.obsClient.GetVideoSettings()
	if err != nil {
		return fmt.Errorf("cannot place against the canvas: %w", err)
	}

	spec := layout.Spec{Mode: fit.Mode, Anchor: fit.Anchor}
	if fit.Region != nil {
		spec.Region = &layout.Region{
			X: fit.Region.X, Y: fit.Region.Y,
			Width: fit.Region.Width, Height: fit.Region.Height,
		}
	}

	placed, err := layout.Resolve(spec, layout.Canvas{
		Width:  video.BaseWidth,
		Height: video.BaseHeight,
	})
	if err != nil {
		return err
	}

	transform.PositionX = placed.PositionX
	transform.PositionY = placed.PositionY
	transform.Alignment = placed.Alignment
	transform.BoundsType = placed.BoundsType
	transform.BoundsAlignment = placed.BoundsAlignment
	transform.BoundsWidth = placed.BoundsWidth
	transform.BoundsHeight = placed.BoundsHeight

	return nil
}
