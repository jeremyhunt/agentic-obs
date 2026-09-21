package testutil

import (
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

// SetGroup declares a group and places the named sources in it.
//
// obs-websocket has no CreateGroup, so a group can only be seeded. The contents
// are placed through the world rather than stored beside it, which is what makes
// GetGroupSceneItemList and GetSceneByName agree about them.
func (m *MockOBSClient) SetGroup(name string, items []obs.SceneSource) {
	if err := m.world.CreateGroup(name); err != nil {
		return // already declared
	}
	for _, item := range items {
		if _, err := m.world.CreateInput(name, item.Name, item.Type, nil); err != nil {
			// Already an input somewhere: place the existing one instead.
			_, _ = m.world.CreateSceneItem(name, item.Name, true)
		}
	}
}
