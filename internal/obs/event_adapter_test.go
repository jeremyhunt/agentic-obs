package obs

import (
	"fmt"
	"testing"
)

// recordingCallback implements the legacy EventCallback and records each call
// with its arguments, so the adapter's routing and its argument extraction are
// both observable.
type recordingCallback struct {
	calls []string
}

func (r *recordingCallback) OnSceneCreated(sceneName string) {
	r.calls = append(r.calls, "OnSceneCreated:"+sceneName)
}
func (r *recordingCallback) OnSceneRemoved(sceneName string) {
	r.calls = append(r.calls, "OnSceneRemoved:"+sceneName)
}
func (r *recordingCallback) OnCurrentProgramSceneChanged(sceneName string) {
	r.calls = append(r.calls, "OnCurrentProgramSceneChanged:"+sceneName)
}
func (r *recordingCallback) OnRecordingStarted() {
	r.calls = append(r.calls, "OnRecordingStarted")
}
func (r *recordingCallback) OnRecordingStopped(outputPath string) {
	r.calls = append(r.calls, "OnRecordingStopped:"+outputPath)
}
func (r *recordingCallback) OnRecordingPaused() {
	r.calls = append(r.calls, "OnRecordingPaused")
}
func (r *recordingCallback) OnRecordingResumed() {
	r.calls = append(r.calls, "OnRecordingResumed")
}
func (r *recordingCallback) OnRecordingFileChanged(newOutputPath string) {
	r.calls = append(r.calls, "OnRecordingFileChanged:"+newOutputPath)
}
func (r *recordingCallback) OnStreamingStarted() {
	r.calls = append(r.calls, "OnStreamingStarted")
}
func (r *recordingCallback) OnStreamingStopped() {
	r.calls = append(r.calls, "OnStreamingStopped")
}
func (r *recordingCallback) OnVirtualCamStarted() {
	r.calls = append(r.calls, "OnVirtualCamStarted")
}
func (r *recordingCallback) OnVirtualCamStopped() {
	r.calls = append(r.calls, "OnVirtualCamStopped")
}
func (r *recordingCallback) OnReplayBufferSaved(savedPath string) {
	r.calls = append(r.calls, "OnReplayBufferSaved:"+savedPath)
}
func (r *recordingCallback) OnInputMuteChanged(inputName string, muted bool) {
	r.calls = append(r.calls, fmt.Sprintf("OnInputMuteChanged:%s:%v", inputName, muted))
}
func (r *recordingCallback) OnSceneItemVisibilityChanged(sceneName string, sceneItemId int, visible bool) {
	r.calls = append(r.calls, fmt.Sprintf("OnSceneItemVisibilityChanged:%s:%d:%v", sceneName, sceneItemId, visible))
}
func (r *recordingCallback) OnTransitionStarted(transitionName string) {
	r.calls = append(r.calls, "OnTransitionStarted:"+transitionName)
}
func (r *recordingCallback) OnStudioModeChanged(enabled bool) {
	r.calls = append(r.calls, fmt.Sprintf("OnStudioModeChanged:%v", enabled))
}

