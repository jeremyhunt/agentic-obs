package obs

import (
	"errors"
	"strings"
	"testing"
)

// The request registry is what makes "every OBS control surface" a claim the
// code can keep rather than a roadmap item. agentic-obs wraps roughly 65 of
// obs-websocket's 151 requests as typed methods; the registry reaches the rest
// without one wrapper per request.

func TestRegistryCoversTheRequestsGoobsGenerates(t *testing.T) {
	reg := newRequestRegistry()

	// goobs v1.8.3 generates 147 of the 151 requests OBS 32.2.2 advertises.
	// A sharp lower bound rather than a soft one: the registry is built by
	// reflection over goobs' generated shape, so if that shape changes the
	// count collapses quietly, and a quiet collapse is the failure mode worth
	// catching. The live test compares against the server's own list.
	if got := len(reg.names); got < 147 {
		t.Errorf("registry holds %d requests, want at least 147 -- goobs' generated "+
			"method shape has probably changed, which this registry reads by reflection", got)
	}
}

func TestRegistryReachesRequestsNoWrapperCovers(t *testing.T) {
	reg := newRequestRegistry()

	// Each of these is a control surface with no typed method in this package,
	// drawn from the gap table in the design plan. They are the reason the
	// registry exists, so naming them here makes the claim checkable.
	for _, name := range []string{
		"GetStats",                   // telemetry: cpu, memory, dropped frames
		"TriggerMediaInputAction",    // media playback control
		"SetMediaInputCursor",        //
		"GetProfileList",             // profiles (there is no GetCurrentProfile;
		"SetCurrentProfile",          // GetProfileList reports the current one)
		"CreateSceneCollection",      // scene collections
		"OpenInputPropertiesDialog",  // dialogs on the operator's machine
		"OpenSourceProjector",        // projectors
		"SplitRecordFile",            // recording
		"CreateRecordChapter",        //
		"GetOutputList",              // generic outputs beyond vcam/replay
		"TriggerHotkeyByKeySequence", // hotkeys by chord, not just by name
		"GetSceneItemSource",         //
		"SendStreamCaption",          //
		"GetCanvasList",              // the second canvas
	} {
		if _, ok := reg.entries[name]; !ok {
			t.Errorf("%s is not reachable through the registry", name)
		}
	}
}

func TestCallRequestRejectsAnUnknownRequestType(t *testing.T) {
	reg := newRequestRegistry()

	_, err := reg.lookup("NoSuchRequest")
	if err == nil {
		t.Fatal("expected an error for an unknown request type")
	}
	// An agent typing a request name from memory needs to know it was the name
	// that was wrong, not the arguments or the connection.
	if !strings.Contains(err.Error(), "NoSuchRequest") {
		t.Errorf("error does not name the request that was not found: %v", err)
	}
}

func TestDestructiveRequestsAreRefusedInFavourOfTheirWrappers(t *testing.T) {
	// The passthrough reaches every request, including the ones whose typed
	// tools exist precisely so a destructive act is confirmable. Letting them
	// through here would route around elicitation while looking like a feature.
	for request, wrapper := range map[string]string{
		"RemoveScene":               "remove_scene",
		"RemoveInput":               "remove_source",
		"RemoveSceneItem":           "remove_scene_item",
		"SetCurrentSceneCollection": "",
	} {
		err := checkRequestAllowed(request, nil, noTransport)
		if err == nil {
			t.Errorf("%s was allowed through the passthrough", request)
			continue
		}
		if wrapper != "" && !strings.Contains(err.Error(), wrapper) {
			t.Errorf("refusing %s should point at %s, said: %v", request, wrapper, err)
		}
	}
}

func TestOrdinaryRequestsAreNotRefused(t *testing.T) {
	// The deny-list has to be narrow. A list that grows by suspicion turns the
	// one tool that reaches everything into another tool that reaches some of it.
	for _, request := range []string{
		"GetStats", "GetVersion", "SetSceneItemTransform", "TriggerMediaInputAction",
		"SetCurrentProgramScene", "CreateSceneItem", "SetInputSettings",
	} {
		if err := checkRequestAllowed(request, nil, noTransport); err != nil {
			t.Errorf("%s should be allowed: %v", request, err)
		}
	}
}

// The Lua bridge's transport is a pair of sources addressed by name, and
// writing the inbox's settings runs the chunk they carry inside OBS. The
// deny-list above cannot see that: it keys on the request type, and
// SetInputSettings is the request set_source_settings wraps, so denying the
// type would cost the passthrough a legitimate use and still leave CreateInput,
// SetInputName and CreateSceneItem aimed at the same two names.
//
// These tests pin the target-based refusal. They fail against the deny-list
// alone: before it existed, every call below returned nil.

