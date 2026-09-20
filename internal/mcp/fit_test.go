package mcp

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// set_source_transform gains a fit block: say what you want rather than compute
// a scale factor. It reads the canvas itself, so a caller no longer has to know
// the resolution to place something against it. (FB-75)

func TestSetSourceTransformFit(t *testing.T) {
	t.Run("fills the canvas without being told its size", func(t *testing.T) {
		server, mock := testServer(t)

		_, _, err := server.handleSetSourceTransform(context.Background(), nil, SetSourceTransformInput{
			SceneName:  "Scene 1",
			SourceName: "Text",
			Fit:        &FitInput{Mode: "fit"},
		})
		require.NoError(t, err)

		tr, err := mock.GetSceneItemTransform("Scene 1", 2)
		require.NoError(t, err)

		// The mock canvas is 2560x1440, deliberately not the 1920x1080 a
		// hardcoded assumption would use.
		assert.Equal(t, obs.BoundsScaleInner, tr.BoundsType)
		assert.Equal(t, 2560.0, tr.BoundsWidth)
		assert.Equal(t, 1440.0, tr.BoundsHeight)
		assert.Equal(t, 0.0, tr.PositionX)
		assert.Equal(t, 0.0, tr.PositionY)
	})

	t.Run("places into a region", func(t *testing.T) {
		server, mock := testServer(t)

		_, _, err := server.handleSetSourceTransform(context.Background(), nil, SetSourceTransformInput{
			SceneName:  "Scene 1",
			SourceName: "Text",
			Fit: &FitInput{
				Mode:   "fill",
				Anchor: "bottom-right",
				Region: &RegionInput{X: 1280, Y: 720, Width: 1280, Height: 720},
			},
		})
		require.NoError(t, err)

		tr, err := mock.GetSceneItemTransform("Scene 1", 2)
		require.NoError(t, err)

		assert.Equal(t, obs.BoundsScaleOuter, tr.BoundsType)
		assert.Equal(t, 1280.0, tr.PositionX)
		assert.Equal(t, 720.0, tr.PositionY)
		assert.Equal(t, obs.AlignBottom|obs.AlignRight, tr.BoundsAlignment)
	})

	t.Run("leaves fields the layout does not own alone", func(t *testing.T) {
		server, mock := testServer(t)

		// Crop is not a placement concern, so a fit must not clear it. This is
		// the read-modify-write property FB-54 and FB-64 were both about.
		current, err := mock.GetSceneItemTransform("Scene 1", 2)
		require.NoError(t, err)
		current.CropTop = 12
		require.NoError(t, mock.SetSceneItemTransform("Scene 1", 2, current))

		_, _, err = server.handleSetSourceTransform(context.Background(), nil, SetSourceTransformInput{
			SceneName: "Scene 1", SourceName: "Text", Fit: &FitInput{Mode: "fit"},
		})
		require.NoError(t, err)

		tr, err := mock.GetSceneItemTransform("Scene 1", 2)
		require.NoError(t, err)
		assert.Equal(t, 12, tr.CropTop, "a fit must not reset crop")
	})

	t.Run("explicit coordinates still work and win", func(t *testing.T) {
		server, mock := testServer(t)

		// The existing x/y path is untouched; supplying both is a caller
		// mistake, and the explicit number is the more specific request.
		x := 42.0
		_, _, err := server.handleSetSourceTransform(context.Background(), nil, SetSourceTransformInput{
			SceneName: "Scene 1", SourceName: "Text",
			X:   &x,
			Fit: &FitInput{Mode: "fit"},
		})
		require.NoError(t, err)

		tr, err := mock.GetSceneItemTransform("Scene 1", 2)
		require.NoError(t, err)
		assert.Equal(t, 42.0, tr.PositionX, "an explicit x overrides the layout's position")
		assert.Equal(t, obs.BoundsScaleInner, tr.BoundsType, "the rest of the layout still applies")
	})

	t.Run("an unknown mode is refused with the list", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleSetSourceTransform(context.Background(), nil, SetSourceTransformInput{
			SceneName: "Scene 1", SourceName: "Text",
			Fit: &FitInput{Mode: "cover"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fill", "naming the modes beats saying no")
	})
}
