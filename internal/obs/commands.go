package obs

import (
	"fmt"
	"strings"

	"github.com/andreykaipov/goobs/api/requests/filters"
	"github.com/andreykaipov/goobs/api/requests/general"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/andreykaipov/goobs/api/requests/sources"
	"github.com/andreykaipov/goobs/api/requests/transitions"
	"github.com/andreykaipov/goobs/api/requests/ui"
	"github.com/andreykaipov/goobs/api/typedefs"
)

// Scene represents an OBS scene with its sources.
type Scene struct {
	Name    string        `json:"name"`
	Index   int           `json:"index"`
	Sources []SceneSource `json:"sources"`
}

// SceneSource represents a source within a scene.
type SceneSource struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Enabled  bool    `json:"enabled"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	ScaleX   float64 `json:"scale_x"`
	ScaleY   float64 `json:"scale_y"`
	Rotation float64 `json:"rotation"`
	Visible  bool    `json:"visible"`
	Locked   bool    `json:"locked"`

	// IsGroup separates a group from a nested scene. Both report Type
	// "OBS_SOURCE_TYPE_SCENE", because groups in OBS are scenes wearing a flag,
	// so this is the only field that tells them apart -- and they need
	// different requests to read their contents.
	IsGroup bool `json:"is_group"`
}

// RecordingStatus represents the current recording state.
type RecordingStatus struct {
	Active      bool   `json:"active"`
	Paused      bool   `json:"paused"`
	Timecode    string `json:"timecode,omitempty"`
	OutputPath  string `json:"output_path,omitempty"`
	OutputBytes int64  `json:"output_bytes,omitempty"`
}

// StreamingStatus represents the current streaming state.
type StreamingStatus struct {
	Active       bool   `json:"active"`
	Reconnecting bool   `json:"reconnecting"`
	Timecode     string `json:"timecode,omitempty"`
	TotalBytes   int64  `json:"total_bytes,omitempty"`
	TotalFrames  int    `json:"total_frames,omitempty"`
}

// GetSceneList retrieves all scenes from OBS.
// Returns a list of scene names and the currently active scene.
func (c *Client) GetSceneList() ([]string, string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, "", err
	}

	resp, err := client.Scenes.GetSceneList()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get scene list from OBS: %w", err)
	}

	sceneNames := make([]string, len(resp.Scenes))
	for i, scene := range resp.Scenes {
		sceneNames[i] = scene.SceneName
	}

	return sceneNames, resp.CurrentProgramSceneName, nil
}

// GetSceneByName retrieves detailed information about a specific scene, including its sources.
func (c *Client) GetSceneByName(name string) (*Scene, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	// Get scene item list
	resp, err := client.SceneItems.GetSceneItemList(&sceneitems.GetSceneItemListParams{
		SceneName: &name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get scene '%s' from OBS: %w", name, err)
	}

	scene := &Scene{
		Name:    name,
		Sources: make([]SceneSource, 0, len(resp.SceneItems)),
	}

	// Convert scene items to our SceneSource format
	for _, item := range resp.SceneItems {
		scene.Sources = append(scene.Sources, sceneSourceFromItem(*item))
	}

	return scene, nil
}

// sceneSourceFromItem converts one goobs scene item into the shape this package
// exposes.
//
// It is a free function rather than inline in GetSceneByName so it can be tested
// without a websocket connection: the conversion is where fields get dropped,
// and nothing that needs a live OBS to exercise will be covered in CI.
// GetGroupList returns the names of every group.
//
// Groups are absent from GetSceneList and from GetInputList, so without this
// call a group is invisible to anything enumerating a collection even though it
// is placed in scenes like any other source.
func (c *Client) GetGroupList() ([]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Scenes.GetGroupList()
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}
	if resp.Groups == nil {
		return []string{}, nil
	}
	return resp.Groups, nil
}

// GetGroupSceneItemList returns the items inside a group.
//
// This is not interchangeable with GetSceneByName. obs-websocket refuses each
// call for the other's argument with InvalidResourceType (602) -- "The
// specified source is not a scene. (Is group)" and "The specified source is not
// a group. (Is scene)" -- so a caller walking a collection must dispatch on
// SceneSource.IsGroup rather than try one and fall back to the other.
func (c *Client) GetGroupSceneItemList(groupName string) ([]SceneSource, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.SceneItems.GetGroupSceneItemList(&sceneitems.GetGroupSceneItemListParams{
		SceneName: &groupName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list items in group '%s': %w", groupName, err)
	}

	out := make([]SceneSource, 0, len(resp.SceneItems))
	for _, item := range resp.SceneItems {
		out = append(out, sceneSourceFromItem(*item))
	}
	return out, nil
}

func sceneSourceFromItem(item typedefs.SceneItem) SceneSource {
	source := SceneSource{
		ID:      int(item.SceneItemID),
		Name:    item.SourceName,
		Type:    item.SourceType,
		Enabled: item.SceneItemEnabled,
		Locked:  item.SceneItemLocked,
		// obs-websocket has a single notion of an item showing --
		// sceneItemEnabled -- so Visible is the same fact under the name the
		// obs://scene/{name} resource publishes it as. It was never populated,
		// which made that resource report every source as hidden. (FB-60)
		Visible: item.SceneItemEnabled,
		// A group and a nested scene both report SourceType
		// "OBS_SOURCE_TYPE_SCENE", so this flag is the only thing that tells a
		// caller which of the two it is holding -- and they need different
		// requests to read their contents.
		IsGroup: item.IsGroup,
	}

	// Extract transform information (always available as a struct)
	transform := item.SceneItemTransform
	source.X = transform.PositionX
	source.Y = transform.PositionY
	source.Width = transform.Width
	source.Height = transform.Height
	source.ScaleX = transform.ScaleX
	source.ScaleY = transform.ScaleY
	source.Rotation = transform.Rotation

	return source
}

// SetCurrentScene switches the active scene in OBS.
func (c *Client) SetCurrentScene(name string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Scenes.SetCurrentProgramScene(&scenes.SetCurrentProgramSceneParams{
		SceneName: &name,
	})
	if err != nil {
		return fmt.Errorf("failed to set current scene to '%s': %w. Scene may not exist", name, err)
	}

	return nil
}

// CreateScene creates a new scene in OBS.
func (c *Client) CreateScene(name string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	sceneName := name
	_, err = client.Scenes.CreateScene(&scenes.CreateSceneParams{
		SceneName: &sceneName,
	})
	if err != nil {
		return fmt.Errorf("failed to create scene '%s': %w. Scene may already exist", name, err)
	}

	return nil
}

// RemoveScene deletes a scene from OBS.
func (c *Client) RemoveScene(name string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	sceneName := name
	_, err = client.Scenes.RemoveScene(&scenes.RemoveSceneParams{
		SceneName: &sceneName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove scene '%s': %w. Scene may not exist or may be the only scene", name, err)
	}

	return nil
}

// StartRecording begins recording in OBS.
func (c *Client) StartRecording() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Record.StartRecord()
	if err != nil {
		return fmt.Errorf("failed to start recording: %w. Check OBS recording settings and output path", err)
	}

	return nil
}

// StopRecording stops the current recording in OBS.
func (c *Client) StopRecording() (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	resp, err := client.Record.StopRecord()
	if err != nil {
		return "", fmt.Errorf("failed to stop recording: %w. Recording may not be active", err)
	}

	return resp.OutputPath, nil
}

// GetRecordingStatus retrieves the current recording status from OBS.
func (c *Client) GetRecordingStatus() (*RecordingStatus, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Record.GetRecordStatus()
	if err != nil {
		return nil, fmt.Errorf("failed to get recording status: %w", err)
	}

	status := &RecordingStatus{
		Active:      resp.OutputActive,
		Paused:      resp.OutputPaused,
		Timecode:    resp.OutputTimecode,
		OutputBytes: int64(resp.OutputBytes),
	}

	return status, nil
}

// PauseRecording pauses the current recording.
func (c *Client) PauseRecording() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Record.PauseRecord()
	if err != nil {
		return fmt.Errorf("failed to pause recording: %w. Recording may not be active", err)
	}

	return nil
}

// ResumeRecording resumes a paused recording.
func (c *Client) ResumeRecording() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Record.ResumeRecord()
	if err != nil {
		return fmt.Errorf("failed to resume recording: %w. Recording may not be paused", err)
	}

	return nil
}

// StartStreaming begins streaming in OBS.
func (c *Client) StartStreaming() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Stream.StartStream()
	if err != nil {
		return fmt.Errorf("failed to start streaming: %w. Check OBS stream settings and credentials", err)
	}

	return nil
}

// StopStreaming stops the current stream in OBS.
func (c *Client) StopStreaming() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Stream.StopStream()
	if err != nil {
		return fmt.Errorf("failed to stop streaming: %w. Stream may not be active", err)
	}

	return nil
}

// GetStreamingStatus retrieves the current streaming status from OBS.
func (c *Client) GetStreamingStatus() (*StreamingStatus, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Stream.GetStreamStatus()
	if err != nil {
		return nil, fmt.Errorf("failed to get streaming status: %w", err)
	}

	status := &StreamingStatus{
		Active:       resp.OutputActive,
		Reconnecting: resp.OutputReconnecting,
		Timecode:     resp.OutputTimecode,
		TotalBytes:   int64(resp.OutputBytes),
	}

	return status, nil
}

// ListSources retrieves all input sources available in OBS.
func (c *Client) ListSources() ([]*typedefs.Input, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Inputs.GetInputList(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list sources from OBS: %w", err)
	}

	return resp.Inputs, nil
}

// GetSourceSettings retrieves the settings for a specific source.
func (c *Client) GetSourceSettings(sourceName string) (map[string]interface{}, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	inputName := sourceName
	resp, err := client.Inputs.GetInputSettings(&inputs.GetInputSettingsParams{
		InputName: &inputName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get settings for source '%s': %w. Source may not exist", sourceName, err)
	}

	return resp.InputSettings, nil
}

// SetSourceSettings writes a source's own settings.
//
// overlay=true merges into what is already there; overlay=false resets the input
// to its kind's defaults and then applies, so any key left out reverts rather
// than persisting. Use false when the caller intends the settings to *be* the
// given map -- applying a stored scene spec, for one -- and true for a
// single-field change.
//
// This is the request every OBS script in the workspace reaches past
// agentic-obs to make. (FB-67)
func (c *Client) SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	inputName := sourceName
	_, err = client.Inputs.SetInputSettings(&inputs.SetInputSettingsParams{
		InputName:     &inputName,
		InputSettings: settings,
		Overlay:       &overlay,
	})
	if err != nil {
		return fmt.Errorf("failed to set settings for source '%s': %w. Source may not exist", sourceName, err)
	}

	return nil
}

// GetInputDefaultSettings reports the default settings for an input kind.
//
// Two uses. It is how a caller discovers what an unfamiliar kind can be
// configured with, without guessing key names; and it is what stops a diff
// reporting drift for a setting that merely equals its default.
func (c *Client) GetInputDefaultSettings(inputKind string) (map[string]interface{}, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	kind := inputKind
	resp, err := client.Inputs.GetInputDefaultSettings(&inputs.GetInputDefaultSettingsParams{
		InputKind: &kind,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get default settings for input kind '%s': %w. "+
			"Check the kind against list_input_kinds", inputKind, err)
	}

	return resp.DefaultInputSettings, nil
}

// PressInputPropertiesButton presses a button on a source's properties dialog.
//
// Some source behaviour is reachable only this way: a browser source's cache-
// busting reload is the button "refreshnocache", and pressing it mutates no
// stored settings, which is why it is preferable to the cache-buster URL trick
// scripts otherwise resort to.
func (c *Client) PressInputPropertiesButton(sourceName, propertyName string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	inputName, property := sourceName, propertyName
	_, err = client.Inputs.PressInputPropertiesButton(&inputs.PressInputPropertiesButtonParams{
		InputName:    &inputName,
		PropertyName: &property,
	})
	if err != nil {
		return fmt.Errorf("failed to press button '%s' on source '%s': %w. "+
			"The source may not exist, or may have no such button property", propertyName, sourceName, err)
	}

	return nil
}

// SetSceneItemEnabled sets a scene item's visibility to an explicit state.
//
// Prefer this over ToggleSourceVisibility whenever the caller knows the state it
// wants. A toggle is not idempotent: a retry, a duplicate event or two rules
// firing on the same item leave it in the opposite state to the one intended.
// obs-websocket has always had SetSceneItemEnabled -- it was used privately here
// but never exposed, which is why the automation executor ended up toggling when
// asked to set. (FB-55)
func (c *Client) SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.SceneItems.SetSceneItemEnabled(&sceneitems.SetSceneItemEnabledParams{
		SceneName:        &sceneName,
		SceneItemId:      &sceneItemID,
		SceneItemEnabled: &enabled,
	})
	if err != nil {
		return fmt.Errorf("failed to set visibility for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return nil
}

// GetSceneItemEnabled reports whether a scene item is currently visible.
//
// This is the reader that pairs with SetSceneItemEnabled. Without it a caller
// could only discover an item's visibility by toggling it, or by listing the
// whole scene -- which is why ToggleSourceVisibility used to make this request
// inline. (FB-60)
func (c *Client) GetSceneItemEnabled(sceneName string, sceneItemID int) (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	resp, err := client.SceneItems.GetSceneItemEnabled(&sceneitems.GetSceneItemEnabledParams{
		SceneName:   &sceneName,
		SceneItemId: &sceneItemID,
	})
	if err != nil {
		return false, fmt.Errorf("failed to get visibility state for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return resp.SceneItemEnabled, nil
}

// ToggleSourceVisibility toggles the visibility of a source in a specific scene.
func (c *Client) ToggleSourceVisibility(sceneName string, sourceID int) (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	// Read, then invert. Prefer SetSceneItemEnabled wherever the caller knows the
	// state it wants -- a toggle cannot be retried safely.
	current, err := c.GetSceneItemEnabled(sceneName, sourceID)
	if err != nil {
		return false, err
	}

	sceneItemIDInt := sourceID
	newState := !current
	newStateBool := newState
	_, err = client.SceneItems.SetSceneItemEnabled(&sceneitems.SetSceneItemEnabledParams{
		SceneName:        &sceneName,
		SceneItemId:      &sceneItemIDInt,
		SceneItemEnabled: &newStateBool,
	})
	if err != nil {
		return false, fmt.Errorf("failed to toggle visibility for source %d in scene '%s': %w", sourceID, sceneName, err)
	}

	return newState, nil
}

// GetInputMute retrieves the mute state of an audio input.
func (c *Client) GetInputMute(inputName string) (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	name := inputName
	resp, err := client.Inputs.GetInputMute(&inputs.GetInputMuteParams{
		InputName: &name,
	})
	if err != nil {
		return false, fmt.Errorf("failed to get mute state for input '%s': %w. Input may not exist", inputName, err)
	}

	return resp.InputMuted, nil
}

// ToggleInputMute toggles the mute state of an audio input.
func (c *Client) ToggleInputMute(inputName string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	name := inputName
	_, err = client.Inputs.ToggleInputMute(&inputs.ToggleInputMuteParams{
		InputName: &name,
	})
	if err != nil {
		return fmt.Errorf("failed to toggle mute for input '%s': %w. Input may not exist", inputName, err)
	}

	return nil
}

// SetInputMute sets an input's mute state explicitly.
//
// Prefer this to ToggleInputMute wherever the caller knows the state it wants.
// The same argument as SetSceneItemEnabled: a toggle cannot be retried, so a
// timeout, a duplicate event or two rules firing on one input leave it in the
// opposite state to the one intended -- and for audio that means a silent
// stream nobody notices until the VOD. (FB-68)
func (c *Client) SetInputMute(inputName string, muted bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	name := inputName
	_, err = client.Inputs.SetInputMute(&inputs.SetInputMuteParams{
		InputName:  &name,
		InputMuted: &muted,
	})
	if err != nil {
		return fmt.Errorf("failed to set mute=%v for input '%s': %w. Input may not exist", muted, inputName, err)
	}

	return nil
}

// SetInputVolume sets the volume level of an audio input.
// volumeDb is in decibels (-100.0 to 26.0), or use volumeMul (0.0 to 20.0) for multiplier.
func (c *Client) SetInputVolume(inputName string, volumeDb *float64, volumeMul *float64) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	name := inputName
	params := &inputs.SetInputVolumeParams{
		InputName: &name,
	}

	if volumeDb != nil {
		params.InputVolumeDb = volumeDb
	}
	if volumeMul != nil {
		params.InputVolumeMul = volumeMul
	}

	_, err = client.Inputs.SetInputVolume(params)
	if err != nil {
		return fmt.Errorf("failed to set volume for input '%s': %w. Input may not exist", inputName, err)
	}

	return nil
}

// GetInputVolume retrieves the volume level of an audio input.
// Returns (volumeMultiplier, volumeDb, error).
func (c *Client) GetInputVolume(inputName string) (float64, float64, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, 0, err
	}

	name := inputName
	resp, err := client.Inputs.GetInputVolume(&inputs.GetInputVolumeParams{
		InputName: &name,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get volume for input '%s': %w. Input may not exist", inputName, err)
	}

	return resp.InputVolumeMul, resp.InputVolumeDb, nil
}

// GetOBSStatus retrieves overall OBS status information.
func (c *Client) GetOBSStatus() (*OBSStatus, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	// Get version info
	versionResp, err := client.General.GetVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get OBS version: %w", err)
	}

	// Get stats
	statsResp, err := client.General.GetStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get OBS stats: %w", err)
	}

	// Get recording status
	recordStatus, err := c.GetRecordingStatus()
	if err != nil {
		// Non-fatal, continue with empty status
		recordStatus = &RecordingStatus{}
	}

	// Get streaming status
	streamStatus, err := c.GetStreamingStatus()
	if err != nil {
		// Non-fatal, continue with empty status
		streamStatus = &StreamingStatus{}
	}

	// Get current scene
	_, currentScene, err := c.GetSceneList()
	if err != nil {
		// Non-fatal, continue with empty scene name
		currentScene = ""
	}

	status := &OBSStatus{
		Version:          versionResp.ObsVersion,
		WebSocketVersion: versionResp.ObsWebSocketVersion,
		Platform:         versionResp.Platform,
		CurrentScene:     currentScene,
		Recording:        recordStatus.Active,
		Streaming:        streamStatus.Active,
		FPS:              statsResp.ActiveFps,
		FrameTime:        statsResp.AverageFrameRenderTime,
		Frames:           int(statsResp.OutputTotalFrames),
		DroppedFrames:    int(statsResp.OutputSkippedFrames),

		SupportedImageFormats: versionResp.SupportedImageFormats,
	}

	// Non-fatal, like the scene name above: a status report without the canvas
	// is still worth returning.
	if video, err := c.GetVideoSettings(); err == nil {
		status.Video = video
	}

	return status, nil
}

// GetVideoSettings reports the canvas resolution, output resolution and frame
// rate.
//
// This is the question every layout decision starts from and nothing could ask.
// The scene-designer skill assumed 1920x1080 throughout, which is wrong on any
// canvas that is not -- and placements computed against the wrong canvas land
// off-screen rather than merely off-centre. (FB-69)
func (c *Client) GetVideoSettings() (*VideoSettings, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Config.GetVideoSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get video settings: %w", err)
	}

	return &VideoSettings{
		BaseWidth:      resp.BaseWidth,
		BaseHeight:     resp.BaseHeight,
		OutputWidth:    resp.OutputWidth,
		OutputHeight:   resp.OutputHeight,
		FPSNumerator:   resp.FpsNumerator,
		FPSDenominator: resp.FpsDenominator,
	}, nil
}

// OBSStatus represents overall OBS status information.
type OBSStatus struct {
	Version          string  `json:"version"`
	WebSocketVersion string  `json:"websocket_version"`
	Platform         string  `json:"platform"`
	CurrentScene     string  `json:"current_scene"`
	Recording        bool    `json:"recording"`
	Streaming        bool    `json:"streaming"`
	FPS              float64 `json:"fps"`
	FrameTime        float64 `json:"frame_time_ms"`
	Frames           int     `json:"frames"`
	DroppedFrames    int     `json:"dropped_frames"`

	// Video is the canvas OBS is compositing onto. Nil when it could not be
	// read, which is not fatal: everything else in the status is still useful,
	// and failing the whole call over it would be worse than omitting it.
	Video *VideoSettings `json:"video,omitempty"`

	// SupportedImageFormats is what this OBS build can produce for a
	// screenshot. It comes from Qt's image writers and so varies by build,
	// which is why it is reported rather than assumed. (FB-73)
	SupportedImageFormats []string `json:"supported_image_formats,omitempty"`
}

// VideoSettings describes OBS's canvas and frame rate.
//
// It is the answer to "how big is the thing I am laying out on", which nothing
// could ask before: the scene-designer skill hardcoded 1920x1080 and was simply
// wrong on this machine, where the canvas is 2560x1440. Every placement decision
// depends on it. (FB-69)
type VideoSettings struct {
	// BaseWidth/BaseHeight are the canvas: the coordinate space scene item
	// transforms live in, and what assets should be authored against.
	BaseWidth  float64 `json:"base_width"`
	BaseHeight float64 `json:"base_height"`

	// OutputWidth/OutputHeight are what OBS actually encodes, after any
	// downscale. Not the coordinate space -- a source at x=2000 is on-canvas at
	// 2560 wide even when the output is 1920.
	OutputWidth  float64 `json:"output_width"`
	OutputHeight float64 `json:"output_height"`

	// OBS stores frame rate as a fraction, because 59.94 is 60000/1001.
	FPSNumerator   float64 `json:"fps_numerator"`
	FPSDenominator float64 `json:"fps_denominator"`
}

// FPS is the frame rate as a single number.
//
// Guarded rather than divided directly: obs-websocket marshals with omitempty,
// so a field it does not report arrives as zero rather than absent.
func (v VideoSettings) FPS() float64 {
	if v.FPSDenominator == 0 {
		return 0
	}
	return v.FPSNumerator / v.FPSDenominator
}

// IsScaled reports whether the output resolution differs from the canvas.
//
// Worth surfacing because it is the common source of confusion when a layout
// looks right in OBS and wrong in the stream.
func (v VideoSettings) IsScaled() bool {
	if v.BaseWidth == 0 || v.BaseHeight == 0 {
		return false
	}
	return v.BaseWidth != v.OutputWidth || v.BaseHeight != v.OutputHeight
}

// SourceState represents the visibility state of a source for preset capture/apply.
type SourceState struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// CaptureSceneState captures the current state of all sources in a scene.
// Returns source IDs, names, and their enabled (visible) states.
// TODO: For scenes with >25 sources, consider implementing batch operations or
// concurrent processing to improve performance.
func (c *Client) CaptureSceneState(sceneName string) ([]SourceState, error) {
	scene, err := c.GetSceneByName(sceneName)
	if err != nil {
		return nil, fmt.Errorf("failed to capture scene state: %w", err)
	}

	states := make([]SourceState, len(scene.Sources))
	for i, src := range scene.Sources {
		states[i] = SourceState{
			ID:      src.ID,
			Name:    src.Name,
			Enabled: src.Enabled,
		}
	}

	return states, nil
}

// ApplyScenePreset applies source visibility states to a scene.
// This sets each source's enabled state according to the provided states.
// TODO: For scenes with >25 sources, consider implementing batch operations to improve
// performance and provide better error handling (collect partial failures rather than
// failing on first error).
func (c *Client) ApplyScenePreset(sceneName string, sources []SourceState) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	for _, src := range sources {
		sceneItemID := src.ID
		enabled := src.Enabled
		_, err := client.SceneItems.SetSceneItemEnabled(&sceneitems.SetSceneItemEnabledParams{
			SceneName:        &sceneName,
			SceneItemId:      &sceneItemID,
			SceneItemEnabled: &enabled,
		})
		if err != nil {
			return fmt.Errorf("failed to set visibility for source '%s' (ID %d): %w", src.Name, src.ID, err)
		}
	}

	return nil
}

// ScreenshotOptions configures screenshot capture settings.
type ScreenshotOptions struct {
	SourceName string // Name of the source or scene to capture
	Format     string // Image format: "png" or "jpg" (default: "png")
	Width      int    // Optional resize width (0 = original)
	Height     int    // Optional resize height (0 = original)
	Quality    int    // Compression quality 0-100 (default: -1 = library default)
}

// TakeSourceScreenshot captures a screenshot of the specified source or scene.
// Returns the Base64-encoded image data with data URI prefix (e.g., "data:image/png;base64,...").
func (c *Client) TakeSourceScreenshot(opts ScreenshotOptions) (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	// Set defaults
	imageFormat := opts.Format
	if imageFormat == "" {
		imageFormat = "png"
	}

	// Build parameters
	params := &sources.GetSourceScreenshotParams{
		SourceName:  &opts.SourceName,
		ImageFormat: &imageFormat,
	}

	// Optional width/height
	if opts.Width > 0 {
		width := float64(opts.Width)
		params.ImageWidth = &width
	}
	if opts.Height > 0 {
		height := float64(opts.Height)
		params.ImageHeight = &height
	}

	// Optional quality (for JPEG)
	if opts.Quality >= 0 && opts.Quality <= 100 {
		quality := float64(opts.Quality)
		params.ImageCompressionQuality = &quality
	}

	resp, err := client.Sources.GetSourceScreenshot(params)
	if err != nil {
		return "", fmt.Errorf("failed to capture screenshot of '%s': %w", opts.SourceName, err)
	}

	// OBS returns data as a data URI (e.g., "data:image/png;base64,iVBORw0...")
	// Strip the prefix to return clean base64 data
	imageData := resp.ImageData
	if idx := strings.Index(imageData, ","); idx != -1 {
		imageData = imageData[idx+1:]
	}

	return imageData, nil
}

// BrowserSourceSettings holds configuration for creating a browser source.
type BrowserSourceSettings struct {
	URL         string // URL to display
	Width       int    // Width in pixels
	Height      int    // Height in pixels
	RefreshRate int    // Page refresh rate in seconds (0 = no refresh)
	CSS         string // Custom CSS to inject
}

// SceneItemTransform represents the transform properties of a scene item.
// SceneItemTransform is a scene item's placement within its scene.
//
// Every settable field obs-websocket accepts must appear here. goobs marshals
// typedefs.SceneItemTransform with no omitempty, and obs-websocket applies any
// transform key present in the request, so a field missing from this struct is
// not "left alone" on a write -- it is sent as its zero value and applied.
// Alignment was missing until FB-54, which meant every set_source_transform,
// set_source_crop and set_source_bounds call sent alignment=0 (OBS_ALIGN_CENTER)
// and silently re-anchored any item still on the libobs default of
// OBS_ALIGN_TOP|OBS_ALIGN_LEFT (5), shifting it by half its rendered size.
//
// Width, Height, SourceWidth and SourceHeight are derived by OBS and read-only;
// they are populated on read for diffing and are never sent on a write.
type SceneItemTransform struct {
	PositionX float64 `json:"position_x"`
	PositionY float64 `json:"position_y"`
	ScaleX    float64 `json:"scale_x"`
	ScaleY    float64 `json:"scale_y"`
	Rotation  float64 `json:"rotation"`

	// Alignment is an OBS_ALIGN_* bitmask: 0 centre, 1 left, 2 right, 4 top,
	// 8 bottom. New scene items default to 5 (top|left).
	Alignment       int     `json:"alignment"`
	BoundsType      string  `json:"bounds_type"`
	BoundsAlignment int     `json:"bounds_alignment"`
	BoundsWidth     float64 `json:"bounds_width"`
	BoundsHeight    float64 `json:"bounds_height"`
	CropToBounds    bool    `json:"crop_to_bounds"`

	CropTop    int `json:"crop_top"`
	CropBottom int `json:"crop_bottom"`
	CropLeft   int `json:"crop_left"`
	CropRight  int `json:"crop_right"`

	// Derived by OBS; read-only.
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	SourceWidth  float64 `json:"source_width"`
	SourceHeight float64 `json:"source_height"`
}

// CreateBrowserSource creates a new browser source in the specified scene.
// Returns the scene item ID of the created source.
func (c *Client) CreateBrowserSource(sceneName, sourceName string, settings BrowserSourceSettings) (int, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	// Build input settings
	inputSettings := map[string]interface{}{
		"url":    settings.URL,
		"width":  settings.Width,
		"height": settings.Height,
	}

	if settings.RefreshRate > 0 {
		inputSettings["refresh_rate"] = settings.RefreshRate
	}
	if settings.CSS != "" {
		inputSettings["css"] = settings.CSS
	}

	// Create the browser source
	enabled := true
	inputKind := "browser_source"
	resp, err := client.Inputs.CreateInput(&inputs.CreateInputParams{
		SceneName:        &sceneName,
		InputName:        &sourceName,
		InputKind:        &inputKind,
		InputSettings:    inputSettings,
		SceneItemEnabled: &enabled,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create browser source '%s' in scene '%s': %w", sourceName, sceneName, err)
	}

	return int(resp.SceneItemId), nil
}

// CreateInput creates a new input source in the specified scene.
// Returns the scene item ID of the created source.
func (c *Client) CreateInput(sceneName, sourceName, inputKind string, settings map[string]interface{}) (int, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	enabled := true
	resp, err := client.Inputs.CreateInput(&inputs.CreateInputParams{
		SceneName:        &sceneName,
		InputName:        &sourceName,
		InputKind:        &inputKind,
		InputSettings:    settings,
		SceneItemEnabled: &enabled,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create input '%s' of type '%s' in scene '%s': %w", sourceName, inputKind, sceneName, err)
	}

	return int(resp.SceneItemId), nil
}

// CreateSceneItem places an input that already exists into a scene.
//
// The distinction CreateInput cannot express: an input is a shared object, and
// putting it in a second scene is adding a reference, not making a copy. Without
// this, showing one overlay in two scenes meant creating it twice under
// different names -- two objects to configure, and two to keep in step.
// OVERLAY_NowPlaying is exactly this case. (FB-71)
func (c *Client) CreateSceneItem(sceneName, sourceName string, enabled bool) (int, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	scene, source := sceneName, sourceName
	resp, err := client.SceneItems.CreateSceneItem(&sceneitems.CreateSceneItemParams{
		SceneName:        &scene,
		SourceName:       &source,
		SceneItemEnabled: &enabled,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to add source '%s' to scene '%s': %w. "+
			"The scene or the source may not exist", sourceName, sceneName, err)
	}

	return resp.SceneItemId, nil
}

// GetSceneItemTransform retrieves the transform properties of a scene item.
func (c *Client) GetSceneItemTransform(sceneName string, sceneItemID int) (*SceneItemTransform, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.SceneItems.GetSceneItemTransform(&sceneitems.GetSceneItemTransformParams{
		SceneName:   &sceneName,
		SceneItemId: &sceneItemID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get transform for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return fromGoobsTransform(resp.SceneItemTransform), nil
}

// SetSceneItemTransform sets the transform properties of a scene item.
// toGoobsTransform converts our transform into the wire type.
//
// Width, Height, SourceWidth and SourceHeight are deliberately omitted: OBS
// derives them and ignores them on a write. Every other field must be set,
// because goobs marshals this struct with no omitempty and obs-websocket applies
// any key present -- so a field left at its zero value here is transmitted and
// applied, not skipped. (FB-54)
// BoundsTypeNone is the bounds mode OBS uses when an item has no bounding box.
// With it set, the bounds dimensions are ignored entirely.
const BoundsTypeNone = "OBS_BOUNDS_NONE"

// NormaliseBounds makes a transform writable.
//
// OBS reports bounds of zero for any item that has never had a bounding box --
// which is most items -- but obs-websocket rejects a write whose boundsWidth or
// boundsHeight is below 1, and goobs sends every field because it marshals
// without omitempty. The result is that a transform read straight out of OBS
// cannot be written back: read-modify-write, which is what every transform tool
// does, failed with RequestFieldOutOfRange naming a field the caller never
// touched. (FB-64)
//
// The dimensions are only forced when bounds are unused, so a caller that asks
// for a real bounds mode with a nonsense size still gets OBS's error rather than
// a silently resized bounding box. With OBS_BOUNDS_NONE the values are inert.
func NormaliseBounds(boundsType string, width, height float64) (string, float64, float64) {
	if boundsType == "" {
		boundsType = BoundsTypeNone
	}
	if boundsType != BoundsTypeNone {
		return boundsType, width, height
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return boundsType, width, height
}

func toGoobsTransform(transform *SceneItemTransform) *typedefs.SceneItemTransform {
	boundsType, boundsWidth, boundsHeight := NormaliseBounds(
		transform.BoundsType, transform.BoundsWidth, transform.BoundsHeight)

	return &typedefs.SceneItemTransform{
		PositionX:       transform.PositionX,
		PositionY:       transform.PositionY,
		ScaleX:          transform.ScaleX,
		ScaleY:          transform.ScaleY,
		Rotation:        transform.Rotation,
		Alignment:       float64(transform.Alignment),
		BoundsType:      boundsType,
		BoundsAlignment: float64(transform.BoundsAlignment),
		BoundsWidth:     boundsWidth,
		BoundsHeight:    boundsHeight,
		CropToBounds:    transform.CropToBounds,
		CropTop:         float64(transform.CropTop),
		CropBottom:      float64(transform.CropBottom),
		CropLeft:        float64(transform.CropLeft),
		CropRight:       float64(transform.CropRight),
	}
}

func (c *Client) SetSceneItemTransform(sceneName string, sceneItemID int, transform *SceneItemTransform) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	// Build transform struct
	t := toGoobsTransform(transform)

	_, err = client.SceneItems.SetSceneItemTransform(&sceneitems.SetSceneItemTransformParams{
		SceneName:          &sceneName,
		SceneItemId:        &sceneItemID,
		SceneItemTransform: t,
	})
	if err != nil {
		return fmt.Errorf("failed to set transform for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return nil
}

// SetSceneItemIndex sets the z-order index of a scene item.
func (c *Client) SetSceneItemIndex(sceneName string, sceneItemID int, index int) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.SceneItems.SetSceneItemIndex(&sceneitems.SetSceneItemIndexParams{
		SceneName:      &sceneName,
		SceneItemId:    &sceneItemID,
		SceneItemIndex: &index,
	})
	if err != nil {
		return fmt.Errorf("failed to set index for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return nil
}

// SetSceneItemLocked sets the locked state of a scene item.
func (c *Client) SetSceneItemLocked(sceneName string, sceneItemID int, locked bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.SceneItems.SetSceneItemLocked(&sceneitems.SetSceneItemLockedParams{
		SceneName:       &sceneName,
		SceneItemId:     &sceneItemID,
		SceneItemLocked: &locked,
	})
	if err != nil {
		return fmt.Errorf("failed to set locked state for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return nil
}

// GetSceneItemLocked gets the locked state of a scene item.
func (c *Client) GetSceneItemLocked(sceneName string, sceneItemID int) (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	resp, err := client.SceneItems.GetSceneItemLocked(&sceneitems.GetSceneItemLockedParams{
		SceneName:   &sceneName,
		SceneItemId: &sceneItemID,
	})
	if err != nil {
		return false, fmt.Errorf("failed to get locked state for item %d in scene '%s': %w", sceneItemID, sceneName, err)
	}

	return resp.SceneItemLocked, nil
}

// DuplicateSceneItem duplicates a scene item within the same scene or to another scene.
// Returns the scene item ID of the duplicated item.
func (c *Client) DuplicateSceneItem(sceneName string, sceneItemID int, destScene string) (int, error) {
	client, err := c.getClient()
	if err != nil {
		return 0, err
	}

	params := &sceneitems.DuplicateSceneItemParams{
		SceneName:   &sceneName,
		SceneItemId: &sceneItemID,
	}

	// If destination scene is specified and different, set it
	if destScene != "" && destScene != sceneName {
		params.DestinationSceneName = &destScene
	}

	resp, err := client.SceneItems.DuplicateSceneItem(params)
	if err != nil {
		return 0, fmt.Errorf("failed to duplicate item %d from scene '%s': %w", sceneItemID, sceneName, err)
	}

	return int(resp.SceneItemId), nil
}

// RemoveSceneItem removes a scene item from a scene.
func (c *Client) RemoveSceneItem(sceneName string, sceneItemID int) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.SceneItems.RemoveSceneItem(&sceneitems.RemoveSceneItemParams{
		SceneName:   &sceneName,
		SceneItemId: &sceneItemID,
	})
	if err != nil {
		return fmt.Errorf("failed to remove item %d from scene '%s': %w", sceneItemID, sceneName, err)
	}

	return nil
}

// GetInputKindList returns a list of available input kinds (source types).
func (c *Client) GetInputKindList() ([]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Inputs.GetInputKindList(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get input kind list: %w", err)
	}

	return resp.InputKinds, nil
}

// AudioDevice represents a selectable audio device returned from OBS input property queries.
type AudioDevice struct {
	Name  string `json:"name"`  // Human-readable display name shown in OBS UI
	Value string `json:"value"` // Device ID used as device_id in CreateInput settings
}

// SpecialInputs holds the names of OBS's built-in global audio inputs (configured in Settings → Audio).
type SpecialInputs struct {
	Desktop1 string // Desktop Audio
	Desktop2 string // Desktop Audio 2
	Mic1     string // Mic/Auxiliary Audio
	Mic2     string // Mic/Auxiliary Audio 2
	Mic3     string // Mic/Auxiliary Audio 3
	Mic4     string // Mic/Auxiliary Audio 4
}

// GetSpecialInputs returns the names of OBS's built-in global audio sources.
func (c *Client) GetSpecialInputs() (*SpecialInputs, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Inputs.GetSpecialInputs()
	if err != nil {
		return nil, fmt.Errorf("failed to get special inputs: %w", err)
	}
	return &SpecialInputs{
		Desktop1: resp.Desktop1,
		Desktop2: resp.Desktop2,
		Mic1:     resp.Mic1,
		Mic2:     resp.Mic2,
		Mic3:     resp.Mic3,
		Mic4:     resp.Mic4,
	}, nil
}

// GetInputPropertiesItems returns the selectable items for a list property on an input.
// For WASAPI inputs, use propertyName="device_id" to enumerate available audio devices.
func (c *Client) GetInputPropertiesItems(inputName, propertyName string) ([]AudioDevice, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Inputs.GetInputPropertiesListPropertyItems(
		&inputs.GetInputPropertiesListPropertyItemsParams{
			InputName:    &inputName,
			PropertyName: &propertyName,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get property items for '%s'.%s: %w", inputName, propertyName, err)
	}
	devices := make([]AudioDevice, 0, len(resp.PropertyItems))
	for _, item := range resp.PropertyItems {
		val := ""
		if item.ItemValue != nil {
			val = fmt.Sprintf("%v", item.ItemValue)
		}
		devices = append(devices, AudioDevice{Name: item.ItemName, Value: val})
	}
	return devices, nil
}

// CreateAudioInput creates a WASAPI audio capture source in a scene.
// inputKind must be "wasapi_output_capture" (playback devices, e.g. Voicemeeter virtual output)
// or "wasapi_input_capture" (recording devices, e.g. microphones).
// deviceID is the Value from GetInputPropertiesItems; pass "default" to use the system default device.
func (c *Client) CreateAudioInput(sceneName, sourceName, inputKind, deviceID string) (int, error) {
	return c.CreateInput(sceneName, sourceName, inputKind, map[string]interface{}{
		"device_id": deviceID,
	})
}

// =============================================================================
// Filter Types and Methods (FB-23)
// =============================================================================

// FilterInfo represents basic information about a source filter.
type FilterInfo struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Index   int    `json:"index"`
	Enabled bool   `json:"enabled"`
}

// FilterDetails represents detailed information about a source filter.
type FilterDetails struct {
	Name     string                 `json:"name"`
	Kind     string                 `json:"kind"`
	Index    int                    `json:"index"`
	Enabled  bool                   `json:"enabled"`
	Settings map[string]interface{} `json:"settings"`
}

// GetSourceFilterList retrieves all filters on a source.
func (c *Client) GetSourceFilterList(sourceName string) ([]FilterInfo, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Filters.GetSourceFilterList(&filters.GetSourceFilterListParams{
		SourceName: &sourceName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get filters for source '%s': %w", sourceName, err)
	}

	result := make([]FilterInfo, len(resp.Filters))
	for i, f := range resp.Filters {
		result[i] = FilterInfo{
			Name:    f.FilterName,
			Kind:    f.FilterKind,
			Index:   int(f.FilterIndex),
			Enabled: f.FilterEnabled,
		}
	}

	return result, nil
}

// GetSourceFilter retrieves details about a specific filter on a source.
func (c *Client) GetSourceFilter(sourceName, filterName string) (*FilterDetails, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Filters.GetSourceFilter(&filters.GetSourceFilterParams{
		SourceName: &sourceName,
		FilterName: &filterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get filter '%s' on source '%s': %w", filterName, sourceName, err)
	}

	return &FilterDetails{
		Name:     filterName,
		Kind:     resp.FilterKind,
		Index:    int(resp.FilterIndex),
		Enabled:  resp.FilterEnabled,
		Settings: resp.FilterSettings,
	}, nil
}

// CreateSourceFilter creates a new filter on a source.
func (c *Client) CreateSourceFilter(sourceName, filterName, filterKind string, settings map[string]interface{}) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	params := &filters.CreateSourceFilterParams{
		SourceName: &sourceName,
		FilterName: &filterName,
		FilterKind: &filterKind,
	}

	if settings != nil {
		params.FilterSettings = settings
	}

	_, err = client.Filters.CreateSourceFilter(params)
	if err != nil {
		return fmt.Errorf("failed to create filter '%s' on source '%s': %w", filterName, sourceName, err)
	}

	return nil
}

// RemoveSourceFilter removes a filter from a source.
func (c *Client) RemoveSourceFilter(sourceName, filterName string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Filters.RemoveSourceFilter(&filters.RemoveSourceFilterParams{
		SourceName: &sourceName,
		FilterName: &filterName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove filter '%s' from source '%s': %w", filterName, sourceName, err)
	}

	return nil
}

// SetSourceFilterEnabled enables or disables a filter on a source.
func (c *Client) SetSourceFilterEnabled(sourceName, filterName string, enabled bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Filters.SetSourceFilterEnabled(&filters.SetSourceFilterEnabledParams{
		SourceName:    &sourceName,
		FilterName:    &filterName,
		FilterEnabled: &enabled,
	})
	if err != nil {
		return fmt.Errorf("failed to set filter '%s' enabled=%v on source '%s': %w", filterName, enabled, sourceName, err)
	}

	return nil
}

// SetSourceFilterSettings updates the settings of a filter.
// If overlay is true, settings are merged with existing; otherwise they replace entirely.
func (c *Client) SetSourceFilterSettings(sourceName, filterName string, settings map[string]interface{}, overlay bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Filters.SetSourceFilterSettings(&filters.SetSourceFilterSettingsParams{
		SourceName:     &sourceName,
		FilterName:     &filterName,
		FilterSettings: settings,
		Overlay:        &overlay,
	})
	if err != nil {
		return fmt.Errorf("failed to set settings for filter '%s' on source '%s': %w", filterName, sourceName, err)
	}

	return nil
}

// GetSourceFilterKindList retrieves all available filter types.
func (c *Client) GetSourceFilterKindList() ([]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Filters.GetSourceFilterKindList(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get filter kind list: %w", err)
	}

	return resp.SourceFilterKinds, nil
}

// =============================================================================
// Transition Types and Methods (FB-24)
// =============================================================================

// TransitionInfo represents basic information about a scene transition.
type TransitionInfo struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Fixed        bool   `json:"fixed"`
	Configurable bool   `json:"configurable"`
}

// TransitionDetails represents the current scene transition with settings.
type TransitionDetails struct {
	Name         string                 `json:"name"`
	Kind         string                 `json:"kind"`
	Duration     int                    `json:"duration_ms"`
	Configurable bool                   `json:"configurable"`
	Settings     map[string]interface{} `json:"settings,omitempty"`
}

// GetSceneTransitionList retrieves all available scene transitions.
func (c *Client) GetSceneTransitionList() ([]TransitionInfo, string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, "", err
	}

	resp, err := client.Transitions.GetSceneTransitionList()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get transition list: %w", err)
	}

	result := make([]TransitionInfo, len(resp.Transitions))
	for i, t := range resp.Transitions {
		result[i] = TransitionInfo{
			Name:         t.TransitionName,
			Kind:         t.TransitionKind,
			Fixed:        t.TransitionFixed,
			Configurable: t.TransitionConfigurable,
		}
	}

	return result, resp.CurrentSceneTransitionName, nil
}

// GetCurrentSceneTransition retrieves the current scene transition details.
func (c *Client) GetCurrentSceneTransition() (*TransitionDetails, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Transitions.GetCurrentSceneTransition()
	if err != nil {
		return nil, fmt.Errorf("failed to get current transition: %w", err)
	}

	return &TransitionDetails{
		Name:         resp.TransitionName,
		Kind:         resp.TransitionKind,
		Duration:     int(resp.TransitionDuration),
		Configurable: resp.TransitionConfigurable,
		Settings:     resp.TransitionSettings,
	}, nil
}

// SetCurrentSceneTransition sets the current scene transition.
func (c *Client) SetCurrentSceneTransition(transitionName string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Transitions.SetCurrentSceneTransition(&transitions.SetCurrentSceneTransitionParams{
		TransitionName: &transitionName,
	})
	if err != nil {
		return fmt.Errorf("failed to set current transition to '%s': %w", transitionName, err)
	}

	return nil
}

// SetCurrentSceneTransitionDuration sets the duration of the current scene transition.
func (c *Client) SetCurrentSceneTransitionDuration(durationMs int) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	duration := float64(durationMs)
	_, err = client.Transitions.SetCurrentSceneTransitionDuration(&transitions.SetCurrentSceneTransitionDurationParams{
		TransitionDuration: &duration,
	})
	if err != nil {
		return fmt.Errorf("failed to set transition duration to %dms: %w", durationMs, err)
	}

	return nil
}

// TriggerStudioModeTransition triggers the current scene transition in studio mode.
// This executes the transition from preview to program.
// Returns an error if studio mode is not enabled.
func (c *Client) TriggerStudioModeTransition() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Transitions.TriggerStudioModeTransition()
	if err != nil {
		// Check if it's because studio mode isn't enabled
		if strings.Contains(err.Error(), "studio mode") {
			return fmt.Errorf("studio mode is not enabled. Enable studio mode in OBS to use this feature")
		}
		return fmt.Errorf("failed to trigger studio mode transition: %w", err)
	}

	return nil
}

// =============================================================================
// Virtual Camera Types and Methods (FB-25)
// =============================================================================

// VirtualCamStatus represents the current virtual camera state.
type VirtualCamStatus struct {
	Active bool `json:"active"`
}

// GetVirtualCamStatus retrieves the current virtual camera status.
func (c *Client) GetVirtualCamStatus() (*VirtualCamStatus, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Outputs.GetVirtualCamStatus()
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual camera status: %w", err)
	}

	return &VirtualCamStatus{
		Active: resp.OutputActive,
	}, nil
}

// ToggleVirtualCam toggles the virtual camera on or off.
// Returns the new active state.
func (c *Client) ToggleVirtualCam() (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	resp, err := client.Outputs.ToggleVirtualCam()
	if err != nil {
		return false, fmt.Errorf("failed to toggle virtual camera: %w", err)
	}

	return resp.OutputActive, nil
}

// =============================================================================
// Replay Buffer Types and Methods (FB-25)
// =============================================================================

// ReplayBufferStatus represents the current replay buffer state.
type ReplayBufferStatus struct {
	Active bool `json:"active"`
}

// GetReplayBufferStatus retrieves the current replay buffer status.
func (c *Client) GetReplayBufferStatus() (*ReplayBufferStatus, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.Outputs.GetReplayBufferStatus()
	if err != nil {
		return nil, fmt.Errorf("failed to get replay buffer status: %w", err)
	}

	return &ReplayBufferStatus{
		Active: resp.OutputActive,
	}, nil
}

// ToggleReplayBuffer toggles the replay buffer on or off.
// Returns the new active state.
func (c *Client) ToggleReplayBuffer() (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	resp, err := client.Outputs.ToggleReplayBuffer()
	if err != nil {
		return false, fmt.Errorf("failed to toggle replay buffer: %w", err)
	}

	return resp.OutputActive, nil
}

// SaveReplayBuffer saves the current replay buffer to disk.
// The replay buffer must be active for this to succeed.
func (c *Client) SaveReplayBuffer() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Outputs.SaveReplayBuffer()
	if err != nil {
		return fmt.Errorf("failed to save replay buffer: %w", err)
	}

	return nil
}

// GetLastReplayBufferReplay retrieves the path to the last saved replay.
func (c *Client) GetLastReplayBufferReplay() (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	resp, err := client.Outputs.GetLastReplayBufferReplay()
	if err != nil {
		return "", fmt.Errorf("failed to get last replay path: %w", err)
	}

	return resp.SavedReplayPath, nil
}

// =============================================================================
// Studio Mode Types and Methods (FB-26)
// =============================================================================

// GetStudioModeEnabled retrieves whether studio mode is enabled.
func (c *Client) GetStudioModeEnabled() (bool, error) {
	client, err := c.getClient()
	if err != nil {
		return false, err
	}

	resp, err := client.Ui.GetStudioModeEnabled()
	if err != nil {
		return false, fmt.Errorf("failed to get studio mode status: %w", err)
	}

	return resp.StudioModeEnabled, nil
}

// SetStudioModeEnabled enables or disables studio mode.
func (c *Client) SetStudioModeEnabled(enabled bool) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Ui.SetStudioModeEnabled(&ui.SetStudioModeEnabledParams{
		StudioModeEnabled: &enabled,
	})
	if err != nil {
		return fmt.Errorf("failed to set studio mode enabled=%v: %w", enabled, err)
	}

	return nil
}

// GetCurrentPreviewScene retrieves the current preview scene in studio mode.
// Returns an error if studio mode is not enabled.
func (c *Client) GetCurrentPreviewScene() (string, error) {
	client, err := c.getClient()
	if err != nil {
		return "", err
	}

	resp, err := client.Scenes.GetCurrentPreviewScene()
	if err != nil {
		if strings.Contains(err.Error(), "studio mode") {
			return "", fmt.Errorf("studio mode is not enabled. Enable studio mode in OBS to use preview scenes")
		}
		return "", fmt.Errorf("failed to get current preview scene: %w", err)
	}

	return resp.SceneName, nil
}

// SetCurrentPreviewScene sets the current preview scene in studio mode.
// Returns an error if studio mode is not enabled.
func (c *Client) SetCurrentPreviewScene(sceneName string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Scenes.SetCurrentPreviewScene(&scenes.SetCurrentPreviewSceneParams{
		SceneName: &sceneName,
	})
	if err != nil {
		if strings.Contains(err.Error(), "studio mode") {
			return fmt.Errorf("studio mode is not enabled. Enable studio mode in OBS to use preview scenes")
		}
		return fmt.Errorf("failed to set preview scene to '%s': %w", sceneName, err)
	}

	return nil
}

// =============================================================================
// Hotkey Methods (FB-26)
// =============================================================================

// TriggerHotkeyByName triggers a hotkey by its name.
// Use GetHotkeyList() to discover available hotkey names.
func (c *Client) TriggerHotkeyByName(hotkeyName string) error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.General.TriggerHotkeyByName(&general.TriggerHotkeyByNameParams{
		HotkeyName: &hotkeyName,
	})
	if err != nil {
		return fmt.Errorf("failed to trigger hotkey '%s': %w", hotkeyName, err)
	}

	return nil
}

// GetHotkeyList retrieves all available hotkey names.
func (c *Client) GetHotkeyList() ([]string, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.General.GetHotkeyList()
	if err != nil {
		return nil, fmt.Errorf("failed to get hotkey list: %w", err)
	}

	return resp.Hotkeys, nil
}

// StartVirtualCam starts the virtual camera.
func (c *Client) StartVirtualCam() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Outputs.StartVirtualCam()
	if err != nil {
		return fmt.Errorf("failed to start virtual camera: %w", err)
	}

	return nil
}

// StopVirtualCam stops the virtual camera.
func (c *Client) StopVirtualCam() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Outputs.StopVirtualCam()
	if err != nil {
		return fmt.Errorf("failed to stop virtual camera: %w", err)
	}

	return nil
}

// StartReplayBuffer starts the replay buffer.
func (c *Client) StartReplayBuffer() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Outputs.StartReplayBuffer()
	if err != nil {
		return fmt.Errorf("failed to start replay buffer: %w", err)
	}

	return nil
}

// StopReplayBuffer stops the replay buffer.
func (c *Client) StopReplayBuffer() error {
	client, err := c.getClient()
	if err != nil {
		return err
	}

	_, err = client.Outputs.StopReplayBuffer()
	if err != nil {
		return fmt.Errorf("failed to stop replay buffer: %w", err)
	}

	return nil
}

// fromGoobsTransform converts the wire type into ours.
//
// Extracted so the read path can be tested without a live OBS, the same way
// toGoobsTransform is. Every transform tool is read-modify-write, so a field
// dropped here is written back as zero on the next set -- the same corruption
// FB-54 caused on the write side, arriving by the other door. (FB-59)
func fromGoobsTransform(t *typedefs.SceneItemTransform) *SceneItemTransform {
	return &SceneItemTransform{
		PositionX:       t.PositionX,
		PositionY:       t.PositionY,
		ScaleX:          t.ScaleX,
		ScaleY:          t.ScaleY,
		Rotation:        t.Rotation,
		Alignment:       int(t.Alignment),
		BoundsType:      t.BoundsType,
		BoundsAlignment: int(t.BoundsAlignment),
		BoundsWidth:     t.BoundsWidth,
		BoundsHeight:    t.BoundsHeight,
		CropToBounds:    t.CropToBounds,
		CropTop:         int(t.CropTop),
		CropBottom:      int(t.CropBottom),
		CropLeft:        int(t.CropLeft),
		CropRight:       int(t.CropRight),
		Width:           t.Width,
		Height:          t.Height,
		SourceWidth:     t.SourceWidth,
		SourceHeight:    t.SourceHeight,
	}
}
