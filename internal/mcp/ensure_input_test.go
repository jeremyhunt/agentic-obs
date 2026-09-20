package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ensure_input is create-or-update: the idiom every setup script in this
// workspace hand-rolls, because the five create_* tools fail on a second run and
// there was no way to say "make this exist, like this". (FB-71)
//
// It reports which of the three things happened, because they are not
// interchangeable to a caller deciding whether anything needs doing.

func ensure(t *testing.T, server *Server, input EnsureInputInput) map[string]interface{} {
	t.Helper()
	_, result, err := server.handleEnsureInput(context.Background(), nil, input)
	require.NoError(t, err)
	res, ok := result.(map[string]interface{})
	require.True(t, ok)
	return res
}

func TestEnsureInput(t *testing.T) {
	t.Run("creates an input that does not exist", func(t *testing.T) {
		server, mock := testServer(t)

		res := ensure(t, server, EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: "Brand New",
			InputKind:  "color_source_v3",
			Settings:   map[string]interface{}{"width": 640.0},
		})
		assert.Equal(t, "created", res["action"])

		settings, err := mock.GetSourceSettings("Brand New")
		require.NoError(t, err)
		assert.Equal(t, 640.0, settings["width"])
	})

	t.Run("is idempotent: a second identical call changes nothing", func(t *testing.T) {
		server, _ := testServer(t)

		in := EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: "Brand New",
			InputKind:  "color_source_v3",
			Settings:   map[string]interface{}{"width": 640.0},
		}
		assert.Equal(t, "created", ensure(t, server, in)["action"])

		// The whole reason the tool exists: running a setup script twice must be
		// safe, and must say that nothing needed doing rather than reporting a
		// write it did not make.
		assert.Equal(t, "unchanged", ensure(t, server, in)["action"])
	})

	t.Run("updates an input whose settings differ", func(t *testing.T) {
		server, mock := testServer(t)

		base := EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: "Brand New",
			InputKind:  "color_source_v3",
			Settings:   map[string]interface{}{"width": 640.0},
		}
		ensure(t, server, base)

		base.Settings = map[string]interface{}{"width": 1280.0}
		assert.Equal(t, "updated", ensure(t, server, base)["action"])

		settings, err := mock.GetSourceSettings("Brand New")
		require.NoError(t, err)
		assert.Equal(t, 1280.0, settings["width"])
	})

	t.Run("places an existing input into a scene that lacks it", func(t *testing.T) {
		server, mock := testServer(t)

		// "Webcam" already exists and is in Scene 1. Ensuring it into Gaming must
		// add a placement rather than fail on the name being taken, and must not
		// create a second input -- that is the OVERLAY_NowPlaying case.
		res := ensure(t, server, EnsureInputInput{
			SceneName:  "Starting Soon",
			SourceName: "Webcam",
			InputKind:  "dshow_input",
		})
		assert.Equal(t, "placed", res["action"])

		scene, err := mock.GetSceneByName("Starting Soon")
		require.NoError(t, err)
		found := false
		for _, src := range scene.Sources {
			if src.Name == "Webcam" {
				found = true
			}
		}
		assert.True(t, found, "the input must now be placed in the scene")

		// And it must be the *same* input, not a new one with the same name.
		//
		// Checking only that the scene contains the name is too weak: creating a
		// second input would satisfy it while leaving two objects to configure
		// and keep in step, which is the problem placing exists to avoid. Caught
		// by mutation -- swapping CreateSceneItem for CreateInput left this test
		// green. (FB-71)
		inputs, err := mock.ListSources()
		require.NoError(t, err)
		count := 0
		for _, in := range inputs {
			if in != nil && in.InputName == "Webcam" {
				count++
			}
		}
		assert.Equal(t, 1, count, "placing shares the existing input; it must not create another")
	})

	t.Run("refuses an existing input of a different kind", func(t *testing.T) {
		server, _ := testServer(t)

		// Silently updating would mean the caller believes it has a browser
		// source and has a webcam. There is no safe reinterpretation, so this is
		// the caller's decision to make.
		_, _, err := server.handleEnsureInput(context.Background(), nil, EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: "Webcam",
			InputKind:  "browser_source",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dshow_input", "the error must name the kind that is actually there")
	})

	t.Run("reports the scene item id in every outcome", func(t *testing.T) {
		server, _ := testServer(t)

		in := EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: "Brand New",
			InputKind:  "color_source_v3",
			Settings:   map[string]interface{}{"width": 640.0},
		}

		// A caller almost always wants to position what it just ensured, and
		// without the id it has to go and look the item up.
		created := ensure(t, server, in)
		assert.NotNil(t, created["scene_item_id"])

		unchanged := ensure(t, server, in)
		assert.Equal(t, created["scene_item_id"], unchanged["scene_item_id"])
	})
}
