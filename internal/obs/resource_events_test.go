package obs

import "testing"

// allEventTypes is every EventType this package defines. It is hand-listed
// because Go cannot enumerate constants, and that is useful here: adding an
// event kind forces a decision about whether it changes a published resource,
// rather than letting the answer default to "no" unnoticed.
var allEventTypes = []EventType{
	EventTypeSceneCreated,
	EventTypeSceneRemoved,
	EventTypeSceneChanged,
	EventTypeRecordingStarted,
	EventTypeRecordingStopped,
	EventTypeRecordingPaused,
	EventTypeRecordingResumed,
	EventTypeRecordingFileChanged,
	EventTypeStreamingStarted,
	EventTypeStreamingStopped,
	EventTypeVirtualCamStarted,
	EventTypeVirtualCamStopped,
	EventTypeReplayBufferSaved,
	EventTypeInputMuteChanged,
	EventTypeSourceVisibilityChanged,
	EventTypeTransitionStarted,
	EventTypeStudioModeChanged,
}

// TestOnlyResourceAffectingEventsTriggerUpdates states the rule exactly, in both
// directions: an event triggers a resources/updated notification if and only if
// it changes what obs://scene/{name} serialises.
//
// The "only if" half is the one worth guarding. A notification for a resource
// whose content has not changed is not harmless -- the client re-reads, finds
// the same bytes, and learns to trust the signal less.
//
// Filter events are the live example. FB-62 subscribed to them, and the plan
// called for mapping them here, but obs://scene/{name} publishes id, name, type,
// enabled, visible, locked and the transform -- no filter state at all. So a
// filter change cannot alter that resource, and notifying on it would be noise.
// Mapping them becomes correct only if the resource is enriched to carry
// filters, which the plan's own review rejected on round-trip cost. (FB-63)
func TestOnlyResourceAffectingEventsTriggerUpdates(t *testing.T) {
	triggers := map[EventType]bool{
		// Switching scenes changes which scene is current.
		EventTypeSceneChanged: true,
		// Hiding or showing an item changes that item's "visible" and "enabled"
		// in the scene's own representation.
		EventTypeSourceVisibilityChanged: true,
	}

	for _, et := range allEventTypes {
		want := triggers[et]
		if got := ShouldTriggerResourceUpdated(et); got != want {
			if want {
				t.Errorf("%s should trigger a resource update; it changes obs://scene/{name}", et)
			} else {
				t.Errorf("%s triggers a resource update but does not change obs://scene/{name}; "+
					"notifying on it teaches clients the signal is unreliable", et)
			}
		}
	}
}

// TestUnknownEventTypesDoNotTriggerUpdates covers the default branch. Events
// arrive from a subscription mask deliberately wider than the set translated
// here, so an unrecognised type is routine rather than exceptional.
func TestUnknownEventTypesDoNotTriggerUpdates(t *testing.T) {
	for _, et := range []EventType{"", "source_filter_enabled", "vendor_event", "not_a_real_event"} {
		if ShouldTriggerResourceUpdated(et) {
			t.Errorf("unrecognised event %q triggered a resource update", et)
		}
	}
}
