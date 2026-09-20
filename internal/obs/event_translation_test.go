package obs

import (
	"reflect"
	"testing"
	"time"

	"github.com/andreykaipov/goobs/api/events"
)

// at is a fixed timestamp so the translation stays a pure function of its input.
var at = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// TestEventFromTranslatesEveryHandledEvent covers the goobs-to-Event table.
//
// This logic had no test at all: it lived inside a switch in the middle of a
// goroutine reading a live websocket, so exercising it meant running OBS. The
// four-way RecordStateChanged branch in particular decides which of four
// notifications a client sees, entirely on string comparison against
// obs-websocket's state constants, and a typo in one of those strings would
// simply mean that notification never fires. (FB-63)
func TestEventFromTranslatesEveryHandledEvent(t *testing.T) {
	tests := []struct {
		name    string
		raw     any
		want    EventType
		payload map[string]interface{}
	}{
		{
			name:    "scene created",
			raw:     &events.SceneCreated{SceneName: "Starting Soon"},
			want:    EventTypeSceneCreated,
			payload: map[string]interface{}{"scene_name": "Starting Soon", "action": "created"},
		},
		{
			name:    "scene removed",
			raw:     &events.SceneRemoved{SceneName: "Starting Soon"},
			want:    EventTypeSceneRemoved,
			payload: map[string]interface{}{"scene_name": "Starting Soon", "action": "removed"},
		},
		{
			name:    "current program scene changed",
			raw:     &events.CurrentProgramSceneChanged{SceneName: "Game"},
			want:    EventTypeSceneChanged,
			payload: map[string]interface{}{"scene_name": "Game", "action": "changed"},
		},
		{
			name:    "recording started",
			raw:     &events.RecordStateChanged{OutputActive: true, OutputState: "OBS_WEBSOCKET_OUTPUT_STARTED"},
			want:    EventTypeRecordingStarted,
			payload: map[string]interface{}{},
		},
		{
			name:    "recording stopped carries the output path",
			raw:     &events.RecordStateChanged{OutputActive: false, OutputState: "OBS_WEBSOCKET_OUTPUT_STOPPED", OutputPath: "C:/vod.mkv"},
			want:    EventTypeRecordingStopped,
			payload: map[string]interface{}{"output_path": "C:/vod.mkv"},
		},
		{
			name:    "recording paused",
			raw:     &events.RecordStateChanged{OutputActive: true, OutputState: "OBS_WEBSOCKET_OUTPUT_PAUSED"},
			want:    EventTypeRecordingPaused,
			payload: map[string]interface{}{},
		},
		{
			name:    "recording resumed",
			raw:     &events.RecordStateChanged{OutputActive: true, OutputState: "OBS_WEBSOCKET_OUTPUT_RESUMED"},
			want:    EventTypeRecordingResumed,
			payload: map[string]interface{}{},
		},
		{
			name:    "recording file changed",
			raw:     &events.RecordFileChanged{NewOutputPath: "C:/vod-002.mkv"},
			want:    EventTypeRecordingFileChanged,
			payload: map[string]interface{}{"new_output_path": "C:/vod-002.mkv"},
		},
		{
			name:    "streaming started",
			raw:     &events.StreamStateChanged{OutputActive: true, OutputState: "OBS_WEBSOCKET_OUTPUT_STARTED"},
			want:    EventTypeStreamingStarted,
			payload: map[string]interface{}{},
		},
		{
			name:    "streaming stopped",
			raw:     &events.StreamStateChanged{OutputActive: false, OutputState: "OBS_WEBSOCKET_OUTPUT_STOPPED"},
			want:    EventTypeStreamingStopped,
			payload: map[string]interface{}{},
		},
		{
			name:    "virtual cam started",
			raw:     &events.VirtualcamStateChanged{OutputActive: true, OutputState: "OBS_WEBSOCKET_OUTPUT_STARTED"},
			want:    EventTypeVirtualCamStarted,
			payload: map[string]interface{}{},
		},
		{
			name:    "virtual cam stopped",
			raw:     &events.VirtualcamStateChanged{OutputActive: false, OutputState: "OBS_WEBSOCKET_OUTPUT_STOPPED"},
			want:    EventTypeVirtualCamStopped,
			payload: map[string]interface{}{},
		},
		{
			name:    "replay buffer saved",
			raw:     &events.ReplayBufferSaved{SavedReplayPath: "C:/replay.mkv"},
			want:    EventTypeReplayBufferSaved,
			payload: map[string]interface{}{"saved_path": "C:/replay.mkv"},
		},
		{
			name:    "input mute changed",
			raw:     &events.InputMuteStateChanged{InputName: "Microphone", InputMuted: true},
			want:    EventTypeInputMuteChanged,
			payload: map[string]interface{}{"input_name": "Microphone", "muted": true},
		},
		{
			name: "scene item visibility changed",
			raw:  &events.SceneItemEnableStateChanged{SceneName: "Game", SceneItemId: 7, SceneItemEnabled: true},
			want: EventTypeSourceVisibilityChanged,
			payload: map[string]interface{}{
				"scene_name": "Game", "scene_item_id": 7, "visible": true,
			},
		},
		{
			name:    "transition started",
			raw:     &events.SceneTransitionStarted{TransitionName: "Fade"},
			want:    EventTypeTransitionStarted,
			payload: map[string]interface{}{"transition_name": "Fade"},
		},
		{
			name:    "studio mode changed",
			raw:     &events.StudioModeStateChanged{StudioModeEnabled: true},
			want:    EventTypeStudioModeChanged,
			payload: map[string]interface{}{"enabled": true},
		},
		{
			// The only inbound channel from a plugin. FB-62 subscribed to the
			// Vendors category and the events have been arriving and being
			// dropped ever since; this is the handler they were waiting for.
			// (FB-77)
			name: "vendor event",
			raw: &events.VendorEvent{
				VendorName: "AdvancedSceneSwitcher",
				EventType:  "MacroRun",
				EventData:  map[string]any{"macro": "PersonaShow_Sonic"},
			},
			want: EventTypeVendorEvent,
			payload: map[string]interface{}{
				"vendor_name": "AdvancedSceneSwitcher",
				"event_type":  "MacroRun",
				"event_data":  map[string]any{"macro": "PersonaShow_Sonic"},
			},
		},
		{
			// In studio mode the preview scene is what goes live on the next
			// transition, so "which scene is queued" is real state an agent can
			// act on. Nothing in this repo heard about it changing. (FB-65)
			name:    "preview scene changed",
			raw:     &events.CurrentPreviewSceneChanged{SceneName: "Starting Soon"},
			want:    EventTypePreviewSceneChanged,
			payload: map[string]interface{}{"scene_name": "Starting Soon", "action": "preview_changed"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := eventFrom(tc.raw, at)
			if !ok {
				t.Fatalf("eventFrom did not recognise %T", tc.raw)
			}
			if got.Type != tc.want {
				t.Errorf("type is %q, want %q", got.Type, tc.want)
			}
			if !got.At.Equal(at) {
				t.Errorf("At is %v, want %v", got.At, at)
			}
			if !reflect.DeepEqual(got.Payload, tc.payload) {
				t.Errorf("payload is %#v, want %#v", got.Payload, tc.payload)
			}
		})
	}
}

