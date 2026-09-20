package obstest

import (
	"fmt"
	"sync"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Fake is an in-memory stand-in for the OBS client.
//
// State lives in one place rather than in a map per accessor. The existing
// MockOBSClient keeps scene items, their transforms and their lock states in
// three unrelated maps, which lets two of its methods disagree about the same
// item -- a shape of bug the real client cannot have.
type Fake struct {
	mu    sync.Mutex
	world world
}

type world struct {
	transforms map[itemKey]obs.SceneItemTransform
}

type itemKey struct {
	scene string
	id    int
}

// NewFake returns a Fake with an empty world.
func NewFake() *Fake {
	return &Fake{world: world{transforms: map[itemKey]obs.SceneItemTransform{}}}
}

func (f *Fake) GetSceneItemTransform(sceneName string, sceneItemID int) (*obs.SceneItemTransform, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	t, ok := f.world.transforms[itemKey{sceneName, sceneItemID}]
	if !ok {
		return nil, fmt.Errorf("scene item %d not found in scene %q", sceneItemID, sceneName)
	}
	// Return a copy: a caller mutating what it read must not alter stored state.
	return &t, nil
}

func (f *Fake) SetSceneItemTransform(sceneName string, sceneItemID int, transform *obs.SceneItemTransform) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := itemKey{sceneName, sceneItemID}
	stored := *transform

	// OBS derives these from the source and ignores them on a write, so keep
	// whatever the item already had rather than accepting the caller's values.
	if prev, ok := f.world.transforms[key]; ok {
		stored.Width, stored.Height = prev.Width, prev.Height
		stored.SourceWidth, stored.SourceHeight = prev.SourceWidth, prev.SourceHeight
	} else {
		stored.Width, stored.Height = 0, 0
		stored.SourceWidth, stored.SourceHeight = 0, 0
	}

	f.world.transforms[key] = stored
	return nil
}
