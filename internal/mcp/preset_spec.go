package mcp

import (
	"github.com/ironystock/agentic-obs/internal/scenespec"
	"github.com/ironystock/agentic-obs/internal/storage"
)

// presetToSpec turns a saved preset into the scene spec it always was.
//
// A preset is a list of which sources are visible and nothing else, which is a
// spec restricted to one aspect. Expressing it that way means presets and specs
// share one reconciler: the preset gets a dry run and a per-op report for free,
// and there is no second piece of code deciding what "apply" means.
//
// The occurrence index is the part that is not cosmetic. A source can be placed
// twice in one scene -- jurmiey_avatar is, in Game -- so a preset of that scene
// carries two entries under one name. Addressing them by name alone collapses
// both onto whichever placement the lookup found last, leaving the other at
// whatever state it happened to have.
//
// No Sources list: an apply restricted to enabled never creates or configures a
// source, so there is nothing for one to say.
func presetToSpec(sceneName string, sources []storage.SourceState) *scenespec.Spec {
	seen := map[string]int{}
	items := make([]scenespec.ItemSpec, 0, len(sources))

	for _, src := range sources {
		items = append(items, scenespec.ItemSpec{
			Source:     src.Name,
			Occurrence: seen[src.Name],
			Enabled:    src.Visible,
		})
		seen[src.Name]++
	}

	return &scenespec.Spec{
		Version: scenespec.SpecVersion,
		Scene:   sceneName,
		Items:   items,
	}
}