// TestCallbackSinkRoutesEveryEventType is the compatibility guarantee. Existing
// callers registered an EventCallback and must keep receiving exactly what they
// received before the sink existed -- including the payload values, which the
// adapter has to dig back out of a map. A wrong key there is silent: the
// argument arrives as the zero value and the call still happens. (FB-63)
func TestCallbackSinkRoutesEveryEventType(t *testing.T) {
	tests := []struct {
		event Event
		want  string
	}{
		{Event{Type: EventTypeSceneCreated, Payload: map[string]interface{}{"scene_name": "Game"}}, "OnSceneCreated:Game"},
		{Event{Type: EventTypeSceneRemoved, Payload: map[string]interface{}{"scene_name": "Game"}}, "OnSceneRemoved:Game"},
		{Event{Type: EventTypeSceneChanged, Payload: map[string]interface{}{"scene_name": "Game"}}, "OnCurrentProgramSceneChanged:Game"},
		{Event{Type: EventTypeRecordingStarted, Payload: map[string]interface{}{}}, "OnRecordingStarted"},
		{Event{Type: EventTypeRecordingStopped, Payload: map[string]interface{}{"output_path": "C:/v.mkv"}}, "OnRecordingStopped:C:/v.mkv"},
		{Event{Type: EventTypeRecordingPaused, Payload: map[string]interface{}{}}, "OnRecordingPaused"},
		{Event{Type: EventTypeRecordingResumed, Payload: map[string]interface{}{}}, "OnRecordingResumed"},
		{Event{Type: EventTypeRecordingFileChanged, Payload: map[string]interface{}{"new_output_path": "C:/v2.mkv"}}, "OnRecordingFileChanged:C:/v2.mkv"},
		{Event{Type: EventTypeStreamingStarted, Payload: map[string]interface{}{}}, "OnStreamingStarted"},
		{Event{Type: EventTypeStreamingStopped, Payload: map[string]interface{}{}}, "OnStreamingStopped"},
		{Event{Type: EventTypeVirtualCamStarted, Payload: map[string]interface{}{}}, "OnVirtualCamStarted"},
		{Event{Type: EventTypeVirtualCamStopped, Payload: map[string]interface{}{}}, "OnVirtualCamStopped"},
		{Event{Type: EventTypeReplayBufferSaved, Payload: map[string]interface{}{"saved_path": "C:/r.mkv"}}, "OnReplayBufferSaved:C:/r.mkv"},
		{Event{Type: EventTypeInputMuteChanged, Payload: map[string]interface{}{"input_name": "Mic", "muted": true}}, "OnInputMuteChanged:Mic:true"},
		{Event{Type: EventTypeSourceVisibilityChanged, Payload: map[string]interface{}{"scene_name": "Game", "scene_item_id": 7, "visible": true}}, "OnSceneItemVisibilityChanged:Game:7:true"},
		{Event{Type: EventTypeTransitionStarted, Payload: map[string]interface{}{"transition_name": "Fade"}}, "OnTransitionStarted:Fade"},
		{Event{Type: EventTypeStudioModeChanged, Payload: map[string]interface{}{"enabled": true}}, "OnStudioModeChanged:true"},
	}

	for _, tc := range tests {
		t.Run(string(tc.event.Type), func(t *testing.T) {
			rec := &recordingCallback{}
			callbackSink{cb: rec}.HandleEvent(tc.event)

			if len(rec.calls) != 1 {
				t.Fatalf("expected exactly one callback, got %v", rec.calls)
			}
			if rec.calls[0] != tc.want {
				t.Errorf("called %q, want %q", rec.calls[0], tc.want)
			}
		})
	}
}

// TestCallbackSinkTolerksUnknownAndNil covers the paths that would otherwise
// crash the event goroutine, taking every later event with it.
func TestCallbackSinkToleratesUnknownAndNil(t *testing.T) {
	rec := &recordingCallback{}

	callbackSink{cb: rec}.HandleEvent(Event{Type: "something_new", Payload: map[string]interface{}{}})
	if len(rec.calls) != 0 {
		t.Errorf("an unknown event type produced %v", rec.calls)
	}

	// A nil callback must not panic: SetEventCallback(nil) is a legitimate way
	// to stop listening.
	callbackSink{cb: nil}.HandleEvent(Event{Type: EventTypeSceneCreated})

	// A payload missing its keys must not panic either. The event still
	// dispatches, with zero values -- which is why the routing test above
	// asserts on arguments rather than just on which method fired.
	callbackSink{cb: rec}.HandleEvent(Event{Type: EventTypeSceneCreated, Payload: nil})
	if len(rec.calls) != 1 || rec.calls[0] != "OnSceneCreated:" {
		t.Errorf("nil payload gave %v, want one call with an empty name", rec.calls)
	}
}

// TestSetEventCallbackAndSinkAreExclusive pins the client wiring: whichever was
// registered last wins, and nil clears.
func TestSetEventCallbackAndSinkAreExclusive(t *testing.T) {
	c := NewClient(ConnectionConfig{Host: "localhost", Port: "4455"})

	if c.eventSink != nil {
		t.Error("a new client should have no sink")
	}

	rec := &recordingCallback{}
	c.SetEventCallback(rec)
	if c.eventSink == nil {
		t.Fatal("SetEventCallback left no sink; events would be dropped")
	}

	// The adapter must be live, not merely present.
	c.eventSink.HandleEvent(Event{Type: EventTypeSceneCreated, Payload: map[string]interface{}{"scene_name": "Game"}})
	if len(rec.calls) != 1 {
		t.Errorf("the registered callback received %v", rec.calls)
	}

	c.SetEventCallback(nil)
	if c.eventSink != nil {
		t.Error("SetEventCallback(nil) should clear the sink, not install an adapter around nil")
	}
}
