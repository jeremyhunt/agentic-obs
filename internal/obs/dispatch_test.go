package obs

import (
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
		err := checkRequestAllowed(request)
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
		if err := checkRequestAllowed(request); err != nil {
			t.Errorf("%s should be allowed: %v", request, err)
		}
	}
}
