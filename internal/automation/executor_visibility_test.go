package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetVisibilityIsNotAToggle covers the FB-55 defect.
//
// setVisibility used to delegate to toggleVisibility, under a comment asserting
// that obs-websocket had no setter. It does -- internal/obs was already calling
// SetSceneItemEnabled privately inside ApplyScenePreset; the method simply was
// not on the client interface.
//
// The consequence was not subtle: a rule asking for visible=true flipped the item
// instead. Firing it twice hid the thing it was meant to show, and any invariant
// built on the action ("keep exactly one camera visible") would oscillate by
// construction, because every correction inverted the state it had just read.
func TestSetVisibilityIsNotAToggle(t *testing.T) {
	t.Run("requests the state it was given, every time", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		params := map[string]interface{}{
			"scene_name": "Scene 1",
			"source_id":  1,
			"visible":    true,
		}

		require.NoError(t, executor.setVisibility(params))
		require.NoError(t, executor.setVisibility(params))

		// Two identical requests must produce two identical writes. A toggle would
		// have produced one "on" and one "off".
		assert.Equal(t,
			[]string{"set_visibility:Scene 1:1:true", "set_visibility:Scene 1:1:true"},
			mock.GetActions(),
			"set_visibility must be idempotent; a toggle here inverts the state on the second call")
	})

	t.Run("honours visible=false", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		require.NoError(t, executor.setVisibility(map[string]interface{}{
			"scene_name": "Scene 1",
			"source_id":  2,
			"visible":    false,
		}))

		assert.Equal(t, []string{"set_visibility:Scene 1:2:false"}, mock.GetActions())
	})

	t.Run("never routes through the toggle path", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		require.NoError(t, executor.setVisibility(map[string]interface{}{
			"scene_name": "Scene 1",
			"source_id":  1,
			"visible":    true,
		}))

		assert.NotContains(t, mock.GetActions(), "toggle_visibility",
			"set_visibility must not delegate to ToggleSourceVisibility")
	})

	t.Run("rejects a missing visible parameter", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		err := executor.setVisibility(map[string]interface{}{
			"scene_name": "Scene 1",
			"source_id":  1,
		})

		require.Error(t, err, "without 'visible' there is no state to set; silently toggling is what this fixes")
		assert.Contains(t, err.Error(), "visible")
		assert.Empty(t, mock.GetActions(), "a rejected action must not touch OBS")
	})

	t.Run("toggle_visibility still toggles", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		require.NoError(t, executor.toggleVisibility(map[string]interface{}{
			"scene_name": "Scene 1",
			"source_id":  1,
		}))

		assert.Equal(t, []string{"toggle_visibility"}, mock.GetActions())
	})
}
