package testutil

import (
	"fmt"

	"github.com/andreykaipov/goobs/api/typedefs"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// MockOBSClient's OBS state lives in obstest.Fake.
//
// There were two doubles in this repo and nothing forced them to agree. Running
// the shared behavioural contract against MockOBSClient settled how far apart
// they had drifted: **all thirty rows failed**. Not a handful of edge cases --
// every behaviour the contract checks, on the double that ~40 mcp tests rely on.
// The cost had already been paid several times over: SceneSource.Visible
// (FB-60) was populated by both doubles while the real client never set it, so
// every test agreed with a client that was wrong; FB-64 found three ways this
// mock contradicted itself; FB-71 found CreateInput making an input ListSources
// could not see.
//
// So the state the contract covers is delegated to the one world that is
// checked against a real OBS. What stays here is what is genuinely this mock's
// own: connection state, the ErrorOnX injection points, and the
// recording/streaming flags that have no counterpart in the fake.
//
// Each method below keeps its error-injection check and its connected check,
// then defers to the world. The generated shape is deliberate -- a hand-written
// wrapper is a place for the two to diverge again.

func (m *MockOBSClient) CreateInput(sceneName, sourceName, inputKind string, settings map[string]interface{}) (int, error) {
	if m.ErrorOnCreateInput != nil {
		return 0, m.ErrorOnCreateInput
	}
	if !m.connected {
		return 0, fmt.Errorf("not connected to OBS")
	}
	return m.world.CreateInput(sceneName, sourceName, inputKind, settings)
}

func (m *MockOBSClient) CreateScene(name string) error {
	if m.ErrorOnCreateScene != nil {
		return m.ErrorOnCreateScene
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.CreateScene(name)
}

func (m *MockOBSClient) CreateSceneItem(sceneName, sourceName string, enabled bool) (int, error) {
	if m.ErrorOnCreateSceneItem != nil {
		return 0, m.ErrorOnCreateSceneItem
	}
	if !m.connected {
		return 0, fmt.Errorf("not connected to OBS")
	}
	return m.world.CreateSceneItem(sceneName, sourceName, enabled)
}

func (m *MockOBSClient) CreateSourceFilter(sourceName, filterName, filterKind string, settings map[string]interface{}) error {
	if m.ErrorOnCreateSourceFilter != nil {
		return m.ErrorOnCreateSourceFilter
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.CreateSourceFilter(sourceName, filterName, filterKind, settings)
}

func (m *MockOBSClient) DuplicateSceneItem(sceneName string, sceneItemID int, destScene string) (int, error) {
	if m.ErrorOnDuplicateSceneItem != nil {
		return 0, m.ErrorOnDuplicateSceneItem
	}
	if !m.connected {
		return 0, fmt.Errorf("not connected to OBS")
	}
	return m.world.DuplicateSceneItem(sceneName, sceneItemID, destScene)
}

func (m *MockOBSClient) GetGroupList() ([]string, error) {
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetGroupList()
}

func (m *MockOBSClient) GetGroupSceneItemList(groupName string) ([]obs.SceneSource, error) {
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetGroupSceneItemList(groupName)
}

func (m *MockOBSClient) GetInputDefaultSettings(inputKind string) (map[string]interface{}, error) {
	if m.ErrorOnGetInputDefaultSettings != nil {
		return nil, m.ErrorOnGetInputDefaultSettings
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetInputDefaultSettings(inputKind)
}

func (m *MockOBSClient) GetInputMute(inputName string) (bool, error) {
	if m.ErrorOnGetInputMute != nil {
		return false, m.ErrorOnGetInputMute
	}
	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetInputMute(inputName)
}

func (m *MockOBSClient) GetOBSStatus() (*obs.OBSStatus, error) {
	if m.ErrorOnGetOBSStatus != nil {
		return nil, m.ErrorOnGetOBSStatus
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetOBSStatus()
}

func (m *MockOBSClient) GetSceneByName(name string) (*obs.Scene, error) {
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSceneByName(name)
}

func (m *MockOBSClient) GetSceneItemEnabled(sceneName string, sceneItemID int) (bool, error) {
	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSceneItemEnabled(sceneName, sceneItemID)
}

func (m *MockOBSClient) GetSceneItemTransform(sceneName string, sceneItemID int) (*obs.SceneItemTransform, error) {
	if m.ErrorOnGetSceneItemTransform != nil {
		return nil, m.ErrorOnGetSceneItemTransform
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSceneItemTransform(sceneName, sceneItemID)
}

func (m *MockOBSClient) GetSceneList() ([]string, string, error) {
	if m.ErrorOnGetSceneList != nil {
		return nil, "", m.ErrorOnGetSceneList
	}
	if !m.connected {
		return nil, "", fmt.Errorf("not connected to OBS")
	}

	// The current scene is the mock's own: the world models the scene graph,
	// not the frontend's notion of what is on air.
	scenes, _, err := m.world.GetSceneList()
	return scenes, m.currentScene, err
}

func (m *MockOBSClient) GetSourceFilter(sourceName, filterName string) (*obs.FilterDetails, error) {
	if m.ErrorOnGetSourceFilter != nil {
		return nil, m.ErrorOnGetSourceFilter
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSourceFilter(sourceName, filterName)
}

func (m *MockOBSClient) GetSourceFilterList(sourceName string) ([]obs.FilterInfo, error) {
	if m.ErrorOnGetSourceFilterList != nil {
		return nil, m.ErrorOnGetSourceFilterList
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSourceFilterList(sourceName)
}

func (m *MockOBSClient) GetSourceSettings(sourceName string) (map[string]interface{}, error) {
	if m.ErrorOnGetSourceSettings != nil {
		return nil, m.ErrorOnGetSourceSettings
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSourceSettings(sourceName)
}

func (m *MockOBSClient) GetVideoSettings() (*obs.VideoSettings, error) {
	if m.ErrorOnGetVideoSettings != nil {
		return nil, m.ErrorOnGetVideoSettings
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetVideoSettings()
}

func (m *MockOBSClient) ListSources() ([]*typedefs.Input, error) {
	if m.ErrorOnListSources != nil {
		return nil, m.ErrorOnListSources
	}
	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}
	return m.world.ListSources()
}

func (m *MockOBSClient) RemoveScene(name string) error {
	if m.ErrorOnRemoveScene != nil {
		return m.ErrorOnRemoveScene
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.RemoveScene(name)
}

func (m *MockOBSClient) RemoveSceneItem(sceneName string, sceneItemID int) error {
	if m.ErrorOnRemoveSceneItem != nil {
		return m.ErrorOnRemoveSceneItem
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.RemoveSceneItem(sceneName, sceneItemID)
}

func (m *MockOBSClient) RemoveSourceFilter(sourceName, filterName string) error {
	if m.ErrorOnRemoveSourceFilter != nil {
		return m.ErrorOnRemoveSourceFilter
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.RemoveSourceFilter(sourceName, filterName)
}

func (m *MockOBSClient) SetInputMute(inputName string, muted bool) error {
	if m.ErrorOnSetInputMute != nil {
		return m.ErrorOnSetInputMute
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetInputMute(inputName, muted)
}

func (m *MockOBSClient) SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error {
	if m.ErrorOnSetSceneItemEnabled != nil {
		return m.ErrorOnSetSceneItemEnabled
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSceneItemEnabled(sceneName, sceneItemID, enabled)
}

func (m *MockOBSClient) SetSceneItemIndex(sceneName string, sceneItemID int, index int) error {
	if m.ErrorOnSetSceneItemIndex != nil {
		return m.ErrorOnSetSceneItemIndex
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSceneItemIndex(sceneName, sceneItemID, index)
}

func (m *MockOBSClient) SetSceneItemLocked(sceneName string, sceneItemID int, locked bool) error {
	if m.ErrorOnSetSceneItemLocked != nil {
		return m.ErrorOnSetSceneItemLocked
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSceneItemLocked(sceneName, sceneItemID, locked)
}

func (m *MockOBSClient) SetSceneItemTransform(sceneName string, sceneItemID int, transform *obs.SceneItemTransform) error {
	if m.ErrorOnSetSceneItemTransform != nil {
		return m.ErrorOnSetSceneItemTransform
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSceneItemTransform(sceneName, sceneItemID, transform)
}

func (m *MockOBSClient) SetSourceFilterEnabled(sourceName, filterName string, enabled bool) error {
	if m.ErrorOnSetSourceFilterEnabled != nil {
		return m.ErrorOnSetSourceFilterEnabled
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSourceFilterEnabled(sourceName, filterName, enabled)
}

func (m *MockOBSClient) SetSourceFilterSettings(sourceName, filterName string, settings map[string]interface{}, overlay bool) error {
	if m.ErrorOnSetSourceFilterSettings != nil {
		return m.ErrorOnSetSourceFilterSettings
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSourceFilterSettings(sourceName, filterName, settings, overlay)
}

func (m *MockOBSClient) SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error {
	if m.ErrorOnSetSourceSettings != nil {
		return m.ErrorOnSetSourceSettings
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.SetSourceSettings(sourceName, settings, overlay)
}

func (m *MockOBSClient) ToggleInputMute(inputName string) error {
	if m.ErrorOnToggleInputMute != nil {
		return m.ErrorOnToggleInputMute
	}
	if !m.connected {
		return fmt.Errorf("not connected to OBS")
	}
	return m.world.ToggleInputMute(inputName)
}

func (m *MockOBSClient) GetSceneItemLocked(sceneName string, sceneItemID int) (bool, error) {
	if m.ErrorOnGetSceneItemLocked != nil {
		return false, m.ErrorOnGetSceneItemLocked
	}
	if !m.connected {
		return false, fmt.Errorf("not connected to OBS")
	}
	return m.world.GetSceneItemLocked(sceneName, sceneItemID)
}
