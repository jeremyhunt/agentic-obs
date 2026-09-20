package obs

import "github.com/andreykaipov/goobs/api/typedefs"

// Role interfaces over the OBS client.
//
// `mcp.OBSClient` was one flat list of 76 methods, which every consumer had to
// accept whole: the automation executor declared its own near-copy to avoid it,
// and the two drifted (the copy's comment still claimed obs-websocket had no
// visibility setter long after it did). A consumer that takes a role states what
// it actually touches, and a new capability adds a method to one role rather
// than a line to a list nobody can read.
//
// The roles are grouped the way obs-websocket groups its requests, so a method
// has one obvious home. Splitting reads from writes matters more than it looks:
// it is what lets a future planner or diff take a read-only client and be unable
// to mutate anything by accident.
//
// Adding a method here is the only way to widen `mcp.OBSClient` --
// TestOBSClientIsExactlyTheUnionOfRoles fails if one is declared inline instead.

// Connector covers the websocket connection's lifecycle and health.
type Connector interface {
	Connect() error
	Disconnect() error
	Close() error
	IsConnected() bool
	GetConnectionStatus() (ConnectionStatus, error)
	HealthCheck() error
}

// SceneReader reads the scene list and a scene's contents.
type SceneReader interface {
	GetSceneList() ([]string, string, error)
	GetSceneByName(name string) (*Scene, error)
}

// SceneWriter creates, removes and switches scenes.
type SceneWriter interface {
	SetCurrentScene(name string) error
	CreateScene(name string) error
	RemoveScene(name string) error
}

// SceneItemReader reads one placement's state.
type SceneItemReader interface {
	GetSceneItemTransform(sceneName string, sceneItemID int) (*SceneItemTransform, error)
	GetSceneItemEnabled(sceneName string, sceneItemID int) (bool, error)
	GetSceneItemLocked(sceneName string, sceneItemID int) (bool, error)
}

// SceneItemWriter mutates placements: where they sit, whether they show, and
// whether they exist at all.
type SceneItemWriter interface {
	SetSceneItemTransform(sceneName string, sceneItemID int, transform *SceneItemTransform) error
	SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error
	SetSceneItemIndex(sceneName string, sceneItemID int, index int) error
	SetSceneItemLocked(sceneName string, sceneItemID int, locked bool) error
	ToggleSourceVisibility(sceneName string, sourceID int) (bool, error)
	DuplicateSceneItem(sceneName string, sceneItemID int, destScene string) (int, error)
	RemoveSceneItem(sceneName string, sceneItemID int) error
}

// InputReader reads source objects and what they can be configured with.
type InputReader interface {
	ListSources() ([]*typedefs.Input, error)
	GetSourceSettings(sourceName string) (map[string]interface{}, error)
	GetInputDefaultSettings(inputKind string) (map[string]interface{}, error)
	GetInputKindList() ([]string, error)
	GetSpecialInputs() (*SpecialInputs, error)
	GetInputPropertiesItems(inputName, propertyName string) ([]AudioDevice, error)
}

// InputConfigurer creates and configures source objects.
type InputConfigurer interface {
	CreateInput(sceneName, sourceName, inputKind string, settings map[string]interface{}) (int, error)
	CreateBrowserSource(sceneName, sourceName string, settings BrowserSourceSettings) (int, error)
	CreateAudioInput(sceneName, sourceName, inputKind, deviceID string) (int, error)
	SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error
	PressInputPropertiesButton(sourceName, propertyName string) error
}

// AudioController covers mute and volume on an input.
type AudioController interface {
	GetInputMute(inputName string) (bool, error)
	SetInputMute(inputName string, muted bool) error
	ToggleInputMute(inputName string) error
	SetInputVolume(inputName string, volumeDb *float64, volumeMul *float64) error
	GetInputVolume(inputName string) (float64, float64, error)
}

// FilterManager covers filters attached to a source.
type FilterManager interface {
	GetSourceFilterList(sourceName string) ([]FilterInfo, error)
	GetSourceFilter(sourceName, filterName string) (*FilterDetails, error)
	CreateSourceFilter(sourceName, filterName, filterKind string, settings map[string]interface{}) error
	RemoveSourceFilter(sourceName, filterName string) error
	SetSourceFilterEnabled(sourceName, filterName string, enabled bool) error
	SetSourceFilterSettings(sourceName, filterName string, settings map[string]interface{}, overlay bool) error
	GetSourceFilterKindList() ([]string, error)
}

// RecordingController drives the recording output.
type RecordingController interface {
	StartRecording() error
	StopRecording() (string, error)
	GetRecordingStatus() (*RecordingStatus, error)
	PauseRecording() error
	ResumeRecording() error
}

// StreamingController drives the streaming output.
type StreamingController interface {
	StartStreaming() error
	StopStreaming() error
	GetStreamingStatus() (*StreamingStatus, error)
}

