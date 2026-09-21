package mcp

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scene preset is a list of which sources are visible and nothing else, which
// is exactly a scene spec restricted to one aspect. Routing it through the same
// reconciler gives it a dry run and a per-op report, and stops there being two
// pieces of code that decide what "apply" means.

func TestPresetToSpecGivesEachPlacementItsOwnOccurrence(t *testing.T) {
	// A source can be placed twice in one scene -- jurmiey_avatar is, in Game --
	// so a preset of that scene has two entries under one name. Addressing them
	// by name alone collapses both onto whichever placement the lookup found
	// last, and the other keeps whatever state it had.
	spec := presetToSpec("Scene 1", []storage.SourceState{
		{Name: "Webcam", Visible: false},
		{Name: "Text", Visible: true},
		{Name: "Webcam", Visible: true},
	})

	require.Len(t, spec.Items, 3)
	assert.Equal(t, "Scene 1", spec.Scene)

	assert.Equal(t, "Webcam", spec.Items[0].Source)
	assert.Equal(t, 0, spec.Items[0].Occurrence)
	assert.False(t, spec.Items[0].Enabled)

	assert.Equal(t, "Text", spec.Items[1].Source)
	assert.Equal(t, 0, spec.Items[1].Occurrence, "a different source starts its own count")

	assert.Equal(t, "Webcam", spec.Items[2].Source)
	assert.Equal(t, 1, spec.Items[2].Occurrence)
	assert.True(t, spec.Items[2].Enabled)
}

func TestApplyScenePresetAddressesBothPlacementsOfOneSource(t *testing.T) {
	server, mock, db := testServerWithStorage(t)

	// Webcam is scene item 1 in Scene 1; place it a second time.
	second, err := mock.CreateSceneItem("Scene 1", "Webcam", true)
	require.NoError(t, err)

	_, err = db.CreateScenePreset(context.Background(), storage.ScenePreset{
		Name:      "Two Webcams",
		SceneName: "Scene 1",
		Sources: []storage.SourceState{
			{Name: "Webcam", Visible: false},
			{Name: "Webcam", Visible: true},
		},
	})
	require.NoError(t, err)

	_, _, err = server.handleApplyScenePreset(context.Background(), nil,
		ApplyPresetInput{PresetName: "Two Webcams"})
	require.NoError(t, err)

	scene, err := mock.GetSceneByName("Scene 1")
	require.NoError(t, err)
	for _, src := range scene.Sources {
		switch src.ID {
		case 1:
			assert.False(t, src.Enabled, "the first placement was not addressed")
		case second:
			assert.True(t, src.Enabled, "the second placement was not addressed")
		}
	}
}

func TestApplyScenePresetDryRunChangesNothing(t *testing.T) {
	server, mock, db := testServerWithStorage(t)

	_, err := db.CreateScenePreset(context.Background(), storage.ScenePreset{
		Name:      "Hide Webcam",
		SceneName: "Scene 1",
		Sources:   []storage.SourceState{{Name: "Webcam", Visible: false}},
	})
	require.NoError(t, err)

	dry := true
	_, result, err := server.handleApplyScenePreset(context.Background(), nil,
		ApplyPresetInput{PresetName: "Hide Webcam", DryRun: &dry})
	require.NoError(t, err)

	res := result.(map[string]interface{})
	assert.Equal(t, true, res["dry_run"])

	scene, _ := mock.GetSceneByName("Scene 1")
	for _, src := range scene.Sources {
		if src.ID == 1 {
			assert.True(t, src.Enabled, "a dry run hid the source anyway")
		}
	}
}

func TestApplyScenePresetTouchesNothingButVisibility(t *testing.T) {
	// The preset says which sources are shown. A transform somebody moved on
	// purpose, or a setting somebody changed, is none of its business.
	server, mock, db := testServerWithStorage(t)

	transform, err := mock.GetSceneItemTransform("Scene 1", 1)
	require.NoError(t, err)
	transform.PositionX = 777
	require.NoError(t, mock.SetSceneItemTransform("Scene 1", 1, transform))

	_, err = db.CreateScenePreset(context.Background(), storage.ScenePreset{
		Name:      "Hide Webcam",
		SceneName: "Scene 1",
		Sources:   []storage.SourceState{{Name: "Webcam", Visible: false}},
	})
	require.NoError(t, err)

	_, _, err = server.handleApplyScenePreset(context.Background(), nil,
		ApplyPresetInput{PresetName: "Hide Webcam"})
	require.NoError(t, err)

	after, err := mock.GetSceneItemTransform("Scene 1", 1)
	require.NoError(t, err)
	assert.Equal(t, 777.0, after.PositionX, "applying a preset moved a source")
}
