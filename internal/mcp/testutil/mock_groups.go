package testutil

import (
	"fmt"
	"sort"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Groups, as the mock sees them.
//
// A group is a container that reports the same sourceType as a nested scene and
// is told apart only by isGroup, and whose contents open through
// GetGroupSceneItemList alone -- GetSceneItemList refuses it with
// InvalidResourceType (602). The mock models that refusal rather than being
// permissive about it, because a double that accepts what OBS rejects is how
// FB-60 and FB-79 both got shipped.
//
// obs-websocket has no CreateGroup, so nothing here pretends a group can be
// made over the wire; SetGroup seeds one the way a collection file would.

// SetGroup declares a group and its contents.
func (m *MockOBSClient) SetGroup(name string, items []obs.SceneSource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.groups == nil {
		m.groups = map[string][]obs.SceneSource{}
	}
	m.groups[name] = items
}

// GetGroupList returns the names of every group.
func (m *MockOBSClient) GetGroupList() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	out := make([]string, 0, len(m.groups))
	for name := range m.groups {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// GetGroupSceneItemList returns a group's contents, and refuses a scene.
func (m *MockOBSClient) GetGroupSceneItemList(groupName string) ([]obs.SceneSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return nil, fmt.Errorf("not connected to OBS")
	}

	items, ok := m.groups[groupName]
	if !ok {
		// obs-websocket distinguishes these, and so does the fake: a name that
		// is a scene gets "is scene", a name that is nothing gets not-found.
		for _, scene := range m.scenes {
			if scene == groupName {
				return nil, fmt.Errorf("the specified source is not a group. (is scene): %q", groupName)
			}
		}
		return nil, fmt.Errorf("group %q not found", groupName)
	}
	return append([]obs.SceneSource(nil), items...), nil
}