// noTransport stands in for "the bridge is not installed": no source carries a
// reserved name, so no uuid resolves to one.
func noTransport() (map[string]string, error) { return map[string]string{}, nil }

// TestEverySourceAddressingFieldMatchesTheKeyRule is what turns "the key rule is
// broad" into "the key rule is exhaustive for this dependency".
//
// CallRequest marshals request_data into goobs' generated params struct, and
// unknown fields are dropped there -- so the fields a caller can actually
// address anything through are exactly the ones goobs declares. Every one of
// them that mentions a name or a uuid is spelled *Name or *Uuid today, which is
// why addressesSource can be a suffix test rather than a table.
//
// That is a fact about goobs 1.8.3, not a law, so it is asserted over the whole
// generated surface instead of being trusted. A future goobs that adds
// sourceNames or inputNameList fails here, loudly, rather than leaving the
// guard quietly blind to it.
func TestEverySourceAddressingFieldMatchesTheKeyRule(t *testing.T) {
	reg := newRequestRegistry()
	checked := 0

	for request, entry := range reg.entries {
		for i := 0; i < entry.params.NumField(); i++ {
			field := entry.params.Field(i)
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			if key == "" || key == "-" {
				continue
			}
			checked++

			lower := strings.ToLower(key)
			mentions := strings.Contains(lower, "name") || strings.Contains(lower, "uuid")
			if _, addresses := addressesSource(key); mentions != addresses {
				t.Errorf("%s.%s: the field mentions a name or uuid but addressesSource says %v -- "+
					"goobs has introduced a spelling the guard's key rule does not match",
					request, key, addresses)
			}
		}
	}

	// goobs 1.8.3 declares exactly 300 tagged params fields across the 147
	// requests. A sharp lower bound for the same reason the registry test has
	// one: reflection that stopped finding fields would make this test pass by
	// inspecting nothing, which is the failure mode worth catching.
	if checked < 300 {
		t.Errorf("only %d params fields were inspected; the generated surface is much larger "+
			"than that, so this test is not looking at what it claims to", checked)
	}
}

func TestPassthroughRefusesRequestsAimedAtTheBridgeTransport(t *testing.T) {
	for _, name := range []string{BridgeInboxSource, BridgeMailboxSource} {
		for _, tc := range []struct {
			request string
			data    map[string]interface{}
			why     string
		}{
			{
				request: "SetInputSettings",
				data: map[string]interface{}{
					"inputName": name,
					"inputSettings": map[string]interface{}{
						"id": "1", "lua": "return 6 * 7",
					},
				},
				why: "the write that runs the chunk",
			},
			{
				request: "CreateInput",
				data:    map[string]interface{}{"sceneName": "Game", "inputName": name, "inputKind": "color_source_v3"},
				why:     "a decoy that collides with the real transport",
			},
			{
				request: "SetInputName",
				data:    map[string]interface{}{"inputName": name, "newInputName": "gone"},
				why:     "renaming it breaks the bridge with no error anywhere",
			},
			{
				request: "SetInputName",
				data:    map[string]interface{}{"inputName": "Webcam", "newInputName": name},
				why:     "renaming something else ONTO the reserved name",
			},
			{
				request: "CreateSceneItem",
				data:    map[string]interface{}{"sceneName": "Game", "sourceName": name},
				why:     "placing it in a live scene",
			},
			{
				request: "CreateSourceFilter",
				data: map[string]interface{}{
					"sourceName": name, "filterName": "f", "filterKind": "color_filter_v2",
				},
				why: "a write to the source by its other addressing field",
			},
			{
				request: "PressInputPropertiesButton",
				data:    map[string]interface{}{"inputName": name, "propertyName": "refresh"},
				why:     "a request this guard was never written for, refused because it is not a Get*",
			},
		} {
			err := checkRequestAllowed(tc.request, tc.data, noTransport)
			if err == nil {
				t.Errorf("%s aimed at %q was allowed through the passthrough (%s)", tc.request, name, tc.why)
				continue
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("refusing %s should name %q, said: %v", tc.request, name, err)
			}
			if !strings.Contains(err.Error(), "run_lua_in_obs") {
				t.Errorf("refusing %s should point at the gated tool, said: %v", tc.request, err)
			}
		}
	}
}

