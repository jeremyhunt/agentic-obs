package testutil

import (
	"fmt"
	"strings"
	"sync"

	"github.com/andreykaipov/goobs/api/typedefs"
	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/obs/obstest"
)

// Ensure MockOBSClient implements the OBSClient interface
// This is verified at compile time in the mcp package

// MockOBSClient implements a mock OBS client for testing.
// It simulates OBS behavior without requiring a real OBS instance.
type MockOBSClient struct {
	mu sync.RWMutex

	// Connection state
	connected bool

	// world holds the OBS state this mock shares with the contract suite and,
	// through it, with a real OBS. See mock_world.go for why it is not modelled
	// here any more.
	world *obstest.Fake

	// currentScene has no counterpart in the fake, which models the scene graph
	// rather than the frontend's notion of what is on air.
	currentScene string

	// inputVolumes likewise: the fake models mute, not level.
	inputVolumes map[string]float64

	// Recording/Streaming state
	recording bool
	paused    bool
	streaming bool

	// Error injection for testing error paths
	ErrorOnConnect             error
	ErrorOnGetSceneList        error
	ErrorOnSetCurrentScene     error
	ErrorOnCreateScene         error
	ErrorOnRemoveScene         error
	ErrorOnStartRecording      error
	ErrorOnStopRecording       error
	ErrorOnPauseRecording      error
	ErrorOnResumeRecording     error
	ErrorOnStartStreaming      error
	ErrorOnStopStreaming       error
	ErrorOnListSources         error
	ErrorOnGetSourceSettings   error
	ErrorOnSetSceneItemEnabled error
	ErrorOnToggleVisibility    error
	ErrorOnGetInputMute        error
	ErrorOnToggleInputMute     error
	ErrorOnSetInputVolume      error
	ErrorOnGetInputVolume      error
	ErrorOnGetOBSStatus        error
	ErrorOnTakeScreenshot      error
	ErrorOnCreateBrowserSource error

	// Screenshot mock data
	mockScreenshotData string // Base64 PNG data to return

	// Design tool mock data
	sceneItemTransforms map[string]map[int]*obs.SceneItemTransform // scene -> itemID -> transform
	sceneItemLocked     map[string]map[int]bool                    // scene -> itemID -> locked
	inputKinds          []string                                   // available input kinds
	inputDefaults       map[string]map[string]interface{}          // input kind -> default settings
	buttonPresses       []string                                   // recorded "source:property" presses
	vendorCalls         []VendorCall                               // recorded vendor requests
	rawCalls            []RawCall                                  // recorded call_obs_request calls
	rawResponses        map[string]map[string]interface{}          // seeded raw request responses
	vendorResponses     map[string]map[string]any                  // "vendor/request" -> response
	nextSceneItemID     int                                        // counter for new scene items

	// Error injection for design tools
	ErrorOnSetInputMute               error
	ErrorOnGetVideoSettings           error
	ErrorOnCreateSceneItem            error
	ErrorOnCallVendorRequest          error
	ErrorOnCallRequest                error
	ErrorOnSetSourceSettings          error
	ErrorOnGetInputDefaultSettings    error
	ErrorOnPressInputPropertiesButton error

	ErrorOnCreateInput           error
	ErrorOnGetSceneItemTransform error
	ErrorOnSetSceneItemTransform error
	ErrorOnSetSceneItemIndex     error
	ErrorOnSetSceneItemLocked    error
	ErrorOnGetSceneItemLocked    error
	ErrorOnDuplicateSceneItem    error
	ErrorOnRemoveSceneItem       error
	ErrorOnGetInputKindList      error

	// Filter mock data
	sourceFilters map[string][]obs.FilterInfo              // source -> filters
	filterDetails map[string]map[string]*obs.FilterDetails // source -> filter name -> details
	filterKinds   []string                                 // available filter types

	// Transition mock data
	transitions       []obs.TransitionInfo
	currentTransition *obs.TransitionDetails
	studioModeEnabled bool

	// Error injection for filters
	ErrorOnGetSourceFilterList     error
	ErrorOnGetSourceFilter         error
	ErrorOnCreateSourceFilter      error
	ErrorOnRemoveSourceFilter      error
	ErrorOnSetSourceFilterEnabled  error
	ErrorOnSetSourceFilterSettings error
	ErrorOnGetSourceFilterKindList error

	// Error injection for transitions
	ErrorOnGetSceneTransitionList            error
	ErrorOnGetCurrentSceneTransition         error
	ErrorOnSetCurrentSceneTransition         error
	ErrorOnSetCurrentSceneTransitionDuration error
	ErrorOnTriggerStudioModeTransition       error

	// Virtual camera and replay buffer state (FB-25)
	virtualCamActive   bool
	replayBufferActive bool
	lastReplayPath     string
	hotkeys            []string
	previewScene       string

	// Error injection for virtual cam and replay buffer (FB-25)
	ErrorOnGetVirtualCamStatus       error
	ErrorOnToggleVirtualCam          error
	ErrorOnStartVirtualCam           error
	ErrorOnStopVirtualCam            error
	ErrorOnGetReplayBufferStatus     error
	ErrorOnToggleReplayBuffer        error
	ErrorOnStartReplayBuffer         error
	ErrorOnStopReplayBuffer          error
	ErrorOnSaveReplayBuffer          error
	ErrorOnGetLastReplayBufferReplay error

	// Error injection for studio mode and hotkeys (FB-26)
	ErrorOnGetStudioModeEnabled   error
	ErrorOnSetStudioModeEnabled   error
	ErrorOnGetCurrentPreviewScene error
	ErrorOnSetCurrentPreviewScene error
	ErrorOnTriggerHotkeyByName    error
	ErrorOnGetHotkeyList          error

	// Audio device mock data
	audioDevices                   []obs.AudioDevice
	ErrorOnGetSpecialInputs        error
	ErrorOnGetInputPropertiesItems error
	ErrorOnCreateAudioInput        error
}

