package obs

import (
	"time"

	"github.com/andreykaipov/goobs/api/events"
)

// obs-websocket's settled output states. The intermediate STARTING and STOPPING
// states are deliberately not listed: acting on them would announce a recording
// that has not begun, or a stream that is still tearing down.
const (
	outputStarted = "OBS_WEBSOCKET_OUTPUT_STARTED"
	outputStopped = "OBS_WEBSOCKET_OUTPUT_STOPPED"
	outputPaused  = "OBS_WEBSOCKET_OUTPUT_PAUSED"
	outputResumed = "OBS_WEBSOCKET_OUTPUT_RESUMED"
)

// Event is one OBS event, flattened.
//
// It replaces dispatching straight into a 17-method callback interface. Adding
// an event kind used to mean widening that interface and updating every
// implementer, which is why obs-websocket categories this client subscribes to
// still have no handler. A sink switches on Type and ignores what it does not
// care about.
type Event struct {
	Type    EventType
	At      time.Time
	Payload map[string]interface{}
}

// EventSink receives translated OBS events.
type EventSink interface {
	HandleEvent(Event)
}

// eventFrom translates a goobs event into an Event, reporting false for events
// this client does not handle.
//
// It is a free function taking its timestamp rather than a method reading the
// clock, so the whole translation table is testable without a websocket. It
// previously lived inside a switch in a goroutine reading live events, which
// meant it could only be exercised by running OBS -- and so never was.
func eventFrom(raw any, at time.Time) (Event, bool) {
	ev := func(t EventType, payload map[string]interface{}) (Event, bool) {
		return Event{Type: t, At: at, Payload: payload}, true
	}
	none := func() (Event, bool) { return Event{}, false }

	switch e := raw.(type) {
	// Scene events
	case *events.SceneCreated:
		return ev(EventTypeSceneCreated, map[string]interface{}{
			"scene_name": e.SceneName, "action": "created",
		})

	case *events.SceneRemoved:
		return ev(EventTypeSceneRemoved, map[string]interface{}{
			"scene_name": e.SceneName, "action": "removed",
		})

	case *events.CurrentProgramSceneChanged:
		return ev(EventTypeSceneChanged, map[string]interface{}{
			"scene_name": e.SceneName, "action": "changed",
		})

	// Recording events
	case *events.RecordStateChanged:
		switch {
		case e.OutputActive && e.OutputState == outputStarted:
			return ev(EventTypeRecordingStarted, map[string]interface{}{})
		case !e.OutputActive && e.OutputState == outputStopped:
			return ev(EventTypeRecordingStopped, map[string]interface{}{
				"output_path": e.OutputPath,
			})
		case e.OutputState == outputPaused:
			return ev(EventTypeRecordingPaused, map[string]interface{}{})
		case e.OutputState == outputResumed:
			return ev(EventTypeRecordingResumed, map[string]interface{}{})
		}
		return none()

	case *events.RecordFileChanged:
		return ev(EventTypeRecordingFileChanged, map[string]interface{}{
			"new_output_path": e.NewOutputPath,
		})

	// Streaming events
	case *events.StreamStateChanged:
		switch {
		case e.OutputActive && e.OutputState == outputStarted:
			return ev(EventTypeStreamingStarted, map[string]interface{}{})
		case !e.OutputActive && e.OutputState == outputStopped:
			return ev(EventTypeStreamingStopped, map[string]interface{}{})
		}
		return none()

	// Virtual camera events
	case *events.VirtualcamStateChanged:
		switch {
		case e.OutputActive && e.OutputState == outputStarted:
			return ev(EventTypeVirtualCamStarted, map[string]interface{}{})
		case !e.OutputActive && e.OutputState == outputStopped:
			return ev(EventTypeVirtualCamStopped, map[string]interface{}{})
		}
		return none()

	// Replay buffer events
	case *events.ReplayBufferSaved:
		return ev(EventTypeReplayBufferSaved, map[string]interface{}{
			"saved_path": e.SavedReplayPath,
		})

	// Input events
	case *events.InputMuteStateChanged:
		return ev(EventTypeInputMuteChanged, map[string]interface{}{
			"input_name": e.InputName, "muted": e.InputMuted,
		})

	// Scene item events
	case *events.SceneItemEnableStateChanged:
		return ev(EventTypeSourceVisibilityChanged, map[string]interface{}{
			"scene_name":    e.SceneName,
			"scene_item_id": int(e.SceneItemId),
			"visible":       e.SceneItemEnabled,
		})

	// Transition events
	case *events.SceneTransitionStarted:
		return ev(EventTypeTransitionStarted, map[string]interface{}{
			"transition_name": e.TransitionName,
		})

	// Studio mode events
	case *events.StudioModeStateChanged:
		return ev(EventTypeStudioModeChanged, map[string]interface{}{
			"enabled": e.StudioModeEnabled,
		})

	case *events.CurrentPreviewSceneChanged:
		return ev(EventTypePreviewSceneChanged, map[string]interface{}{
			"scene_name": e.SceneName, "action": "preview_changed",
		})
	}

	return none()
}

// callbackSink adapts the older EventCallback interface to an EventSink, so a
// caller that registered a callback keeps working unchanged.
type callbackSink struct {
	cb EventCallback
}

func (s callbackSink) HandleEvent(e Event) {
	if s.cb == nil {
		return
	}

	str := func(key string) string {
		v, _ := e.Payload[key].(string)
		return v
	}
	boolean := func(key string) bool {
		v, _ := e.Payload[key].(bool)
		return v
	}

	switch e.Type {
	case EventTypeSceneCreated:
		s.cb.OnSceneCreated(str("scene_name"))
	case EventTypeSceneRemoved:
		s.cb.OnSceneRemoved(str("scene_name"))
	case EventTypeSceneChanged:
		s.cb.OnCurrentProgramSceneChanged(str("scene_name"))
	case EventTypeRecordingStarted:
		s.cb.OnRecordingStarted()
	case EventTypeRecordingStopped:
		s.cb.OnRecordingStopped(str("output_path"))
	case EventTypeRecordingPaused:
		s.cb.OnRecordingPaused()
	case EventTypeRecordingResumed:
		s.cb.OnRecordingResumed()
	case EventTypeRecordingFileChanged:
		s.cb.OnRecordingFileChanged(str("new_output_path"))
	case EventTypeStreamingStarted:
		s.cb.OnStreamingStarted()
	case EventTypeStreamingStopped:
		s.cb.OnStreamingStopped()
	case EventTypeVirtualCamStarted:
		s.cb.OnVirtualCamStarted()
	case EventTypeVirtualCamStopped:
		s.cb.OnVirtualCamStopped()
	case EventTypeReplayBufferSaved:
		s.cb.OnReplayBufferSaved(str("saved_path"))
	case EventTypeInputMuteChanged:
		s.cb.OnInputMuteChanged(str("input_name"), boolean("muted"))
	case EventTypeSourceVisibilityChanged:
		id, _ := e.Payload["scene_item_id"].(int)
		s.cb.OnSceneItemVisibilityChanged(str("scene_name"), id, boolean("visible"))
	case EventTypeTransitionStarted:
		s.cb.OnTransitionStarted(str("transition_name"))
	case EventTypeStudioModeChanged:
		s.cb.OnStudioModeChanged(boolean("enabled"))
	}
}
