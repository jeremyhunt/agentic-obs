package mcp

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/bridge"
	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bridge's transport is reachable by name, so a general-purpose settings
// write was a second route to running Lua inside OBS -- one with no build tag,
// no AGENTIC_OBS_SCRIPTING and no confirmation. These tests pin the refusal.
//
// The payload below is the scripting channel's own wire format
// (internal/bridge/transport.go): {id, lua, args} on the inbox, answered on the
// mailbox. Writing it through set_source_settings on a stock build ran the
// chunk.

func bridgeCommand() map[string]interface{} {
	return map[string]interface{}{
		"id":   "1",
		"lua":  "return 6 * 7",
		"args": []interface{}{},
	}
}

func TestIsBridgeTransport(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{bridge.InboxSource, true},
		{bridge.MailboxSource, true},
		{"Webcam", false},
		{"", false},
		{"agentic-obs-inbox ", false},  // trailing space: a different source
		{"Agentic-OBS-Inbox", false},   // OBS source names are case-sensitive
		{"agentic-obs-inbox-2", false}, // a user's own source, not ours
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isBridgeTransport(tc.name), "isBridgeTransport(%q)", tc.name)
	}
}

func TestSetSourceSettingsRefusesTheBridgeTransport(t *testing.T) {
	for _, name := range []string{bridge.InboxSource, bridge.MailboxSource} {
		t.Run(name, func(t *testing.T) {
			server, mock := testServer(t)

			// The source has to exist for this to be the real case: without
			// it the mock refuses an unknown name anyway, and the test would
			// pass with no guard at all.
			_, err := mock.CreateInput("Scene 1", name, "color_source_v3", nil)
			require.NoError(t, err)

			_, _, err = server.handleSetSourceSettings(context.Background(), nil, SetSourceSettingsInput{
				SourceName: name,
				Settings:   bridgeCommand(),
			})
			require.Error(t, err, "a settings write to the transport must be refused")
			assert.Contains(t, err.Error(), "run_lua_in_obs", "the refusal must point at the gated tool")

			// Refused before OBS was touched, not merely reported afterwards.
			settings, err := mock.GetSourceSettings(name)
			require.NoError(t, err)
			assert.NotContains(t, settings, "lua", "the chunk must not have reached the transport")
		})
	}
}

func TestSetSourceSettingsStillWritesOrdinarySources(t *testing.T) {
	server, mock := testServer(t)

	_, _, err := server.handleSetSourceSettings(context.Background(), nil, SetSourceSettingsInput{
		SourceName: "Webcam",
		Settings:   map[string]interface{}{"resolution": "1280x720"},
	})
	require.NoError(t, err, "the guard must not touch ordinary sources")

	got, err := mock.GetSourceSettings("Webcam")
	require.NoError(t, err)
	assert.Equal(t, "1280x720", got["resolution"])
}