// NewMockOBSClient creates a new mock OBS client with default test data.
func NewMockOBSClient() *MockOBSClient {
	// Seeded with the same fixture the mcp tests have always assumed. The names
	// are load-bearing: roughly forty tests look up "Scene 1", "Gaming",
	// "Microphone" and friends by name, so the fold keeps them rather than
	// making every one of those tests a rewrite.
	world := obstest.NewFake()

	for _, scene := range []string{"Scene 1", "Scene 2", "Gaming", "Starting Soon"} {
		if err := world.CreateScene(scene); err != nil {
			panic("seeding the mock world: " + err.Error())
		}
	}

	// The order matters. Scene item ids are handed out in sequence, and a good
	// number of tests address items by the id the old mock happened to give
	// them -- Webcam is 1 and Text is 2 in "Scene 1". Reproducing the ids costs
	// nothing here and saves rewriting those tests to learn nothing new.
	seed := func(scene, name, kind string, settings map[string]interface{}) {
		if _, err := world.CreateInput(scene, name, kind, settings); err != nil {
			panic("seeding the mock world: " + err.Error())
		}
	}
	seed("Scene 1", "Webcam", "dshow_input",
		map[string]interface{}{"video_device_id": "default", "resolution": "1920x1080"}) // 1
	seed("Scene 1", "Text", "text_gdiplus_v2", nil)     // 2
	seed("Gaming", "Game Capture", "game_capture", nil) // 3

	// Webcam placed a second time, in another scene. A source shared across
	// scenes is the case this project keeps getting wrong, so the fixture holds
	// one: CreateSceneItem references the input rather than copying it.
	if _, err := world.CreateSceneItem("Gaming", "Webcam", true); err != nil { // 4
		panic("seeding the mock world: " + err.Error())
	}

	// The audio inputs go in a scene of their own, after the ids above are
	// settled. OBS refcounts sources, so an input with no placement anywhere
	// does not survive -- the old mock kept a separate list of sources that
	// belonged to no scene, which is a state a real OBS cannot be in.
	seed("Scene 2", "Microphone", "wasapi_input_capture",
		map[string]interface{}{"device_id": "default", "use_device_timing": true})
	seed("Scene 2", "Desktop Audio", "wasapi_output_capture",
		map[string]interface{}{"device_id": "default"})

	// Filters on the seeded sources. They live in the world too, because a
	// filter belongs to a source and the contract checks that they agree.
	seedFilter := func(source, name, kind string, settings map[string]interface{}) {
		if err := world.CreateSourceFilter(source, name, kind, settings); err != nil {
			panic("seeding the mock world: " + err.Error())
		}
	}
	seedFilter("Webcam", "Color Correction", "color_filter_v2",
		map[string]interface{}{"brightness": 0.0, "contrast": 0.0, "saturation": 0.0})
	seedFilter("Webcam", "Sharpen", "sharpness_filter_v2",
		map[string]interface{}{"sharpness": 0.08})
	seedFilter("Microphone", "Noise Suppression", "noise_suppress_filter_v2",
		map[string]interface{}{"suppress_level": -30, "method": "rnnoise"})
	seedFilter("Microphone", "Compressor", "compressor_filter",
		map[string]interface{}{"ratio": 10.0, "threshold": -18.0})
	if err := world.SetSourceFilterEnabled("Microphone", "Compressor", false); err != nil {
		panic("seeding the mock world: " + err.Error())
	}

	return &MockOBSClient{
		connected:    false,
		world:        world,
		currentScene: "Scene 1",
		inputVolumes: map[string]float64{
			"Microphone":    0.0,
			"Desktop Audio": 0.0,
		},
		recording: false,
		paused:    false,
		streaming: false,

		// Everything below has no counterpart in the fake: it is the client's
		// own surface rather than OBS's scene state, so it stays here.
		studioModeEnabled:  false,
		virtualCamActive:   false,
		replayBufferActive: false,
		lastReplayPath:     "/recordings/replay-2024-01-15_14-30-00.mkv",
		previewScene:       "Scene 1",
		hotkeys: []string{
			"OBSBasic.StartRecording",
			"OBSBasic.StopRecording",
			"OBSBasic.StartStreaming",
			"OBSBasic.StopStreaming",
			"OBSBasic.ToggleMute",
			"OBSBasic.Screenshot",
			"OBSBasic.ReplayBuffer",
			"OBSBasic.SaveReplay",
		},
		inputKinds: []string{
			"text_gdiplus_v3", "image_source", "color_source_v3", "browser_source",
			"ffmpeg_source", "wasapi_input_capture", "wasapi_output_capture",
			"dshow_input", "game_capture", "window_capture", "monitor_capture",
		},
		filterKinds: []string{
			"color_filter_v2", "sharpness_filter_v2", "noise_suppress_filter_v2",
			"compressor_filter", "limiter_filter", "gain_filter", "chroma_key_filter_v2",
			"luma_key_filter", "mask_filter_v2", "scroll_filter", "crop_filter",
		},
		currentTransition: &obs.TransitionDetails{
			Name:         "Fade",
			Kind:         "fade_transition",
			Duration:     300,
			Configurable: true,
			Settings:     map[string]interface{}{},
		},
		transitions: []obs.TransitionInfo{
			{Name: "Cut", Kind: "cut_transition", Fixed: true, Configurable: false},
			{Name: "Fade", Kind: "fade_transition", Fixed: false, Configurable: true},
			{Name: "Swipe", Kind: "swipe_transition", Fixed: false, Configurable: true},
			{Name: "Slide", Kind: "slide_transition", Fixed: false, Configurable: true},
			{Name: "Stinger", Kind: "obs_stinger_transition", Fixed: false, Configurable: true},
		},
		audioDevices: []obs.AudioDevice{
			{Name: "Default", Value: "default"},
			{Name: "VoiceMeeter Output (VB-Audio VoiceMeeter VAIO)", Value: "{0.0.0.00000000}.{voicemeeter-output}"},
			{Name: "VoiceMeeter Aux Output (VB-Audio VoiceMeeter AUX VAIO)", Value: "{0.0.0.00000000}.{voicemeeter-aux-output}"},
			{Name: "Speakers (Realtek High Definition Audio)", Value: "{0.0.0.00000000}.{realtek-speakers}"},
		},
	}
}