// VirtualCamController drives the virtual camera output.
type VirtualCamController interface {
	GetVirtualCamStatus() (*VirtualCamStatus, error)
	ToggleVirtualCam() (bool, error)
	StartVirtualCam() error
	StopVirtualCam() error
}

// ReplayBufferController drives the replay buffer output.
type ReplayBufferController interface {
	GetReplayBufferStatus() (*ReplayBufferStatus, error)
	ToggleReplayBuffer() (bool, error)
	StartReplayBuffer() error
	StopReplayBuffer() error
	SaveReplayBuffer() error
	GetLastReplayBufferReplay() (string, error)
}

// OutputController is every output OBS can run. It is the composition rather
// than a role in its own right: the four outputs are independent, and a consumer
// that only records should not have to accept the replay buffer to say so.
type OutputController interface {
	RecordingController
	StreamingController
	VirtualCamController
	ReplayBufferController
}

// StudioController covers studio mode and the preview scene.
type StudioController interface {
	GetStudioModeEnabled() (bool, error)
	SetStudioModeEnabled(enabled bool) error
	GetCurrentPreviewScene() (string, error)
	SetCurrentPreviewScene(sceneName string) error
}

// TransitionController covers scene transitions.
//
// TriggerStudioModeTransition lives here rather than on StudioController because
// it performs a transition; studio mode only decides what it transitions from.
type TransitionController interface {
	GetSceneTransitionList() ([]TransitionInfo, string, error)
	GetCurrentSceneTransition() (*TransitionDetails, error)
	SetCurrentSceneTransition(transitionName string) error
	SetCurrentSceneTransitionDuration(durationMs int) error
	TriggerStudioModeTransition() error
}

// HotkeyTrigger fires hotkeys OBS already has bound. Registering new ones is not
// reachable over obs-websocket at all.
type HotkeyTrigger interface {
	TriggerHotkeyByName(hotkeyName string) error
	GetHotkeyList() ([]string, error)
}

// Screenshotter captures a source or scene as an image.
type Screenshotter interface {
	TakeSourceScreenshot(opts ScreenshotOptions) (string, error)
}

// StatusReader reports aggregate OBS state.
type StatusReader interface {
	GetOBSStatus() (*OBSStatus, error)
}

// ScenePresetOperator captures and re-applies source visibility for a scene.
//
// Scheduled to leave the client: a preset is a scene spec with everything but
// visibility masked out, so these become a planner concern rather than two more
// transport methods.
type ScenePresetOperator interface {
	CaptureSceneState(sceneName string) ([]SourceState, error)
	ApplyScenePreset(sceneName string, sources []SourceState) error
}

// AdvancedSceneSwitcherController talks to the Advanced Scene Switcher plugin
// over its websocket vendor. All three are fire-and-forget -- ASS returns no
// useful data, so the wrappers only surface errors.
type AdvancedSceneSwitcherController interface {
	ASSSendMessage(message string) error
	ASSRunMacro(name string, variables []ASSVariable) error
	ASSSetVariables(variables []ASSVariable) error
}

// EventSource delivers OBS events to a callback.
type EventSource interface {
	SetEventCallback(callback EventCallback)
}

// VendorCaller reaches a plugin's websocket vendor.
//
// This is the general form of what AdvancedSceneSwitcherController does for one
// vendor, and it is the extension channel for every other: obs-browser registers
// a vendor too, which is a supported agent-to-overlay path needing no new OBS
// code. The client has had this method all along; nothing exposes it yet, so it
// is deliberately not embedded in mcp.OBSClient until a tool needs it.
type VendorCaller interface {
	CallVendorRequest(vendorName, requestType string, data map[string]any) (map[string]any, error)
}

// Compile-time proof that the real client satisfies every role. Without these a
// role could drift from the client and only fail where it was composed.
var (
	_ Connector                       = (*Client)(nil)
	_ SceneReader                     = (*Client)(nil)
	_ SceneWriter                     = (*Client)(nil)
	_ SceneItemReader                 = (*Client)(nil)
	_ SceneItemWriter                 = (*Client)(nil)
	_ InputReader                     = (*Client)(nil)
	_ InputConfigurer                 = (*Client)(nil)
	_ AudioController                 = (*Client)(nil)
	_ FilterManager                   = (*Client)(nil)
	_ OutputController                = (*Client)(nil)
	_ StudioController                = (*Client)(nil)
	_ TransitionController            = (*Client)(nil)
	_ HotkeyTrigger                   = (*Client)(nil)
	_ Screenshotter                   = (*Client)(nil)
	_ StatusReader                    = (*Client)(nil)
	_ ScenePresetOperator             = (*Client)(nil)
	_ AdvancedSceneSwitcherController = (*Client)(nil)
	_ EventSource                     = (*Client)(nil)
	_ VendorCaller                    = (*Client)(nil)
)
