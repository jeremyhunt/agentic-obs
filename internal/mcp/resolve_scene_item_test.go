package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scene-item tools address an item by numeric id, which an agent has to look up
// first. Accepting the source name instead removes a round trip and a class of
// mistake -- but a source can be placed in one scene more than once, so the name
// is not a key. (FB-70)

func TestResolveSceneItemID(t *testing.T) {
	t.Run("an explicit id is used as given", func(t *testing.T) {
		server, _ := testServer(t)

		// No lookup, no ambiguity, and no dependence on the source still having
		// the name it had when the caller read it.
		got, err := server.resolveSceneItemID("Scene 1", 2, "")
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("an explicit id wins over a name", func(t *testing.T) {
		server, _ := testServer(t)

		// Supplying both is a caller mistake, but the id is the more specific
		// answer and silently preferring it is better than failing.
		got, err := server.resolveSceneItemID("Scene 1", 2, "Webcam")
		require.NoError(t, err)
		assert.Equal(t, 2, got, "the id addresses one item; the name might not")
	})

	t.Run("a unique source name resolves to its id", func(t *testing.T) {
		server, _ := testServer(t)

		got, err := server.resolveSceneItemID("Scene 1", 0, "Text")
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("a name placed twice in the scene is refused", func(t *testing.T) {
		server, mock := testServer(t)

		// One input, two placements -- OVERLAY_NowPlaying lives in two scenes in
		// this workspace, and duplicating within one scene is equally legal.
		// Picking the first match would silently move whichever OBS happened to
		// list first, so the tool has to say it cannot tell them apart.
		_, err := mock.DuplicateSceneItem("Scene 1", 2, "Scene 1")
		require.NoError(t, err)

		_, err = server.resolveSceneItemID("Scene 1", 0, "Text")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scene_item_id",
			"the error must say how to disambiguate, not just that it failed")
	})

	t.Run("the ambiguity error names the candidate ids", func(t *testing.T) {
		server, mock := testServer(t)

		dupID, err := mock.DuplicateSceneItem("Scene 1", 2, "Scene 1")
		require.NoError(t, err)

		_, err = server.resolveSceneItemID("Scene 1", 0, "Text")
		require.Error(t, err)

		// Without the ids the caller has to go and list the scene anyway, which
		// is the round trip this was meant to save.
		assert.Contains(t, err.Error(), "2")
		assert.Contains(t, err.Error(), itoa(dupID))
	})

	t.Run("a name not in the scene is refused", func(t *testing.T) {
		server, _ := testServer(t)

		_, err := server.resolveSceneItemID("Scene 1", 0, "NotThere")
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "not found")
	})

	t.Run("neither an id nor a name is refused", func(t *testing.T) {
		server, _ := testServer(t)

		_, err := server.resolveSceneItemID("Scene 1", 0, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source_name")
	})

	t.Run("an unreadable scene is reported rather than guessed at", func(t *testing.T) {
		server, _ := testServer(t)

		_, err := server.resolveSceneItemID("NoSuchScene", 0, "Webcam")
		require.Error(t, err)
	})
}

// TestSceneItemToolsAcceptASourceName drives the resolution through a real
// handler, so the wiring is covered and not just the helper.
func TestSceneItemToolsAcceptASourceName(t *testing.T) {
	t.Run("set_source_transform addresses an item by name", func(t *testing.T) {
		server, mock := testServer(t)

		x := 640.0
		_, _, err := server.handleSetSourceTransform(nil, nil, SetSourceTransformInput{
			SceneName:  "Scene 1",
			SourceName: "Text",
			X:          &x,
		})
		require.NoError(t, err)

		// Item 2 is "Text" in the fixture.
		tr, err := mock.GetSceneItemTransform("Scene 1", 2)
		require.NoError(t, err)
		assert.Equal(t, 640.0, tr.PositionX)
	})

	t.Run("an unresolvable name fails the call", func(t *testing.T) {
		server, _ := testServer(t)

		x := 640.0
		_, _, err := server.handleSetSourceTransform(nil, nil, SetSourceTransformInput{
			SceneName:  "Scene 1",
			SourceName: "NotThere",
			X:          &x,
		})
		assert.Error(t, err)
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