// Connect simulates connecting to OBS.
func (m *MockOBSClient) Connect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnConnect != nil {
		return m.ErrorOnConnect
	}

	m.connected = true
	return nil
}

// Disconnect simulates disconnecting from OBS.
func (m *MockOBSClient) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
	return nil
}

// Close simulates closing the client.
func (m *MockOBSClient) Close() error {
	return m.Disconnect()
}

// IsConnected returns the connection state.
func (m *MockOBSClient) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// GetConnectionStatus returns mock connection status.
func (m *MockOBSClient) GetConnectionStatus() (obs.ConnectionStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return obs.ConnectionStatus{
		Connected:        m.connected,
		Host:             "localhost",
		Port:             "4455",
		OBSVersion:       "30.0.0",
		WebSocketVersion: "5.4.0",
		Platform:         "windows",
	}, nil
}

// HealthCheck simulates a health check.
func (m *MockOBSClient) HealthCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return nil
}

// SetCurrentScene simulates switching scenes.
func (m *MockOBSClient) SetCurrentScene(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSetCurrentScene != nil {
		return m.ErrorOnSetCurrentScene
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	// Scene existence is the world's to answer, not a second list kept here.
	if _, err := m.world.GetSceneByName(name); err != nil {
		return fmt.Errorf("scene '%s' not found", name)
	}

	m.currentScene = name
	return nil
}

// StartRecording simulates starting recording.
func (m *MockOBSClient) StartRecording() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStartRecording != nil {
		return m.ErrorOnStartRecording
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if m.recording {
		return fmt.Errorf("recording already active")
	}

	m.recording = true
	m.paused = false
	return nil
}

// StopRecording simulates stopping recording.
func (m *MockOBSClient) StopRecording() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStopRecording != nil {
		return "", m.ErrorOnStopRecording
	}

	if !m.connected {
		return "", fmt.Errorf("not connected to OBS")
	}

	if !m.recording {
		return "", fmt.Errorf("recording not active")
	}

	m.recording = false
	m.paused = false
	return "/recordings/test-recording.mkv", nil
}

// GetRecordingStatus returns mock recording status.
func (m *MockOBSClient) GetRecordingStatus() (*obs.RecordingStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	return &obs.RecordingStatus{
		Active:      m.recording,
		Paused:      m.paused,
		Timecode:    "00:01:30.000",
		OutputPath:  "/recordings/",
		OutputBytes: 1024000,
	}, nil
}

// PauseRecording simulates pausing recording.
func (m *MockOBSClient) PauseRecording() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnPauseRecording != nil {
		return m.ErrorOnPauseRecording
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.recording {
		return fmt.Errorf("recording not active")
	}

	if m.paused {
		return fmt.Errorf("recording already paused")
	}

	m.paused = true
	return nil
}

// ResumeRecording simulates resuming recording.
func (m *MockOBSClient) ResumeRecording() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnResumeRecording != nil {
		return m.ErrorOnResumeRecording
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.recording {
		return fmt.Errorf("recording not active")
	}

	if !m.paused {
		return fmt.Errorf("recording not paused")
	}

	m.paused = false
	return nil
}

// StartStreaming simulates starting streaming.
func (m *MockOBSClient) StartStreaming() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStartStreaming != nil {
		return m.ErrorOnStartStreaming
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if m.streaming {
		return fmt.Errorf("streaming already active")
	}

	m.streaming = true
	return nil
}

// StopStreaming simulates stopping streaming.
func (m *MockOBSClient) StopStreaming() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStopStreaming != nil {
		return m.ErrorOnStopStreaming
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.streaming {
		return fmt.Errorf("streaming not active")
	}

	m.streaming = false
	return nil
}

// GetStreamingStatus returns mock streaming status.
func (m *MockOBSClient) GetStreamingStatus() (*obs.StreamingStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	return &obs.StreamingStatus{
		Active:       m.streaming,
		Reconnecting: false,
		Timecode:     "00:05:00.000",
		TotalBytes:   5120000,
		TotalFrames:  9000,
	}, nil
}

