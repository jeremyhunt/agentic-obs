package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The five typed creators fail outright when the name is taken, which makes a
// setup script single-use. if_exists says what should happen instead. (FB-72)
//
// The default is "error", which keeps today's behaviour. The plan specified
// "update", written before ensure_input existed in the same increment; now that
// it does, create meaning create and ensure meaning ensure is the clearer pair,
// and changing a shipped tool's default silently is the more expensive mistake.

func createText(t *testing.T, server *Server, in CreateTextSourceInput) (map[string]interface{}, error) {
	t.Helper()
	_, result, err := server.handleCreateTextSource(context.Background(), nil, in)
	if err != nil {
		return nil, err
	}
	res, ok := result.(map[string]interface{})
	require.True(t, ok)
	return res, nil
}

func TestCreateSourceIfExists(t *testing.T) {
	base := CreateTextSourceInput{
		SceneName:  "Scene 1",
		SourceName: "Caption",
		Text:       "hello",
	}

	t.Run("creates when the name is free", func(t *testing.T) {
		server, _ := testServer(t)

		res, err := createText(t, server, base)
		require.NoError(t, err)
		assert.Equal(t, "created", res["action"])
	})

	t.Run("fails on a second call by default", func(t *testing.T) {
		server, _ := testServer(t)

		_, err := createText(t, server, base)
		require.NoError(t, err)

		// Unchanged from before if_exists existed: a bare create still refuses to
		// touch something that is already there.
		_, err = createText(t, server, base)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "if_exists",
			"the error should say how to make the call re-runnable")
	})

	t.Run("if_exists=update rewrites the settings", func(t *testing.T) {
		server, mock := testServer(t)

		_, err := createText(t, server, base)
		require.NoError(t, err)

		second := base
		second.Text = "goodbye"
		second.IfExists = "update"

		res, err := createText(t, server, second)
		require.NoError(t, err)
		assert.Equal(t, "updated", res["action"])

		settings, err := mock.GetSourceSettings("Caption")
		require.NoError(t, err)
		assert.Equal(t, "goodbye", settings["text"])
	})

	t.Run("if_exists=update reports unchanged when nothing differs", func(t *testing.T) {
		server, _ := testServer(t)

		_, err := createText(t, server, base)
		require.NoError(t, err)

		again := base
		again.IfExists = "update"
		res, err := createText(t, server, again)
		require.NoError(t, err)
		assert.Equal(t, "unchanged", res["action"])
	})

	t.Run("if_exists=skip leaves the existing source alone", func(t *testing.T) {
		server, mock := testServer(t)

		_, err := createText(t, server, base)
		require.NoError(t, err)

		second := base
		second.Text = "goodbye"
		second.IfExists = "skip"

		res, err := createText(t, server, second)
		require.NoError(t, err)
		assert.Equal(t, "skipped", res["action"])

		settings, err := mock.GetSourceSettings("Caption")
		require.NoError(t, err)
		assert.Equal(t, "hello", settings["text"], "skip must not write")
	})

	t.Run("an unknown if_exists value is refused", func(t *testing.T) {
		server, _ := testServer(t)

		bad := base
		bad.IfExists = "overwrite"

		_, err := createText(t, server, bad)
		require.Error(t, err)
		// Guessing at what an unrecognised policy meant is how a destructive
		// write happens by accident.
		assert.Contains(t, err.Error(), "overwrite")
	})

	t.Run("the policy applies to every typed creator", func(t *testing.T) {
		server, _ := testServer(t)

		// Each creator builds its own settings map; the policy lives in one
		// place so it cannot be applied to some and forgotten on others.
		_, _, err := server.handleCreateColorSource(context.Background(), nil, CreateColorSourceInput{
			SceneName: "Scene 1", SourceName: "Backdrop", Color: 4278190080,
		})
		require.NoError(t, err)

		_, _, err = server.handleCreateColorSource(context.Background(), nil, CreateColorSourceInput{
			SceneName: "Scene 1", SourceName: "Backdrop", Color: 4278190080,
		})
		require.Error(t, err, "create_color_source must refuse a taken name too")

		_, result, err := server.handleCreateColorSource(context.Background(), nil, CreateColorSourceInput{
			SceneName: "Scene 1", SourceName: "Backdrop", Color: 4278190080, IfExists: "skip",
		})
		require.NoError(t, err)
		res, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "skipped", res["action"])
	})
}

// TestCreateBrowserSourceOnlySendsSuppliedKeys covers the browser keys FB-72
// exposed, and the reason they are all optional.
//
// OBS ships a default stylesheet that hides scrollbars and makes the background
// transparent -- what an overlay wants. Sending css:"" because the field was
// absent would remove it, and the source would render on white. The same
// argument applies to restart_when_active, which obs_wire.py depends on staying
// off to keep a websocket alive across scene switches.
func TestCreateBrowserSourceOnlySendsSuppliedKeys(t *testing.T) {
	t.Run("absent keys are not written", func(t *testing.T) {
		server, mock := testServer(t)

		_, _, err := server.handleCreateBrowserSource(context.Background(), nil, CreateBrowserSourceInput{
			SceneName: "Scene 1", SourceName: "Overlay", URL: "http://localhost/w",
		})
		require.NoError(t, err)

		settings, err := mock.GetSourceSettings("Overlay")
		require.NoError(t, err)

		assert.NotContains(t, settings, "css",
			"an absent css must not be sent as empty; that removes OBS's default stylesheet")
		assert.NotContains(t, settings, "restart_when_active",
			"an absent restart_when_active must be left alone, not defaulted to false")
		assert.NotContains(t, settings, "shutdown")
		assert.NotContains(t, settings, "is_local_file")
	})

	t.Run("supplied keys are written, including false", func(t *testing.T) {
		server, mock := testServer(t)

		off := false
		_, _, err := server.handleCreateBrowserSource(context.Background(), nil, CreateBrowserSourceInput{
			SceneName: "Scene 1", SourceName: "Overlay", URL: "http://localhost/w",
			CSS:               "body { background: transparent }",
			RestartWhenActive: &off,
		})
		require.NoError(t, err)

		settings, err := mock.GetSourceSettings("Overlay")
		require.NoError(t, err)

		assert.Equal(t, "body { background: transparent }", settings["css"])
		// Explicitly false is a request, not an absence -- which is why the
		// field is a pointer.
		assert.Equal(t, false, settings["restart_when_active"])
	})
}