// ensure_input is the other path to the same write, and it has THREE paths of
// its own: create, place-then-write, and write. Only the last ends in
// applySettings, so a guard there let the other two run first -- the place
// path adds the transport to a live scene as a visible colour source before
// refusing, and with settings omitted it never refuses at all.
//
// The transport's sources belong to no scene in a real OBS. The fake mirrors
// OBS's refcounting, where an input with no placement anywhere cannot exist,
// so "in another scene" stands in for it: findPlacement reports both as not
// placed in the target scene, which is the same branch.
func TestEnsureInputRefusesTheBridgeTransportOnEveryPath(t *testing.T) {
	sceneSourceCount := func(t *testing.T, mock *testutil.MockOBSClient, scene string) int {
		t.Helper()
		got, err := mock.GetSceneByName(scene)
		require.NoError(t, err)
		return len(got.Sources)
	}

	t.Run("the create path: no such source yet", func(t *testing.T) {
		server, mock := testServer(t)
		before := sceneSourceCount(t, mock, "Gaming")

		_, _, err := server.handleEnsureInput(context.Background(), nil, EnsureInputInput{
			SceneName:  "Gaming",
			SourceName: bridge.InboxSource,
			InputKind:  "color_source_v3",
			Settings:   bridgeCommand(),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_lua_in_obs")

		// No decoy left behind to collide with the real transport the day the
		// bridge is installed.
		_, err = mock.GetSourceSettings(bridge.InboxSource)
		assert.Error(t, err, "a source must not have been created under the reserved name")
		assert.Equal(t, before, sceneSourceCount(t, mock, "Gaming"))
	})

	t.Run("the place path: the source exists outside this scene", func(t *testing.T) {
		server, mock := testServer(t)
		_, err := mock.CreateInput("Scene 1", bridge.InboxSource, "color_source_v3", nil)
		require.NoError(t, err)
		before := sceneSourceCount(t, mock, "Gaming")

		// Settings omitted on purpose: this is the call that used to report
		// "placed" and never refuse at all.
		_, _, err = server.handleEnsureInput(context.Background(), nil, EnsureInputInput{
			SceneName:  "Gaming",
			SourceName: bridge.InboxSource,
			InputKind:  "color_source_v3",
		})
		require.Error(t, err, "placing the transport into a scene must be refused")
		assert.Contains(t, err.Error(), "run_lua_in_obs")
		assert.Equal(t, before, sceneSourceCount(t, mock, "Gaming"),
			"the transport must not have been added to the scene before refusing")
	})

	t.Run("the write path: the source is already in this scene", func(t *testing.T) {
		server, mock := testServer(t)
		_, err := mock.CreateInput("Scene 1", bridge.InboxSource, "color_source_v3", nil)
		require.NoError(t, err)

		_, _, err = server.handleEnsureInput(context.Background(), nil, EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: bridge.InboxSource,
			InputKind:  "color_source_v3",
			Settings:   bridgeCommand(),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_lua_in_obs")

		settings, err := mock.GetSourceSettings(bridge.InboxSource)
		require.NoError(t, err)
		assert.NotContains(t, settings, "lua", "the chunk must not have reached the inbox")
	})

	// The guard sits above the kind check, so a caller cannot get a different
	// error -- or a different outcome -- by naming the wrong kind.
	t.Run("whatever kind is claimed", func(t *testing.T) {
		server, mock := testServer(t)
		_, err := mock.CreateInput("Scene 1", bridge.MailboxSource, "color_source_v3", nil)
		require.NoError(t, err)

		_, _, err = server.handleEnsureInput(context.Background(), nil, EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: bridge.MailboxSource,
			InputKind:  "browser_source",
			Settings:   bridgeCommand(),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_lua_in_obs",
			"the reservation must win over the kind-mismatch message")
	})
}

// Not an escalation, but ADR-013 lists deleting the inbox as a hazard: it
// breaks the bridge with no error anywhere.
func TestRemoveAndDuplicateSourceRefuseTheBridgeTransport(t *testing.T) {
	for _, name := range []string{bridge.InboxSource, bridge.MailboxSource} {
		t.Run("remove_source/"+name, func(t *testing.T) {
			server, _ := testServer(t)

			_, _, err := server.handleRemoveSource(context.Background(), nil, RemoveSourceInput{
				SceneName:  "Scene 1",
				SourceName: name,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "uninstall-bridge", "the refusal must name the supported way out")
		})

		t.Run("duplicate_source/"+name, func(t *testing.T) {
			server, _ := testServer(t)

			_, _, err := server.handleDuplicateSource(context.Background(), nil, DuplicateSourceInput{
				SceneName:  "Scene 1",
				SourceName: name,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "uninstall-bridge")
		})
	}
}

// Reads are deliberately left open: they expose nothing get_obs_status's
// bridge field does not already report, and refusing them would be noise.
func TestReadingTheBridgeTransportIsNotRefused(t *testing.T) {
	server, mock := testServer(t)

	_, err := mock.CreateInput("Scene 1", bridge.MailboxSource, "color_source_v3", nil)
	require.NoError(t, err)

	_, _, err = server.handleGetSourceSettings(context.Background(), nil, SourceNameInput{
		SourceName: bridge.MailboxSource,
	})
	assert.NoError(t, err, "reads of the transport stay open")
}
