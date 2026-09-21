package testutil_test

import (
	"testing"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/obs/obstest"
)

// There are two test doubles for OBS in this repo, and nothing has ever forced
// them to agree.
//
// obstest.Fake models one world and is checked against a real OBS by the
// contract suite. MockOBSClient is older, keeps its state in a couple of dozen
// unrelated maps, and is what ~40 mcp tests run against. The cost of that has
// been paid repeatedly: SceneSource.Visible (FB-60) was populated by *both*
// doubles while the real client never set it, so every test agreed with a
// client that was wrong; FB-64 found three ways MockOBSClient contradicted
// itself; FB-71 found CreateInput making an input ListSources could not see.
//
// Folding one onto the other is the eventual fix and it is a large change. This
// is the cheap half of the benefit: run the same behavioural contract against
// MockOBSClient that the fake and a live OBS already pass, so a disagreement is
// a failing test rather than a defect discovered from a broken stream.
func TestMockSatisfiesTheSameContract(t *testing.T) {
	obstest.RunContract(t, func(t *testing.T) (obstest.ContractClient, obstest.Fixture) {
		mock := testutil.NewMockOBSClient()

		// Unlike the fake, this mock refuses every call until connected -- it
		// models the client, not just the OBS state behind it.
		if err := mock.Connect(); err != nil {
			t.Fatalf("Connect: %v", err)
		}

		const scene, source, kind = "Contract Scene", "contract-item", "color_source_v3"
		if err := mock.CreateScene(scene); err != nil {
			t.Fatalf("CreateScene: %v", err)
		}
		id, err := mock.CreateInput(scene, source, kind, nil)
		if err != nil {
			t.Fatalf("CreateInput: %v", err)
		}

		// A group too, so the group rows run rather than skipping. The mock can
		// seed one where obs-websocket cannot create one at all.
		const group = "Contract Group"
		mock.SetGroup(group, []obs.SceneSource{{Name: group + "-child", Type: "color_source_v3"}})
		groupItem, err := mock.CreateSceneItem(scene, group, true)
		if err != nil {
			t.Fatalf("placing the group: %v", err)
		}

		return mock, obstest.Fixture{
			SceneName:   scene,
			SourceName:  source,
			SourceKind:  kind,
			SceneItemID: id,
			GroupName:   group,
			GroupItemID: groupItem,
		}
	})
}
