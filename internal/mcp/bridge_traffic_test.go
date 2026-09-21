package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ironystock/agentic-obs/internal/bridge"
	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notBridgeTraffic is the filter between the bridge's replies and everything
// downstream of the event stream: MCP resource notifications and the
// automation engine. Letting a reply through is ADR-010's echo problem by
// another road -- agentic-obs reacting to its own writes -- and dropping too
// much would silence real settings changes on the operator's own sources.
func TestNotBridgeTraffic(t *testing.T) {
	settingsEvent := func(name string) obs.Event {
		return obs.Event{
			Type:    obs.EventTypeInputSettingsChanged,
			Payload: map[string]interface{}{"input_name": name},
		}
	}

	cases := []struct {
		name  string
		event obs.Event
		keep  bool
	}{
		{"a mailbox reply is the bridge answering us",
			settingsEvent(bridge.MailboxSource), false},
		{"an inbox echo is our own command coming back",
			settingsEvent(bridge.InboxSource), false},
		{"an ordinary source's settings change is real news",
			settingsEvent("Webcam"), true},
		{"a source whose name merely resembles the transport is the operator's",
			settingsEvent(bridge.InboxSource + "-2"), true},

		// Only InputSettingsChanged carries input_name for the transport, so
		// every other event type passes without inspecting the payload at all.
		{"another event type about the inbox is not settings traffic",
			obs.Event{Type: obs.EventTypeSceneChanged,
				Payload: map[string]interface{}{"input_name": bridge.InboxSource}}, true},
		{"a scene change is untouched",
			obs.Event{Type: obs.EventTypeSceneChanged,
				Payload: map[string]interface{}{"scene_name": "Gaming"}}, true},

		// Malformed payloads must not be read as "this is the transport":
		// dropping a real event is the worse failure, since nothing downstream
		// would ever learn it happened.
		{"a settings event with no input_name is kept",
			obs.Event{Type: obs.EventTypeInputSettingsChanged,
				Payload: map[string]interface{}{}}, true},
		{"a settings event with a non-string input_name is kept",
			obs.Event{Type: obs.EventTypeInputSettingsChanged,
				Payload: map[string]interface{}{"input_name": 42}}, true},
		{"a settings event with a nil payload is kept",
			obs.Event{Type: obs.EventTypeInputSettingsChanged}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.keep, notBridgeTraffic(tc.event))
		})
	}
}

// get_obs_status reports the bridge rather than assuming it: scripts are
// stored per scene collection, so switching collections unloads the bridge
// with no warning.
func TestGetOBSStatusReportsTheBridge(t *testing.T) {
	server, _ := testServer(t)

	_, result, err := server.handleGetOBSStatus(context.Background(), nil, struct{}{})
	require.NoError(t, err)

	// The handler wraps the status in an anonymous struct, so the field is
	// only reachable through the JSON it actually serves.
	raw, err := json.Marshal(result)
	require.NoError(t, err)

	var payload struct {
		Bridge *bridge.Status `json:"bridge"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.NotNil(t, payload.Bridge, "the status must always carry a bridge field")

	// testServer builds a Server with no transport, which Probe reports rather
	// than panicking on.
	assert.False(t, payload.Bridge.Present)
	assert.Equal(t, "bridge not configured", payload.Bridge.Detail)
}