// PressInputPropertiesButton records a button press so a test can assert one
// happened. There is nothing to observe otherwise -- the press mutates no
// settings, which is the point of it.
func (m *MockOBSClient) PressInputPropertiesButton(sourceName, propertyName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnPressInputPropertiesButton != nil {
		return m.ErrorOnPressInputPropertiesButton
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if _, err := m.world.GetSourceSettings(sourceName); err != nil {
		return fmt.Errorf("source '%s' not found", sourceName)
	}

	m.buttonPresses = append(m.buttonPresses, sourceName+":"+propertyName)
	return nil
}

// ButtonPresses returns the presses recorded so far, as "source:property".
func (m *MockOBSClient) ButtonPresses() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.buttonPresses...)
}

func (m *MockOBSClient) ToggleSourceVisibility(sceneName string, sourceID int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnToggleVisibility != nil {
		return false, m.ErrorOnToggleVisibility
	}

	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}

	// Read the current state and flip it, rather than flipping a copy held in a
	// second map. Keeping visibility in two places is how the old mock managed
	// to have two methods disagree about the same item.
	enabled, err := m.world.GetSceneItemEnabled(sceneName, sourceID)
	if err != nil {
		return false, err
	}
	if err := m.world.SetSceneItemEnabled(sceneName, sourceID, !enabled); err != nil {
		return false, err
	}
	return !enabled, nil
}

// SetInputVolume simulates setting input volume.
func (m *MockOBSClient) SetInputVolume(inputName string, volumeDb *float64, volumeMul *float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSetInputVolume != nil {
		return m.ErrorOnSetInputVolume
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	_, exists := m.inputVolumes[inputName]
	if !exists {
		return fmt.Errorf("input '%s' not found", inputName)
	}

	if volumeDb != nil {
		m.inputVolumes[inputName] = *volumeDb
	}

	return nil
}

// GetInputVolume returns mock input volume.
func (m *MockOBSClient) GetInputVolume(inputName string) (float64, float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetInputVolume != nil {
		return 0, 0, m.ErrorOnGetInputVolume
	}

	if !m.connected {
		return 0, 0, fmt.Errorf("not connected to OBS")
	}

	vol, exists := m.inputVolumes[inputName]
	if !exists {
		return 0, 0, fmt.Errorf("input '%s' not found", inputName)
	}

	return vol, 1.0, nil
}

// videoSettings is the canvas the mock reports. Caller must hold the lock.
//
// Deliberately not 1920x1080, and deliberately downscaled. A fixture that
// matches the most common assumption lets a test pass on hardcoded numbers
// instead of the reported ones -- which is exactly what the scene-designer skill
// did, and why it was wrong on a 2560x1440 canvas. The frame rate is fractional
// for the same reason: 60000/1001 catches code that reads the numerator alone
// and calls it 60000. (FB-69)
func (m *MockOBSClient) videoSettings() *obs.VideoSettings {
	return &obs.VideoSettings{
		BaseWidth:      2560,
		BaseHeight:     1440,
		OutputWidth:    1920,
		OutputHeight:   1080,
		FPSNumerator:   60000,
		FPSDenominator: 1001,
	}
}

// Helper methods for test setup

// SetScenes sets the available scenes for testing.
// SetScenes makes the world hold exactly these scenes.
//
// It adds what is missing and removes what is extra, rather than assigning over
// a list, because the scenes now have contents: replacing the slice would have
// left the items of a removed scene behind with nothing referencing them.
func (m *MockOBSClient) SetScenes(scenes []string) {
	want := map[string]bool{}
	for _, name := range scenes {
		want[name] = true
		_ = m.world.CreateScene(name) // already there is fine
	}

	existing, _, err := m.world.GetSceneList()
	if err != nil {
		return
	}
	for _, name := range existing {
		if !want[name] {
			_ = m.world.RemoveScene(name)
		}
	}
}

// SetCurrentSceneDirect sets the current scene without validation.
func (m *MockOBSClient) SetCurrentSceneDirect(scene string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentScene = scene
}

// SetRecordingState sets the recording state for testing.
func (m *MockOBSClient) SetRecordingState(recording, paused bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recording = recording
	m.paused = paused
}

// SetStreamingState sets the streaming state for testing.
func (m *MockOBSClient) SetStreamingState(streaming bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streaming = streaming
}

// AddSource adds a source to the mock data.
// AddSource adds an input to the world.
//
// OBS has no input that belongs to no scene -- CreateInput requires one, which
// is why this picks the first scene rather than inventing a placeless source the
// real client could never produce.
func (m *MockOBSClient) AddSource(input *typedefs.Input) {
	if input == nil {
		return
	}
	scenes, _, err := m.world.GetSceneList()
	if err != nil || len(scenes) == 0 {
		return
	}
	_, _ = m.world.CreateInput(scenes[0], input.InputName, input.InputKind, nil)
}

// SetSourceSettingsState seeds a source's settings directly, without going
// through the OBS operation. Named like its neighbours SetInputMuteState and
// SetInputVolumeState; it used to be called SetSourceSettings, which collided
// with the real operation once that was added. (FB-67)
// SetSourceSettingsState replaces a source's settings, creating it if needed.
func (m *MockOBSClient) SetSourceSettingsState(sourceName string, settings map[string]interface{}) {
	if err := m.world.SetSourceSettings(sourceName, settings, false); err == nil {
		return
	}
	scenes, _, err := m.world.GetSceneList()
	if err != nil || len(scenes) == 0 {
		return
	}
	if _, err := m.world.CreateInput(scenes[0], sourceName, "color_source_v3", settings); err != nil {
		return
	}
}

// SetInputMuteState sets the mute state for an input.
// SetInputMuteState sets an input's mute state in the world.
func (m *MockOBSClient) SetInputMuteState(inputName string, muted bool) {
	_ = m.world.SetInputMute(inputName, muted)
}

// SetInputVolumeState sets the volume for an input.
func (m *MockOBSClient) SetInputVolumeState(inputName string, volume float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputVolumes[inputName] = volume
}

// SetEventCallback is a no-op for the mock client since we don't need event handling in tests.
func (m *MockOBSClient) SetEventCallback(callback obs.EventCallback) {
	// No-op for mock - events are not simulated
}

// TakeSourceScreenshot simulates taking a screenshot of a source.
func (m *MockOBSClient) TakeSourceScreenshot(opts obs.ScreenshotOptions) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnTakeScreenshot != nil {
		return "", m.ErrorOnTakeScreenshot
	}

	if !m.connected {
		return "", fmt.Errorf("not connected to OBS")
	}

	// Return mock screenshot data if set, otherwise return a minimal valid base64 PNG
	if m.mockScreenshotData != "" {
		return m.mockScreenshotData, nil
	}

	// Return a minimal 1x1 transparent PNG as base64
	// This is a valid PNG that can be used in tests
	return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", nil
}

