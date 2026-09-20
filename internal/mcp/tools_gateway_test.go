package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The gateway tools: the requests every OBS script in this workspace currently
// reaches past agentic-obs to make. obs_wire.py and obs_starting_soon.py open
// their own obsws-python connection for exactly these. (FB-67)

func TestHandleSetSourceSettings(t *testing.T) {
	t.Run("writes the settings it was given", func(t *testing.T) {
		server, mock := testServer(t)

		_, result, err := server.handleSetSourceSettings(context.Background(), nil, SetSourceSettingsInput{
			SourceName: "Webcam",
			Settings:   map[string]interface{}{"resolution": "1280x720"},
		})
		require.NoError(t, err)
		require.NotNil(t, result)

		got, err := mock.GetSourceSettings("Webcam")
		require.NoError(t, err)
		assert.Equal(t, "1280x720", got["resolution"])
	})

	t.Run("merges by default, leaving other keys alone", func(t *testing.T) {
		server, mock := testServer(t)

		// The Webcam fixture starts with video_device_id and resolution. Writing
		// one key must not clear the other -- that is the whole difference
		// between overlay true and false, and defaulting to the destructive one
		// would quietly wipe settings a caller never mentioned.
		_, _, err := server.handleSetSourceSettings(context.Background(), nil, SetSourceSettingsInput{
			SourceName: "Webcam",
			Settings:   map[string]interface{}{"resolution": "640x480"},
		})
		require.NoError(t, err)

		got, err := mock.GetSourceSettings("Webcam")
		require.NoError(t, err)
		assert.Equal(t, "640x480", got["resolution"])
		assert.Contains(t, got, "video_device_id", "a merging write must not drop untouched keys")
	})

	t.Run("replaces when overlay is explicitly false", func(t *testing.T) {
		server, mock := testServer(t)

		overlay := false
		_, _, err := server.handleSetSourceSettings(context.Background(), nil, SetSourceSettingsInput{
			SourceName: "Webcam",
			Settings:   map[string]interface{}{"resolution": "640x480"},
			Overlay:    &overlay,
		})
		require.NoError(t, err)

		got, err := mock.GetSourceSettings("Webcam")
		require.NoError(t, err)
		assert.NotContains(t, got, "video_device_id", "a replacing write resets everything not supplied")
	})

	t.Run("reports an error for a source that does not exist", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleSetSourceSettings(context.Background(), nil, SetSourceSettingsInput{
			SourceName: "NonExistent",
			Settings:   map[string]interface{}{"x": 1},
		})
		assert.Error(t, err)
	})
}

func TestHandlePressSourcePropertiesButton(t *testing.T) {
	t.Run("presses the named button", func(t *testing.T) {
		server, mock := testServer(t)

		_, result, err := server.handlePressSourcePropertiesButton(context.Background(), nil, PressSourcePropertiesButtonInput{
			SourceName:   "Webcam",
			PropertyName: "refreshnocache",
		})
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, []string{"Webcam:refreshnocache"}, mock.ButtonPresses())
	})

	t.Run("reports an error for a source that does not exist", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handlePressSourcePropertiesButton(context.Background(), nil, PressSourcePropertiesButtonInput{
			SourceName:   "NonExistent",
			PropertyName: "refreshnocache",
		})
		assert.Error(t, err)
	})
}

func TestHandleGetInputDefaultSettings(t *testing.T) {
	t.Run("returns the defaults for a kind", func(t *testing.T) {
		server, _ := testServer(t)

		_, result, err := server.handleGetInputDefaultSettings(context.Background(), nil, InputKindInput{
			InputKind: "browser_source",
		})
		require.NoError(t, err)

		defaults, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, defaults, "url", "browser sources default to a url")
	})

	t.Run("reports an error for an unknown kind", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleGetInputDefaultSettings(context.Background(), nil, InputKindInput{
			InputKind: "not_a_real_kind",
		})
		assert.Error(t, err, "an unknown kind is a caller mistake, not an empty result")
	})
}

func TestHandleListInputPropertyItems(t *testing.T) {
	t.Run("lists the items of a property", func(t *testing.T) {
		server, _ := testServer(t)

		// Generalises the audio-only path: the same request answers "which
		// windows can this window_capture target", which obs_setup_veado.py
		// needs and could not ask for.
		_, result, err := server.handleListInputPropertyItems(context.Background(), nil, InputPropertyInput{
			SourceName:   "Microphone",
			PropertyName: "device_id",
		})
		require.NoError(t, err)
		require.NotNil(t, result)

		items, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, items, "items")
	})
}
