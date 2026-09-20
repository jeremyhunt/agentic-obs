package obstest

import (
	"testing"
)

// TestFakeSatisfiesContract runs the shared behavioural contract against the
// in-memory fake. internal/obs/live_test.go runs the identical contract against a
// real OBS, which is what stops the fake drifting into being more agreeable than
// the thing it stands in for. (FB-58)
func TestFakeSatisfiesContract(t *testing.T) {
	RunContract(t, func(t *testing.T) (SceneItemClient, Fixture) {
		return NewFake(), Fixture{SceneName: "Scene 1", SceneItemID: 1}
	})
}