// CreateBrowserSource simulates creating a browser source in a scene.
func (m *MockOBSClient) CreateBrowserSource(sceneName, sourceName string, settings obs.BrowserSourceSettings) (int, error) {
	if m.ErrorOnCreateBrowserSource != nil {
		return 0, m.ErrorOnCreateBrowserSource
	}
	if !m.connected {
		return 0, fmt.Errorf("not connected to OBS")
	}

	// A browser source is an input of kind browser_source, created the same way
	// as any other. The old version invented its own scene item ids starting at
	// 100 to avoid colliding with the ones it had hard-coded elsewhere -- a
	// symptom of ids living in a map rather than being handed out by one place.
	return m.world.CreateInput(sceneName, sourceName, "browser_source", map[string]interface{}{
		"url":    settings.URL,
		"width":  settings.Width,
		"height": settings.Height,
		"css":    settings.CSS,
	})
}

// SetMockScreenshotData sets the screenshot data to return from TakeSourceScreenshot.
func (m *MockOBSClient) SetMockScreenshotData(data string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mockScreenshotData = data
}

// Design tool mock implementations

// GetInputKindList returns the list of available input kinds.
func (m *MockOBSClient) GetInputKindList() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetInputKindList != nil {
		return nil, m.ErrorOnGetInputKindList
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	// Return a copy
	result := make([]string, len(m.inputKinds))
	copy(result, m.inputKinds)
	return result, nil
}

// =============================================================================
// Filter mock implementations (FB-23)
// =============================================================================

// GetSourceFilterKindList returns available filter types.
func (m *MockOBSClient) GetSourceFilterKindList() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetSourceFilterKindList != nil {
		return nil, m.ErrorOnGetSourceFilterKindList
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	// Return a copy
	result := make([]string, len(m.filterKinds))
	copy(result, m.filterKinds)
	return result, nil
}

// =============================================================================
// Transition mock implementations (FB-24)
// =============================================================================

// GetSceneTransitionList returns available transitions and current transition name.
func (m *MockOBSClient) GetSceneTransitionList() ([]obs.TransitionInfo, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetSceneTransitionList != nil {
		return nil, "", m.ErrorOnGetSceneTransitionList
	}

	if !m.connected {
		return nil, "", fmt.Errorf("not connected to OBS")
	}

	// Return a copy
	result := make([]obs.TransitionInfo, len(m.transitions))
	copy(result, m.transitions)

	currentName := ""
	if m.currentTransition != nil {
		currentName = m.currentTransition.Name
	}

	return result, currentName, nil
}

