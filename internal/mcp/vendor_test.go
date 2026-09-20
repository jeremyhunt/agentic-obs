package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// call_vendor_request is the extension channel. A vendor is a name a
// third-party plugin or script registers with obs-websocket, and calling one is
// how you reach anything obs-websocket does not implement itself -- Advanced
// Scene Switcher macros, obs-browser's page events, an in-OBS script. (FB-77)
//
// The client has had CallVendorRequest all along with nothing exposing it.

func TestCallVendorRequest(t *testing.T) {
	t.Run("passes the call through and returns the response", func(t *testing.T) {
		server, mock := testServer(t)
		mock.SetVendorResponse("AdvancedSceneSwitcher", "AdvancedSceneSwitcherRunMacro",
			map[string]any{"ok": true})

		_, result, err := server.handleCallVendorRequest(context.Background(), nil, CallVendorRequestInput{
			VendorName:  "AdvancedSceneSwitcher",
			RequestType: "AdvancedSceneSwitcherRunMacro",
			RequestData: map[string]interface{}{"macro": "PersonaShow_Sonic"},
		})
		require.NoError(t, err)

		res, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "AdvancedSceneSwitcher", res["vendor_name"])
		assert.Equal(t, "AdvancedSceneSwitcherRunMacro", res["request_type"])

		data, ok := res["response_data"].(map[string]any)
		require.True(t, ok, "the vendor's response must come back, not be summarised away")
		assert.Equal(t, true, data["ok"])
	})

	t.Run("records what it sent", func(t *testing.T) {
		server, mock := testServer(t)

		_, _, err := server.handleCallVendorRequest(context.Background(), nil, CallVendorRequestInput{
			VendorName:  "obs-browser",
			RequestType: "emit_event",
			RequestData: map[string]interface{}{"event_name": "song-changed"},
		})
		require.NoError(t, err)

		calls := mock.VendorCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, "obs-browser", calls[0].VendorName)
		assert.Equal(t, "emit_event", calls[0].RequestType)
		assert.Equal(t, "song-changed", calls[0].RequestData["event_name"])
	})

	t.Run("omitted request data is sent as an empty object", func(t *testing.T) {
		server, mock := testServer(t)
		mock.SetVendorResponse("AdvancedSceneSwitcher", "GetSceneSwitcherStatus", map[string]any{})

		// The protocol says requestData defaults to {}. Sending nil where a
		// vendor expects an object is a difference the vendor decides the
		// meaning of, so it is normalised here rather than passed along.
		_, _, err := server.handleCallVendorRequest(context.Background(), nil, CallVendorRequestInput{
			VendorName:  "AdvancedSceneSwitcher",
			RequestType: "GetSceneSwitcherStatus",
		})
		require.NoError(t, err)

		calls := mock.VendorCalls()
		require.Len(t, calls, 1)
		assert.NotNil(t, calls[0].RequestData, "requestData defaults to {} per the protocol")
	})

	t.Run("a vendor that is not installed is reported", func(t *testing.T) {
		server, _ := testServer(t)

		// Vendors cannot be enumerated, so "is it there?" is only answerable by
		// asking. The error has to name the vendor, or the caller cannot tell a
		// missing plugin from a bad request type.
		_, _, err := server.handleCallVendorRequest(context.Background(), nil, CallVendorRequestInput{
			VendorName:  "NotInstalled",
			RequestType: "anything",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotInstalled")
	})

	t.Run("requires a vendor and a request type", func(t *testing.T) {
		server, _ := testServer(t)

		for _, in := range []CallVendorRequestInput{
			{RequestType: "x"},
			{VendorName: "y"},
		} {
			_, _, err := server.handleCallVendorRequest(context.Background(), nil, in)
			assert.Error(t, err, "both fields are required by the protocol")
		}
	})
}
