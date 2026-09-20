package mcp

import (
	"fmt"
	"strings"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// resolveSceneItemID turns whichever of scene_item_id or source_name the caller
// supplied into a numeric id.
//
// Addressing by name saves an agent a lookup, and saves it from transcribing an
// id it read a moment ago. But a name is not a key: OBS lets one input be placed
// in a scene more than once, so a name can address two items that are the same
// source and different placements -- different positions, different visibility.
// Picking the first match would silently move whichever OBS happened to list
// first, which is the sort of failure nobody notices until a layout is wrong on
// stream. When the name is ambiguous this refuses, and says which ids to choose
// between. (FB-70)
func (s *Server) resolveSceneItemID(sceneName string, sceneItemID int, sourceName string) (int, error) {
	// An id is the more specific answer, so it wins even when both are given.
	if sceneItemID > 0 {
		return sceneItemID, nil
	}

	if sourceName == "" {
		return 0, fmt.Errorf("supply either scene_item_id or source_name to identify the item in scene '%s'", sceneName)
	}

	scene, err := s.obsClient.GetSceneByName(sceneName)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve source '%s': %w", sourceName, err)
	}

	var matches []obs.SceneSource
	for _, src := range scene.Sources {
		if src.Name == sourceName {
			matches = append(matches, src)
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("source '%s' not found in scene '%s'; it has %s",
			sourceName, sceneName, describeSceneContents(scene))
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, fmt.Sprintf("%d", m.ID))
		}
		return 0, fmt.Errorf(
			"source '%s' is placed %d times in scene '%s' (scene_item_id %s); "+
				"pass scene_item_id to say which placement you mean",
			sourceName, len(matches), sceneName, strings.Join(ids, ", "))
	}
}

// describeSceneContents lists what a scene does hold, so a caller that got the
// name slightly wrong can see the right one rather than guess again.
func describeSceneContents(scene *obs.Scene) string {
	if len(scene.Sources) == 0 {
		return "no sources"
	}

	names := make([]string, 0, len(scene.Sources))
	for _, src := range scene.Sources {
		names = append(names, fmt.Sprintf("'%s' (scene_item_id %d)", src.Name, src.ID))
	}
	return strings.Join(names, ", ")
}
