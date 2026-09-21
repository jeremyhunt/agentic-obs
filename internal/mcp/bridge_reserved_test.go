package mcp

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/bridge"
	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/ironystock/agentic-obs/internal/scenespec"
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

// The typed create_* tools share one worker, and the guard belongs there rather
// than in five handlers.
//
// The dangerous branch does not look dangerous. if_exists=update ends in
// s.ensureInput -- the unexported worker, which carries no guard; only
// handleEnsureInput does -- and the kind check above it does not stop anyone,
// because the inbox really IS a color_source_v3: that is what the bridge's Lua
// creates. So create_color_source matched the transport exactly and fell
// through to place it in a live scene and write its settings, from a
// default-enabled group.
func TestCreateTypedSourceRefusesTheBridgeTransport(t *testing.T) {
	t.Run("if_exists=update against the real transport", func(t *testing.T) {
		server, mock := testServer(t)

		// Same kind the bridge's Lua uses, so the kind check cannot be what
		// refuses this and the test is the real case.
		_, err := mock.CreateInput("Scene 1", bridge.InboxSource, "color_source_v3", nil)
		require.NoError(t, err)
		before, err := mock.GetSceneByName("Gaming")
		require.NoError(t, err)

		_, _, err = server.handleCreateColorSource(context.Background(), nil, CreateColorSourceInput{
			SceneName:  "Gaming",
			SourceName: bridge.InboxSource,
			Color:      0xFF000000,
			IfExists:   "update",
		})
		require.Error(t, err, "create_color_source must not reach the transport")
		assert.Contains(t, err.Error(), "run_lua_in_obs")
		assert.Contains(t, err.Error(), "create_color_source", "the refusal must name the tool that was used")

		after, err := mock.GetSceneByName("Gaming")
		require.NoError(t, err)
		assert.Equal(t, len(before.Sources), len(after.Sources),
			"the transport was placed in a live scene before refusing")
	})

	t.Run("the create path plants no decoy", func(t *testing.T) {
		// With the bridge absent the name is free, and taking it collides with
		// the real transport the day install-bridge runs.
		server, mock := testServer(t)

		_, _, err := server.handleCreateTextSource(context.Background(), nil, CreateTextSourceInput{
			SceneName:  "Gaming",
			SourceName: bridge.MailboxSource,
			Text:       "hello",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_lua_in_obs")

		_, err = mock.GetSourceSettings(bridge.MailboxSource)
		assert.Error(t, err, "a source was created under the reserved name")
	})

	t.Run("create_audio_input, which does not share that worker", func(t *testing.T) {
		server, mock := testServer(t)

		_, _, err := server.handleCreateAudioInput(context.Background(), nil, CreateAudioInputInput{
			SceneName:  "Gaming",
			SourceName: bridge.InboxSource,
			DeviceKind: "input",
			DeviceID:   "default",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_lua_in_obs")

		_, err = mock.GetSourceSettings(bridge.InboxSource)
		assert.Error(t, err, "a source was created under the reserved name")
	})

	t.Run("ordinary sources are untouched", func(t *testing.T) {
		server, mock := testServer(t)

		_, _, err := server.handleCreateColorSource(context.Background(), nil, CreateColorSourceInput{
			SceneName:  "Gaming",
			SourceName: "agentic-obs-inbox-2",
			Color:      0xFF000000,
		})
		require.NoError(t, err, "a name that merely resembles the transport is a user's own source")

		_, err = mock.GetSourceSettings("agentic-obs-inbox-2")
		assert.NoError(t, err)
	})
}

// apply_scene_preset shares the reconciler, so it inherited the spec guard --
// and refusing there would have been a pure false positive. The preset is
// masked to fields=["enabled"], under which the only write available is
// SetSceneItemEnabled on a placement OBS already holds: it provably cannot
// write, create or place a source.
//
// Reachable in practice, which is why this is a test rather than a note: the
// entries handleApplyScenePreset drops are the ones absent from the LIVE scene,
// so a transport someone placed by hand is captured by save_scene_preset and
// survives into the spec.
func TestApplyScenePresetIsNotRefusedForNamingTheTransport(t *testing.T) {
	server, mock, db := testServerWithStorage(t)

	// Placed in the scene, the way a hand-edited collection would leave it.
	_, err := mock.CreateInput("Scene 1", bridge.InboxSource, "color_source_v3", nil)
	require.NoError(t, err)

	// Captured, rather than hand-written, so the whole chain is under test.
	_, _, err = server.handleSaveScenePreset(context.Background(), nil, SavePresetInput{
		PresetName: "All on",
		SceneName:  "Scene 1",
	})
	require.NoError(t, err)

	preset, err := db.GetScenePreset(context.Background(), "All on")
	require.NoError(t, err)
	named := false
	for _, src := range preset.Sources {
		if src.Name == bridge.InboxSource {
			named = true
		}
	}
	require.True(t, named, "the preset does not name the transport, so this test proves nothing")

	_, _, err = server.handleApplyScenePreset(context.Background(), nil,
		ApplyPresetInput{PresetName: "All on"})
	require.NoError(t, err, "a visibility-only preset cannot reach a source and must not be refused")

	// And it still cannot have written the transport's settings.
	settings, err := mock.GetSourceSettings(bridge.InboxSource)
	require.NoError(t, err)
	assert.NotContains(t, settings, "lua")
}

// apply_scene_spec was the second of the two routes ADR-013 recorded as open.
// The guard is in scenespec.Apply rather than in this handler, so every caller
// of the reconciler inherits it; these tests pin that the tool surfaces it.
func TestApplySceneSpecRefusesASpecNamingTheBridgeTransport(t *testing.T) {
	transportSpec := func(scene, name string) *scenespec.Spec {
		return &scenespec.Spec{
			Version: scenespec.SpecVersion,
			Scene:   scene,
			Sources: []scenespec.SourceSpec{{
				Name:     name,
				Type:     scenespec.SourceInput,
				Kind:     "color_source_v3",
				Settings: bridgeCommand(),
			}},
			Items: []scenespec.ItemSpec{{Source: name, Enabled: true}},
		}
	}

	for _, name := range []string{bridge.InboxSource, bridge.MailboxSource} {
		t.Run(name, func(t *testing.T) {
			server, mock := testServer(t)
			_, err := mock.CreateInput("Scene 1", name, "color_source_v3", nil)
			require.NoError(t, err)

			writes := false
			_, _, err = server.handleApplySceneSpec(context.Background(), nil, ApplySceneSpecInput{
				Spec:      transportSpec("Gaming", name),
				SceneName: "Gaming",
				DryRun:    &writes,
			})
			require.Error(t, err, "an apply naming the transport must be refused")
			assert.Contains(t, err.Error(), name)

			settings, err := mock.GetSourceSettings(name)
			require.NoError(t, err)
			assert.NotContains(t, settings, "lua", "the chunk must not have reached the transport")
		})
	}

	// The dry run stays open on purpose: it writes nothing, and planning is how
	// a caller finds out a stored spec names the transport at all.
	t.Run("the dry run still plans", func(t *testing.T) {
		server, mock := testServer(t)
		_, err := mock.CreateInput("Scene 1", bridge.InboxSource, "color_source_v3", nil)
		require.NoError(t, err)

		_, _, err = server.handleApplySceneSpec(context.Background(), nil, ApplySceneSpecInput{
			Spec:      transportSpec("Gaming", bridge.InboxSource),
			SceneName: "Gaming",
		})
		assert.NoError(t, err, "apply_scene_spec defaults to a dry run, which writes nothing")

		settings, err := mock.GetSourceSettings(bridge.InboxSource)
		require.NoError(t, err)
		assert.NotContains(t, settings, "lua")
	})
}