func TestPassthroughStillReachesOrdinarySources(t *testing.T) {
	// The guard has to be a target check, not a type check. Every one of these
	// is the same request the tests above refuse, aimed somewhere ordinary.
	for _, tc := range []struct {
		request string
		data    map[string]interface{}
	}{
		{"SetInputSettings", map[string]interface{}{
			"inputName":     "Webcam",
			"inputSettings": map[string]interface{}{"resolution": "1280x720"},
		}},
		{"CreateInput", map[string]interface{}{
			"sceneName": "Game", "inputName": "agentic-obs-inbox-2", "inputKind": "color_source_v3",
		}},
		{"SetInputName", map[string]interface{}{"inputName": "Webcam", "newInputName": "Cam"}},
		{"CreateSceneItem", map[string]interface{}{"sceneName": "Game", "sourceName": "Webcam"}},
		{"SetCurrentProgramScene", map[string]interface{}{"sceneName": "Game"}},
		{"TriggerMediaInputAction", map[string]interface{}{
			"inputName": "Stinger", "mediaAction": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_RESTART",
		}},
	} {
		if err := checkRequestAllowed(tc.request, tc.data, noTransport); err != nil {
			t.Errorf("%s should be allowed: %v", tc.request, err)
		}
	}
}

func TestPassthroughStillReadsTheBridgeTransport(t *testing.T) {
	// Reads stay open, and "read" is decided by the Get* prefix rather than by
	// a table. GetVersion with a nil payload is the call ADR-012's live
	// coverage test makes through this same path.
	for _, tc := range []struct {
		request string
		data    map[string]interface{}
	}{
		{"GetVersion", nil},
		{"GetStats", map[string]interface{}{}},
		{"GetInputSettings", map[string]interface{}{"inputName": BridgeInboxSource}},
		{"GetSourceActive", map[string]interface{}{"sourceName": BridgeMailboxSource}},
		{"GetInputList", map[string]interface{}{"inputKind": "color_source_v3"}},
	} {
		if err := checkRequestAllowed(tc.request, tc.data, noTransport); err != nil {
			t.Errorf("read %s should be allowed: %v", tc.request, err)
		}
	}
}

func TestPassthroughIgnoresTheTransportNameAsContent(t *testing.T) {
	// The precision boundary, stated as a test so it is a decision rather than
	// an accident: the guard reads source-addressing keys, not every string in
	// the payload. A text source whose text is the transport's name addresses
	// nothing, and refusing it would be an absurd false positive.
	for _, tc := range []struct {
		request string
		data    map[string]interface{}
	}{
		{"SetInputSettings", map[string]interface{}{
			"inputName":     "Chat overlay",
			"inputSettings": map[string]interface{}{"text": BridgeInboxSource},
		}},
		{"SetInputSettings", map[string]interface{}{
			"inputName":     "Browser",
			"inputSettings": map[string]interface{}{"url": "http://localhost/agentic-obs-inbox"},
		}},
	} {
		if err := checkRequestAllowed(tc.request, tc.data, noTransport); err != nil {
			t.Errorf("%s should be allowed: %v", tc.request, err)
		}
	}

	// The breadth half of the same decision: a *Name key is checked wherever it
	// sits, including inside a nested object, because that costs nothing.
	err := checkRequestAllowed("SetInputSettings", map[string]interface{}{
		"inputName": "Chat overlay",
		"inputSettings": map[string]interface{}{
			"target": map[string]interface{}{"sourceName": BridgeInboxSource},
		},
	}, noTransport)
	if err == nil {
		t.Error("a nested source-addressing key was not checked")
	}
}

func TestPassthroughRefusesTheBridgeTransportAddressedByUUID(t *testing.T) {
	// A name-only guard is one read away from being bypassed: GetInputList is a
	// read, it stays open, and it hands back the inbox's inputUuid. So the
	// reserved names are resolved to live uuids when -- and only when -- the
	// payload carries one.
	const inboxUUID = "9f1c2d33-4e55-4a6b-8c7d-0e1f2a3b4c5d"
	resolved := func() (map[string]string, error) {
		return map[string]string{inboxUUID: BridgeInboxSource}, nil
	}

	err := checkRequestAllowed("SetInputSettings", map[string]interface{}{
		"inputUuid":     inboxUUID,
		"inputSettings": map[string]interface{}{"id": "1", "lua": "return 6 * 7"},
	}, resolved)
	if err == nil {
		t.Fatal("the transport was reachable by uuid")
	}
	if !strings.Contains(err.Error(), BridgeInboxSource) {
		t.Errorf("a uuid refusal should name the source it resolved to, said: %v", err)
	}

	// Another source's uuid is not the transport's.
	if err := checkRequestAllowed("SetInputSettings", map[string]interface{}{
		"inputUuid":     "11111111-2222-3333-4444-555555555555",
		"inputSettings": map[string]interface{}{"resolution": "1280x720"},
	}, resolved); err != nil {
		t.Errorf("an ordinary source addressed by uuid should be allowed: %v", err)
	}
}