// GetCurrentSceneTransition returns the current transition details.
func (m *MockOBSClient) GetCurrentSceneTransition() (*obs.TransitionDetails, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetCurrentSceneTransition != nil {
		return nil, m.ErrorOnGetCurrentSceneTransition
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	if m.currentTransition == nil {
		return nil, fmt.Errorf("no current transition set")
	}

	// Return a copy
	result := *m.currentTransition
	return &result, nil
}

// SetCurrentSceneTransition sets the current transition.
func (m *MockOBSClient) SetCurrentSceneTransition(transitionName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSetCurrentSceneTransition != nil {
		return m.ErrorOnSetCurrentSceneTransition
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	// Find the transition
	var found *obs.TransitionInfo
	for _, t := range m.transitions {
		if t.Name == transitionName {
			found = &t
			break
		}
	}

	if found == nil {
		return fmt.Errorf("transition '%s' not found", transitionName)
	}

	// Update current transition
	duration := 300
	if m.currentTransition != nil {
		duration = m.currentTransition.Duration
	}

	m.currentTransition = &obs.TransitionDetails{
		Name:         found.Name,
		Kind:         found.Kind,
		Duration:     duration,
		Configurable: found.Configurable,
		Settings:     map[string]interface{}{},
	}

	return nil
}

// SetCurrentSceneTransitionDuration sets the transition duration.
func (m *MockOBSClient) SetCurrentSceneTransitionDuration(durationMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSetCurrentSceneTransitionDuration != nil {
		return m.ErrorOnSetCurrentSceneTransitionDuration
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if m.currentTransition == nil {
		return fmt.Errorf("no current transition set")
	}

	m.currentTransition.Duration = durationMs
	return nil
}

// TriggerStudioModeTransition triggers the studio mode transition.
func (m *MockOBSClient) TriggerStudioModeTransition() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnTriggerStudioModeTransition != nil {
		return m.ErrorOnTriggerStudioModeTransition
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.studioModeEnabled {
		return fmt.Errorf("studio mode is not enabled")
	}

	// In a real implementation, this would swap preview and program scenes
	return nil
}

// Helper methods for test setup

// SetStudioModeEnabledDirect sets the studio mode state directly for testing (no error return).
func (m *MockOBSClient) SetStudioModeEnabledDirect(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.studioModeEnabled = enabled
}

// AddFilter adds a filter to a source for testing.
func (m *MockOBSClient) AddFilter(sourceName string, filter obs.FilterDetails) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sourceFilters == nil {
		m.sourceFilters = make(map[string][]obs.FilterInfo)
	}
	if m.filterDetails == nil {
		m.filterDetails = make(map[string]map[string]*obs.FilterDetails)
	}
	if m.filterDetails[sourceName] == nil {
		m.filterDetails[sourceName] = make(map[string]*obs.FilterDetails)
	}

	m.sourceFilters[sourceName] = append(m.sourceFilters[sourceName], obs.FilterInfo{
		Name:    filter.Name,
		Kind:    filter.Kind,
		Index:   filter.Index,
		Enabled: filter.Enabled,
	})
	m.filterDetails[sourceName][filter.Name] = &filter
}

// AddTransition adds a transition for testing.
func (m *MockOBSClient) AddTransition(transition obs.TransitionInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transitions = append(m.transitions, transition)
}

// SetCurrentTransitionDirect sets the current transition directly for testing.
func (m *MockOBSClient) SetCurrentTransitionDirect(transition *obs.TransitionDetails) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentTransition = transition
}

// =============================================================================
// Virtual Camera mock implementations (FB-25)
// =============================================================================

// GetVirtualCamStatus returns the virtual camera status.
func (m *MockOBSClient) GetVirtualCamStatus() (*obs.VirtualCamStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetVirtualCamStatus != nil {
		return nil, m.ErrorOnGetVirtualCamStatus
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	return &obs.VirtualCamStatus{
		Active: m.virtualCamActive,
	}, nil
}

// ToggleVirtualCam toggles the virtual camera state.
func (m *MockOBSClient) ToggleVirtualCam() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnToggleVirtualCam != nil {
		return false, m.ErrorOnToggleVirtualCam
	}

	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}

	m.virtualCamActive = !m.virtualCamActive
	return m.virtualCamActive, nil
}

// StartVirtualCam starts the virtual camera.
func (m *MockOBSClient) StartVirtualCam() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStartVirtualCam != nil {
		return m.ErrorOnStartVirtualCam
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if m.virtualCamActive {
		return fmt.Errorf("virtual camera already active")
	}

	m.virtualCamActive = true
	return nil
}

// StopVirtualCam stops the virtual camera.
func (m *MockOBSClient) StopVirtualCam() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStopVirtualCam != nil {
		return m.ErrorOnStopVirtualCam
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.virtualCamActive {
		return fmt.Errorf("virtual camera not active")
	}

	m.virtualCamActive = false
	return nil
}

// =============================================================================
// Replay Buffer mock implementations (FB-25)
// =============================================================================

// GetReplayBufferStatus returns the replay buffer status.
func (m *MockOBSClient) GetReplayBufferStatus() (*obs.ReplayBufferStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetReplayBufferStatus != nil {
		return nil, m.ErrorOnGetReplayBufferStatus
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	return &obs.ReplayBufferStatus{
		Active: m.replayBufferActive,
	}, nil
}

// ToggleReplayBuffer toggles the replay buffer state.
func (m *MockOBSClient) ToggleReplayBuffer() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnToggleReplayBuffer != nil {
		return false, m.ErrorOnToggleReplayBuffer
	}

	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}

	m.replayBufferActive = !m.replayBufferActive
	return m.replayBufferActive, nil
}

// StartReplayBuffer starts the replay buffer.
func (m *MockOBSClient) StartReplayBuffer() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStartReplayBuffer != nil {
		return m.ErrorOnStartReplayBuffer
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if m.replayBufferActive {
		return fmt.Errorf("replay buffer already active")
	}

	m.replayBufferActive = true
	return nil
}

// StopReplayBuffer stops the replay buffer.
func (m *MockOBSClient) StopReplayBuffer() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnStopReplayBuffer != nil {
		return m.ErrorOnStopReplayBuffer
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.replayBufferActive {
		return fmt.Errorf("replay buffer not active")
	}

	m.replayBufferActive = false
	return nil
}

// SaveReplayBuffer saves the current replay buffer.
func (m *MockOBSClient) SaveReplayBuffer() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSaveReplayBuffer != nil {
		return m.ErrorOnSaveReplayBuffer
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.replayBufferActive {
		return fmt.Errorf("replay buffer not active")
	}

	// In a real implementation, this would save the replay
	return nil
}

