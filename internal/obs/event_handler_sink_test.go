package obs

import "testing"

// TestEventHandlerForwardsEveryEventType is the test that closes the loop FB-63
// opened.
//
// EventHandler is what mcp.Server registers, and it was reaching OBS through
// EventCallback -- an interface with one method per event kind. A kind with no
// method on that interface is dropped by the adapter, so translating a new event
// is not enough to deliver it: preview_scene_changed was translated by eventFrom
// and then went nowhere.
//
// Driving it as a sink means every kind arrives, including ones added later,
// which is the property worth having rather than a longer switch. (FB-65)
func TestEventHandlerForwardsEveryEventType(t *testing.T) {
	var got []EventType
	handler := NewEventHandler(func(et EventType, data map[string]interface{}) {
		got = append(got, et)
	})

	var sink EventSink = handler
	for _, et := range allEventTypes {
		sink.HandleEvent(Event{Type: et, Payload: map[string]interface{}{}})
	}

	if len(got) != len(allEventTypes) {
		t.Fatalf("forwarded %d of %d event types: %v", len(got), len(allEventTypes), got)
	}
	for i, want := range allEventTypes {
		if got[i] != want {
			t.Errorf("event %d forwarded as %q, want %q", i, got[i], want)
		}
	}
}

// TestEventHandlerPassesThePayloadThrough: the notification function receives
// the payload unchanged. The sink path exists partly to remove a translation --
// events used to be turned into typed callback arguments and then rebuilt into a
// map -- so this pins that nothing is lost on the way.
func TestEventHandlerPassesThePayloadThrough(t *testing.T) {
	var gotPayload map[string]interface{}
	handler := NewEventHandler(func(et EventType, data map[string]interface{}) {
		gotPayload = data
	})

	want := map[string]interface{}{"scene_name": "Starting Soon", "action": "preview_changed"}
	handler.HandleEvent(Event{Type: EventTypePreviewSceneChanged, Payload: want})

	if len(gotPayload) != len(want) {
		t.Fatalf("payload arrived as %#v, want %#v", gotPayload, want)
	}
	for k, v := range want {
		if gotPayload[k] != v {
			t.Errorf("payload[%q] is %#v, want %#v", k, gotPayload[k], v)
		}
	}
}

// TestEventHandlerWithNoNotificationFuncDoesNotPanic: the handler is
// constructible without one, and the event goroutine must survive that.
func TestEventHandlerWithNoNotificationFuncDoesNotPanic(t *testing.T) {
	NewEventHandler(nil).HandleEvent(Event{Type: EventTypeSceneCreated})
}