// TestEventFromIgnoresUnhandledEvents pins the contract for everything else.
// FB-62 widened the subscription mask to Filters and Vendors ahead of their
// handlers, so those events genuinely do arrive now and must be dropped quietly
// rather than producing an Event with an empty type that a sink would then try
// to format.
func TestEventFromIgnoresUnhandledEvents(t *testing.T) {
	for _, raw := range []any{
		&events.SourceFilterEnableStateChanged{},
		nil,
	} {
		if got, ok := eventFrom(raw, at); ok {
			t.Errorf("%T was translated to %q; it has no handler yet", raw, got.Type)
		}
	}
}

// TestRecordStateChangedIgnoresIntermediateStates is the branch most likely to
// be got wrong by a reader skimming the switch. obs-websocket emits STARTING
// and STOPPING either side of the states we act on, and treating STARTING as
// "started" would fire a notification for a recording that has not begun.
func TestRecordStateChangedIgnoresIntermediateStates(t *testing.T) {
	for _, state := range []string{"OBS_WEBSOCKET_OUTPUT_STARTING", "OBS_WEBSOCKET_OUTPUT_STOPPING"} {
		if _, ok := eventFrom(&events.RecordStateChanged{OutputState: state}, at); ok {
			t.Errorf("%s produced an event; only settled states are reported", state)
		}
	}
}