// GetLastReplayBufferReplay returns the path to the last saved replay.
func (m *MockOBSClient) GetLastReplayBufferReplay() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetLastReplayBufferReplay != nil {
		return "", m.ErrorOnGetLastReplayBufferReplay
	}

	if !m.connected {
		return "", fmt.Errorf("not connected to OBS")
	}

	return m.lastReplayPath, nil
}

// =============================================================================
// Studio Mode mock implementations (FB-26)
// =============================================================================

// GetStudioModeEnabled returns whether studio mode is enabled.
func (m *MockOBSClient) GetStudioModeEnabled() (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetStudioModeEnabled != nil {
		return false, m.ErrorOnGetStudioModeEnabled
	}

	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}

	return m.studioModeEnabled, nil
}

// SetStudioModeEnabled enables or disables studio mode.
func (m *MockOBSClient) SetStudioModeEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSetStudioModeEnabled != nil {
		return m.ErrorOnSetStudioModeEnabled
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	m.studioModeEnabled = enabled
	return nil
}

// GetCurrentPreviewScene returns the current preview scene in studio mode.
func (m *MockOBSClient) GetCurrentPreviewScene() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetCurrentPreviewScene != nil {
		return "", m.ErrorOnGetCurrentPreviewScene
	}

	if !m.connected {
		return "", fmt.Errorf("not connected to OBS")
	}

	if !m.studioModeEnabled {
		return "", fmt.Errorf("studio mode is not enabled")
	}

	return m.previewScene, nil
}

// SetCurrentPreviewScene sets the current preview scene in studio mode.
func (m *MockOBSClient) SetCurrentPreviewScene(sceneName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnSetCurrentPreviewScene != nil {
		return m.ErrorOnSetCurrentPreviewScene
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	if !m.studioModeEnabled {
		return fmt.Errorf("studio mode is not enabled")
	}

	if _, err := m.world.GetSceneByName(sceneName); err != nil {
		return fmt.Errorf("scene '%s' not found", sceneName)
	}

	m.previewScene = sceneName
	return nil
}

// =============================================================================
// Hotkey mock implementations (FB-26)
// =============================================================================

// TriggerHotkeyByName triggers a hotkey by name.
func (m *MockOBSClient) TriggerHotkeyByName(hotkeyName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnTriggerHotkeyByName != nil {
		return m.ErrorOnTriggerHotkeyByName
	}

	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}

	// Check if hotkey exists
	found := false
	for _, h := range m.hotkeys {
		if h == hotkeyName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("hotkey '%s' not found", hotkeyName)
	}

	// In a real implementation, this would trigger the hotkey action
	return nil
}

// GetHotkeyList returns the list of available hotkeys.
func (m *MockOBSClient) GetHotkeyList() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ErrorOnGetHotkeyList != nil {
		return nil, m.ErrorOnGetHotkeyList
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	// Return a copy
	result := make([]string, len(m.hotkeys))
	copy(result, m.hotkeys)
	return result, nil
}

// =============================================================================
// Helper methods for FB-25/FB-26 test setup
// =============================================================================

// SetVirtualCamState sets the virtual camera state for testing.
func (m *MockOBSClient) SetVirtualCamState(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.virtualCamActive = active
}

// SetReplayBufferState sets the replay buffer state for testing.
func (m *MockOBSClient) SetReplayBufferState(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replayBufferActive = active
}

// SetLastReplayPath sets the last replay path for testing.
func (m *MockOBSClient) SetLastReplayPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastReplayPath = path
}

// SetPreviewScene sets the preview scene for testing.
func (m *MockOBSClient) SetPreviewScene(scene string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.previewScene = scene
}

// AddHotkey adds a hotkey for testing.
func (m *MockOBSClient) AddHotkey(hotkeyName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hotkeys = append(m.hotkeys, hotkeyName)
}

// =============================================================================
// Audio device methods
// =============================================================================

