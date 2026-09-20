package automation

import (
	"fmt"
	"log"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// OBSClient is what the executor needs from OBS.
//
// It used to be a hand-written near-copy of mcp.OBSClient, and the copy drifted:
// its "Source visibility" section carried a comment asserting obs-websocket had
// no way to set an item's visibility, long after SetSceneItemEnabled existed and
// was in use elsewhere in this repo. The executor toggled when asked to set
// because of that comment (FB-55).
//
// Composing roles instead means there is nothing to keep in step. The executor
// takes whole roles rather than individual methods, so it accepts a little more
// than it uses -- GetInputMute's siblings, say -- in exchange for a declaration
// that cannot drift from the client.
type OBSClient interface {
	obs.SceneWriter
	obs.SceneItemWriter
	obs.AudioController
	obs.RecordingController
	obs.StreamingController
	obs.VirtualCamController
	obs.ReplayBufferController
	obs.StudioController
	obs.TransitionController
	obs.HotkeyTrigger
	// VendorCaller is how a rule reaches a third-party plugin -- the one thing
	// an action can do that obs-websocket does not implement itself. (FB-78)
	obs.VendorCaller
	obs.EventSource
}

// Executor handles action execution against OBS.
type Executor struct {
	obsClient OBSClient
}

// NewExecutor creates a new action executor.
func NewExecutor(client OBSClient) *Executor {
	return &Executor{
		obsClient: client,
	}
}

// ExecuteAction runs a single action and returns the result.
func (e *Executor) ExecuteAction(action Action, index int) ActionResult {
	start := time.Now()
	result := ActionResult{
		ActionType: action.Type,
		Index:      index,
	}

	err := e.runAction(action)

	result.DurationMs = time.Since(start).Milliseconds()
	result.Success = err == nil
	if err != nil {
		result.Error = err.Error()
		log.Printf("[Automation] Action %d (%s) failed: %v", index, action.Type, err)
	} else {
		log.Printf("[Automation] Action %d (%s) completed in %dms", index, action.Type, result.DurationMs)
	}

	return result
}

// runAction dispatches to the appropriate handler based on action type.
func (e *Executor) runAction(action Action) error {
	switch action.Type {
	case ActionTypeSetScene:
		return e.setScene(action.Parameters)

	case ActionTypeToggleMute:
		return e.toggleMute(action.Parameters)

	case ActionTypeSetMute:
		return e.setMute(action.Parameters)

	case ActionTypeSetVolume:
		return e.setVolume(action.Parameters)

	case ActionTypeToggleVisibility:
		return e.toggleVisibility(action.Parameters)

	case ActionTypeSetVisibility:
		return e.setVisibility(action.Parameters)

	case ActionTypeStartRecording:
		return e.obsClient.StartRecording()

	case ActionTypeStopRecording:
		_, err := e.obsClient.StopRecording()
		return err

	case ActionTypePauseRecording:
		return e.obsClient.PauseRecording()

	case ActionTypeResumeRecording:
		return e.obsClient.ResumeRecording()

	case ActionTypeStartStreaming:
		return e.obsClient.StartStreaming()

	case ActionTypeStopStreaming:
		return e.obsClient.StopStreaming()

	case ActionTypeToggleVirtualCam:
		_, err := e.obsClient.ToggleVirtualCam()
		return err

	case ActionTypeStartVirtualCam:
		return e.obsClient.StartVirtualCam()

	case ActionTypeStopVirtualCam:
		return e.obsClient.StopVirtualCam()

	case ActionTypeToggleReplayBuffer:
		_, err := e.obsClient.ToggleReplayBuffer()
		return err

	case ActionTypeSaveReplay:
		return e.obsClient.SaveReplayBuffer()

	case ActionTypeTriggerHotkey:
		return e.triggerHotkey(action.Parameters)

	case ActionTypeTriggerTransition:
		return e.obsClient.TriggerStudioModeTransition()

	case ActionTypeSetPreviewScene:
		return e.setPreviewScene(action.Parameters)

	case ActionTypeDelay:
		return e.delay(action.Parameters)

	case ActionTypeCallVendorRequest:
		return e.callVendorRequest(action.Parameters)

	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// setScene switches to a scene.
func (e *Executor) setScene(params map[string]interface{}) error {
	sceneName, ok := getStringParam(params, "scene_name")
	if !ok {
		return fmt.Errorf("set_scene requires 'scene_name' parameter")
	}
	return e.obsClient.SetCurrentScene(sceneName)
}

// toggleMute toggles mute for an input.
func (e *Executor) toggleMute(params map[string]interface{}) error {
	inputName, ok := getStringParam(params, "input_name")
	if !ok {
		return fmt.Errorf("toggle_mute requires 'input_name' parameter")
	}
	return e.obsClient.ToggleInputMute(inputName)
}

// setMute sets the mute state for an input.
func (e *Executor) setMute(params map[string]interface{}) error {
	inputName, ok := getStringParam(params, "input_name")
	if !ok {
		return fmt.Errorf("set_mute requires 'input_name' parameter")
	}

	muted, ok := getBoolParam(params, "muted")
	if !ok {
		return fmt.Errorf("set_mute requires 'muted' parameter")
	}

	// Get current state
	currentMuted, err := e.obsClient.GetInputMute(inputName)
	if err != nil {
		return fmt.Errorf("failed to get current mute state: %w", err)
	}

	// Only toggle if state needs to change
	if currentMuted != muted {
		return e.obsClient.ToggleInputMute(inputName)
	}

	return nil
}

// setVolume sets volume for an input.
func (e *Executor) setVolume(params map[string]interface{}) error {
	inputName, ok := getStringParam(params, "input_name")
	if !ok {
		return fmt.Errorf("set_volume requires 'input_name' parameter")
	}

	volumeDb, hasDb := getFloat64Param(params, "volume_db")
	volumeMul, hasMul := getFloat64Param(params, "volume_mul")

	if !hasDb && !hasMul {
		return fmt.Errorf("set_volume requires either 'volume_db' or 'volume_mul' parameter")
	}

	var dbPtr, mulPtr *float64
	if hasDb {
		dbPtr = &volumeDb
	}
	if hasMul {
		mulPtr = &volumeMul
	}

	return e.obsClient.SetInputVolume(inputName, dbPtr, mulPtr)
}

// toggleVisibility toggles source visibility.
func (e *Executor) toggleVisibility(params map[string]interface{}) error {
	sceneName, ok := getStringParam(params, "scene_name")
	if !ok {
		return fmt.Errorf("toggle_visibility requires 'scene_name' parameter")
	}

	sourceID, ok := getIntParam(params, "source_id")
	if !ok {
		return fmt.Errorf("toggle_visibility requires 'source_id' parameter")
	}

	_, err := e.obsClient.ToggleSourceVisibility(sceneName, sourceID)
	return err
}

// setVisibility sets source visibility to an explicit state.
//
// This used to call toggleVisibility, under a comment claiming obs-websocket had
// no setter. It does, and internal/obs used it privately; it simply was not on
// the client interface. The effect was that a rule asking for visible=true
// flipped the item instead, so firing the rule twice hid what it was meant to
// show -- and any invariant built on it would oscillate by construction. (FB-55)
func (e *Executor) setVisibility(params map[string]interface{}) error {
	sceneName, ok := getStringParam(params, "scene_name")
	if !ok {
		return fmt.Errorf("set_visibility requires 'scene_name'")
	}

	sourceID, ok := getIntParam(params, "source_id")
	if !ok {
		return fmt.Errorf("set_visibility requires 'source_id'")
	}

	visible, ok := getBoolParam(params, "visible")
	if !ok {
		return fmt.Errorf("set_visibility requires 'visible'")
	}

	return e.obsClient.SetSceneItemEnabled(sceneName, sourceID, visible)
}

// triggerHotkey triggers a hotkey by name.
func (e *Executor) triggerHotkey(params map[string]interface{}) error {
	hotkeyName, ok := getStringParam(params, "hotkey_name")
	if !ok {
		return fmt.Errorf("trigger_hotkey requires 'hotkey_name' parameter")
	}
	return e.obsClient.TriggerHotkeyByName(hotkeyName)
}

// setPreviewScene sets the preview scene in studio mode.
func (e *Executor) setPreviewScene(params map[string]interface{}) error {
	sceneName, ok := getStringParam(params, "scene_name")
	if !ok {
		return fmt.Errorf("set_preview_scene requires 'scene_name' parameter")
	}
	return e.obsClient.SetCurrentPreviewScene(sceneName)
}

// delay pauses execution for the specified duration.
func (e *Executor) delay(params map[string]interface{}) error {
	delayMs, ok := getIntParam(params, "delay_ms")
	if !ok {
		return fmt.Errorf("delay requires 'delay_ms' parameter")
	}

	if delayMs < 0 {
		return fmt.Errorf("delay_ms must be non-negative")
	}

	// Cap delay at 5 minutes to prevent excessive waits
	const maxDelayMs = 5 * 60 * 1000
	if delayMs > maxDelayMs {
		log.Printf("[Automation] Capping delay from %dms to %dms", delayMs, maxDelayMs)
		delayMs = maxDelayMs
	}

	time.Sleep(time.Duration(delayMs) * time.Millisecond)
	return nil
}

// Parameter extraction helpers

func getStringParam(params map[string]interface{}, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	if v, ok := params[key].(string); ok {
		return v, true
	}
	return "", false
}

func getBoolParam(params map[string]interface{}, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	if v, ok := params[key].(bool); ok {
		return v, true
	}
	return false, false
}

func getFloat64Param(params map[string]interface{}, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	// JSON numbers are float64
	if v, ok := params[key].(float64); ok {
		return v, true
	}
	// Also handle int for convenience
	if v, ok := params[key].(int); ok {
		return float64(v), true
	}
	return 0, false
}

func getIntParam(params map[string]interface{}, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	// JSON numbers are float64
	if v, ok := params[key].(float64); ok {
		return int(v), true
	}
	// Also handle int
	if v, ok := params[key].(int); ok {
		return v, true
	}
	return 0, false
}

// callVendorRequest calls a request registered by a third-party plugin.
//
// The payload passes through untouched: a vendor's request shape is defined by
// the plugin that registers it, so anything this layer did to it would be a
// guess about someone else's schema. A failure fails the action rather than
// being logged and stepped over -- a rule that continued past a failed macro
// call would leave the operator believing it ran. (FB-78)
func (e *Executor) callVendorRequest(params map[string]interface{}) error {
	vendorName, ok := params["vendor_name"].(string)
	if !ok || vendorName == "" {
		return fmt.Errorf("call_vendor_request requires vendor_name")
	}

	requestType, ok := params["request_type"].(string)
	if !ok || requestType == "" {
		return fmt.Errorf("call_vendor_request requires request_type")
	}

	// Optional: plenty of vendor requests take no payload.
	data, _ := params["request_data"].(map[string]interface{})
	if data == nil {
		data = map[string]interface{}{}
	}

	if _, err := e.obsClient.CallVendorRequest(vendorName, requestType, data); err != nil {
		return fmt.Errorf("vendor request %s/%s failed: %w", vendorName, requestType, err)
	}
	return nil
}
