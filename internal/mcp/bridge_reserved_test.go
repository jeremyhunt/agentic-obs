package mcp

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/bridge"
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

// ensure_input is the other path to the same write, through applySettings.
func TestEnsureInputRefusesTheBridgeTransport(t *testing.T) {
	server, mock := testServer(t)

	// The bridge's script creates these as colour sources. Seeding one makes
	// handleEnsureInput take its "exists, update it" branch, which is the
	// branch that reaches applySettings.
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