func TestPassthroughResolvesUUIDsOnlyWhenTheyAreUsed(t *testing.T) {
	// The lookup is a round trip to OBS. A payload with no uuid in it must not
	// pay for one, which is also what keeps the common path unchanged.
	calls := 0
	counting := func() (map[string]string, error) {
		calls++
		return map[string]string{}, nil
	}

	for _, tc := range []struct {
		request string
		data    map[string]interface{}
	}{
		{"SetInputSettings", map[string]interface{}{"inputName": "Webcam"}},
		{"GetInputSettings", map[string]interface{}{"inputUuid": "irrelevant, this is a read"}},
		{"SetCurrentProgramScene", map[string]interface{}{"sceneName": "Game"}},
	} {
		if err := checkRequestAllowed(tc.request, tc.data, counting); err != nil {
			t.Errorf("%s should be allowed: %v", tc.request, err)
		}
	}
	if calls != 0 {
		t.Errorf("the transport uuids were resolved %d times for payloads that address nothing by uuid", calls)
	}
}

func TestPassthroughReadsPluralAndListAddressingKeys(t *testing.T) {
	// goobs declares no plural addressing field today, so this covers a shape
	// rather than a request -- cheap insurance so that a future sourceNames is
	// caught by the guard as well as reported by
	// TestEverySourceAddressingFieldMatchesTheKeyRule.
	for _, data := range []map[string]interface{}{
		{"sourceNames": []interface{}{"Webcam", BridgeInboxSource}},
		{"inputUuids": []interface{}{"11111111-2222-3333-4444-555555555555"}},
	} {
		resolved := func() (map[string]string, error) {
			return map[string]string{"11111111-2222-3333-4444-555555555555": BridgeMailboxSource}, nil
		}
		if err := checkRequestAllowed("SetSomethingPlural", data, resolved); err == nil {
			t.Errorf("a reserved name in a list under %v was not seen", data)
		}
	}
}

func TestCallRequestOnADisconnectedClientSaysSo(t *testing.T) {
	// A uuid-bearing payload used to make the disconnected case report that the
	// bridge's uuids were unresolvable, which is a confusing answer to "OBS is
	// not running". No connection is no transport, not a failed lookup -- and
	// canvasUuid is declared on thirty-odd params types, so this is an ordinary
	// call rather than an exotic one.
	_, err := (&Client{}).CallRequest("SetSceneItemEnabled", map[string]interface{}{
		"sceneName":        "Game",
		"canvasUuid":       "11111111-2222-3333-4444-555555555555",
		"sceneItemId":      3,
		"sceneItemEnabled": true,
	})
	if err == nil {
		t.Fatal("expected an error from a disconnected client")
	}
	if !strings.Contains(err.Error(), "not connected to OBS") {
		t.Errorf("a disconnected client should say so, said: %v", err)
	}

	// The reservation still wins over the connection message, because it is
	// about the request rather than about OBS's availability.
	_, err = (&Client{}).CallRequest("SetInputSettings", map[string]interface{}{
		"inputName": BridgeInboxSource,
	})
	if err == nil || !strings.Contains(err.Error(), "run_lua_in_obs") {
		t.Errorf("a name-addressed refusal should not depend on the connection, said: %v", err)
	}
}

func TestCallRequestChecksTheTargetBeforeItTouchesOBS(t *testing.T) {
	// Through the exported entry point rather than the helper, and on a client
	// that is not connected: the refusal has to come from the guard, not from
	// the connection or from the registry lookup that follows it. A guard that
	// ran later would still refuse this request and would stop refusing one
	// whose type the registry does not know.
	_, err := (&Client{}).CallRequest("SetInputSettings", map[string]interface{}{
		"inputName":     BridgeInboxSource,
		"inputSettings": map[string]interface{}{"id": "1", "lua": "return 6 * 7"},
	})
	if err == nil {
		t.Fatal("CallRequest allowed a settings write to the transport")
	}
	if !strings.Contains(err.Error(), "run_lua_in_obs") {
		t.Errorf("the refusal should be the transport guard's, said: %v", err)
	}
}

func TestPassthroughFailsClosedWhenTheUUIDLookupFails(t *testing.T) {
	// Not knowing the transport's uuids means not knowing that this request
	// misses them. The lookup only fails when OBS is unreachable, in which case
	// the request itself was going to fail anyway.
	broken := func() (map[string]string, error) {
		return nil, errors.New("not connected to OBS")
	}

	err := checkRequestAllowed("SetInputSettings", map[string]interface{}{
		"inputUuid":     "9f1c2d33-4e55-4a6b-8c7d-0e1f2a3b4c5d",
		"inputSettings": map[string]interface{}{"lua": "return 1"},
	}, broken)
	if err == nil {
		t.Fatal("a uuid-addressed write was allowed while the transport uuids were unknown")
	}
	if !strings.Contains(err.Error(), "not connected to OBS") {
		t.Errorf("the refusal should carry why the lookup failed, said: %v", err)
	}
}
