package obstest

import (
	"testing"
)

// TestFakeSatisfiesContract runs the shared behavioural contract against the
// in-memory fake. internal/obs/live_test.go runs the identical contract against a
// real OBS, which is what stops the fake drifting into being more agreeable than
// the thing it stands in for. (FB-58)
//
// The fixture is built exactly as the live suite builds its own -- create a
// scene, then create an input in it -- so the two sides start from the same
// state rather than the fake starting from a convenient one.
func TestFakeSatisfiesContract(t *testing.T) {
	RunContract(t, func(t *testing.T) (ContractClient, Fixture) {
		f := NewFake()

		const scene, source, kind = "Contract Scene", "contract-item", "color_source_v3"
		if err := f.CreateScene(scene); err != nil {
			t.Fatalf("CreateScene: %v", err)
		}
		id, err := f.CreateInput(scene, source, kind, nil)
		if err != nil {
			t.Fatalf("CreateInput: %v", err)
		}

		return f, Fixture{SceneName: scene, SourceName: source, SourceKind: kind, SceneItemID: id}
	})
}