// GetSpecialInputs returns the names of OBS's built-in global audio inputs.
func (m *MockOBSClient) GetSpecialInputs() (*obs.SpecialInputs, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ErrorOnGetSpecialInputs != nil {
		return nil, m.ErrorOnGetSpecialInputs
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return &obs.SpecialInputs{
		Desktop1: "Desktop Audio",
		Mic1:     "Mic/Aux",
	}, nil
}

// GetInputPropertiesItems returns the mock audio device list for any input/property combination.
func (m *MockOBSClient) GetInputPropertiesItems(inputName, propertyName string) ([]obs.AudioDevice, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ErrorOnGetInputPropertiesItems != nil {
		return nil, m.ErrorOnGetInputPropertiesItems
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	result := make([]obs.AudioDevice, len(m.audioDevices))
	copy(result, m.audioDevices)
	return result, nil
}

// CreateAudioInput creates an audio capture source using the existing CreateInput mock.
func (m *MockOBSClient) CreateAudioInput(sceneName, sourceName, inputKind, deviceID string) (int, error) {
	if m.ErrorOnCreateAudioInput != nil {
		return 0, m.ErrorOnCreateAudioInput
	}
	return m.CreateInput(sceneName, sourceName, inputKind, map[string]interface{}{
		"device_id": deviceID,
	})
}

// SetAudioDevices replaces the mock audio device list for testing.
func (m *MockOBSClient) SetAudioDevices(devices []obs.AudioDevice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioDevices = devices
}

// Advanced Scene Switcher mock implementations
// =============================================================================
//
// ASS state is appended here rather than added to the main struct so the file
// stays diff-friendly. Tests that need to assert which macros ran can read
// LastASSMacro / ASSMessagesSent / ASSVariablesSet after exercising handlers.
// Errors can be injected via the dedicated fields below.

// ASS recording / error-injection state. Initialised lazily on first use.
type assMockState struct {
	ASSMessagesSent      []string
	LastASSMacro         string
	LastASSMacroVarCount int
	ASSVariablesSet      []obs.ASSVariable

	ErrorOnASSSendMessage  error
	ErrorOnASSRunMacro     error
	ErrorOnASSSetVariables error
}

// assState is a package-level map keyed by mock instance pointer. This avoids
// edits to MockOBSClient's struct definition while still letting each mock
// instance hold isolated ASS state.
var (
	assStateMu sync.Mutex
	assState   = map[*MockOBSClient]*assMockState{}
)

func (m *MockOBSClient) assMockState() *assMockState {
	assStateMu.Lock()
	defer assStateMu.Unlock()
	s, ok := assState[m]
	if !ok {
		s = &assMockState{}
		assState[m] = s
	}
	return s
}

// SetASSError configures an error to return on the next call to the named
// ASS method ("send_message", "run_macro", or "set_variables"). Unknown
// method names are ignored.
func (m *MockOBSClient) SetASSError(method string, err error) {
	s := m.assMockState()
	switch method {
	case "send_message":
		s.ErrorOnASSSendMessage = err
	case "run_macro":
		s.ErrorOnASSRunMacro = err
	case "set_variables":
		s.ErrorOnASSSetVariables = err
	}
}

// GetASSCalls returns a snapshot of recorded ASS calls for assertion.
func (m *MockOBSClient) GetASSCalls() (messages []string, lastMacro string, lastVarCount int, varsSet []obs.ASSVariable) {
	s := m.assMockState()
	return append([]string(nil), s.ASSMessagesSent...), s.LastASSMacro, s.LastASSMacroVarCount, append([]obs.ASSVariable(nil), s.ASSVariablesSet...)
}

// ASSSendMessage records the message (or returns the injected error).
func (m *MockOBSClient) ASSSendMessage(message string) error {
	s := m.assMockState()
	if s.ErrorOnASSSendMessage != nil {
		return s.ErrorOnASSSendMessage
	}
	s.ASSMessagesSent = append(s.ASSMessagesSent, message)
	return nil
}

// ASSRunMacro records the macro name and any variables passed.
func (m *MockOBSClient) ASSRunMacro(name string, variables []obs.ASSVariable) error {
	s := m.assMockState()
	if s.ErrorOnASSRunMacro != nil {
		return s.ErrorOnASSRunMacro
	}
	s.LastASSMacro = name
	s.LastASSMacroVarCount = len(variables)
	return nil
}

// ASSSetVariables records each variable update.
func (m *MockOBSClient) ASSSetVariables(variables []obs.ASSVariable) error {
	s := m.assMockState()
	if s.ErrorOnASSSetVariables != nil {
		return s.ErrorOnASSSetVariables
	}
	s.ASSVariablesSet = append(s.ASSVariablesSet, variables...)
	return nil
}

// VendorCall records one CallVendorRequest, so a test can assert what was sent
// rather than only what came back. (FB-77)
type VendorCall struct {
	VendorName  string
	RequestType string
	RequestData map[string]any
}

// CallVendorRequest simulates a vendor request.
//
// Vendors cannot be enumerated in OBS, so an unknown vendor is an error rather
// than an empty result -- the only way to learn a plugin is missing is to ask
// and be refused, and a test double that quietly succeeded would hide that.
func (m *MockOBSClient) CallVendorRequest(vendorName, requestType string, data map[string]any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ErrorOnCallVendorRequest != nil {
		return nil, m.ErrorOnCallVendorRequest
	}

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	m.vendorCalls = append(m.vendorCalls, VendorCall{
		VendorName: vendorName, RequestType: requestType, RequestData: data,
	})

	if m.vendorResponses == nil {
		m.vendorResponses = map[string]map[string]any{}
	}
	if resp, ok := m.vendorResponses[vendorName+"/"+requestType]; ok {
		return resp, nil
	}

	// A vendor the test seeded at all is installed; one it did not is not.
	for key := range m.vendorResponses {
		if strings.HasPrefix(key, vendorName+"/") {
			return map[string]any{}, nil
		}
	}
	if vendorName == "obs-browser" {
		// Always present: obs-browser registers its vendor in every OBS build
		// that ships the browser source, which is all of them.
		return map[string]any{}, nil
	}

	return nil, fmt.Errorf("no vendor named '%s' is registered; the plugin may not be installed", vendorName)
}

// SetVendorResponse seeds what a vendor request returns.
func (m *MockOBSClient) SetVendorResponse(vendorName, requestType string, response map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vendorResponses == nil {
		m.vendorResponses = map[string]map[string]any{}
	}
	m.vendorResponses[vendorName+"/"+requestType] = response
}

// VendorCalls returns the vendor requests made so far.
func (m *MockOBSClient) VendorCalls() []VendorCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]VendorCall(nil), m.vendorCalls...)
}
