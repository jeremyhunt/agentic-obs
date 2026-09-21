package mcp

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/scenespec"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// EnsureInputInput is the input for create-or-update on any input kind.
type EnsureInputInput struct {
	SceneName  string                 `json:"scene_name" jsonschema:"Scene the input should appear in"`
	SourceName string                 `json:"source_name" jsonschema:"Name of the input"`
	InputKind  string                 `json:"input_kind" jsonschema:"Input kind, e.g. browser_source (see list_input_kinds)"`
	Settings   map[string]interface{} `json:"settings,omitempty" jsonschema:"Settings to apply"`
	// Overlay merges into existing settings when true or absent, and replaces
	// them when false. Same meaning as set_source_settings.
	Overlay *bool `json:"overlay,omitempty" jsonschema:"Merge settings (default) or replace them"`

	// PreserveURLParams names query parameters of the existing URL that belong
	// to another writer and must survive this call.
	PreserveURLParams []string `json:"preserve_url_params,omitempty" jsonschema:"Query parameters of the existing url to carry over, for a URL with another writer"`
}

// handleEnsureInput makes an input exist, in a scene, configured as asked.
//
// The five create_* tools are create-only and fail on a second run, so every
// setup script in this workspace hand-rolls this: look it up, create it if
// missing, update it if not. Doing it here removes that from each of them and
// makes re-running a setup safe.
//
// It reports which of four things happened, because they are not
// interchangeable to a caller deciding whether anything needs doing:
//
//	created   the input did not exist
//	placed    the input existed elsewhere and was added to this scene
//	updated   the input existed here and its settings changed
//	unchanged nothing needed doing
//
// "unchanged" is the one worth getting right. Reporting "updated" for a write
// that changed nothing would make a re-run look like a change every time, which
// is exactly what makes a status hard to trust. (FB-71)
func (s *Server) handleEnsureInput(ctx context.Context, request *mcpsdk.CallToolRequest, input EnsureInputInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()
	log.Printf("Ensuring input '%s' (%s) in scene '%s'", input.SourceName, input.InputKind, input.SceneName)

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("ensure_input", "Ensure input", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	existing, err := s.findInput(input.SourceName)
	if err != nil {
		return fail(err)
	}

	// A name already in use by a different kind is not something to reinterpret:
	// the caller believes it has one thing and has another, and only it can say
	// what should happen.
	if existing != nil && existing.InputKind != input.InputKind {
		return fail(fmt.Errorf(
			"input '%s' already exists as kind '%s', not '%s'; "+
				"rename one of them, or remove the existing input first",
			input.SourceName, existing.InputKind, input.InputKind))
	}

	action, sceneItemID, err := s.ensureInput(input, existing != nil)
	if err != nil {
		return fail(err)
	}

	result := map[string]interface{}{
		"scene_name":    input.SceneName,
		"source_name":   input.SourceName,
		"input_kind":    input.InputKind,
		"action":        action,
		"scene_item_id": sceneItemID,
		"message":       fmt.Sprintf("Input '%s' %s in scene '%s'", input.SourceName, action, input.SceneName),
	}
	s.recordAction("ensure_input", "Ensure input", input, result, true, time.Since(start))
	return nil, result, nil
}

// ensureInput does the work, returning what it did and the placement's id.
func (s *Server) ensureInput(input EnsureInputInput, exists bool) (string, int, error) {
	if !exists {
		id, err := s.obsClient.CreateInput(input.SceneName, input.SourceName, input.InputKind, input.Settings)
		if err != nil {
			return "", 0, fmt.Errorf("failed to create input '%s': %w", input.SourceName, err)
		}
		return "created", id, nil
	}

	// The input exists. Is it in this scene?
	sceneItemID, placed, err := s.findPlacement(input.SceneName, input.SourceName)
	if err != nil {
		return "", 0, err
	}

	if !placed {
		// Placing shares the existing input rather than copying it, which is the
		// point: one overlay configured once, shown in several scenes.
		id, err := s.obsClient.CreateSceneItem(input.SceneName, input.SourceName, true)
		if err != nil {
			return "", 0, fmt.Errorf("failed to place input '%s' in scene '%s': %w",
				input.SourceName, input.SceneName, err)
		}
		if len(input.Settings) > 0 {
			// The input exists elsewhere, so it has a live URL to carry
			// parameters from -- but nothing has read it on this path, so a
			// caller that asked for preservation pays for one read here.
			if len(input.PreserveURLParams) > 0 {
				current, err := s.obsClient.GetSourceSettings(input.SourceName)
				if err != nil {
					return "", 0, fmt.Errorf("failed to read settings for '%s': %w", input.SourceName, err)
				}
				input.Settings = scenespec.PreserveURLParams(input.Settings, current, input.PreserveURLParams)
			}
			if err := s.applySettings(input); err != nil {
				return "", 0, err
			}
		}
		return "placed", id, nil
	}

	if len(input.Settings) == 0 {
		return "unchanged", sceneItemID, nil
	}

	current, err := s.obsClient.GetSourceSettings(input.SourceName)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read settings for '%s': %w", input.SourceName, err)
	}
	// Parameters another writer owns are carried onto what is about to be
	// written, before the comparison rather than after it. A URL that differs
	// only by one of them is not a change, and reporting "updated" for it would
	// make every re-run of a setup script look like a write.
	//
	// The helper lives in internal/scenespec because a spec needs the same rule
	// for the same reason: it writes settings with overlay=false, which is right
	// for every key it owns and is exactly what drops a key it does not.
	input.Settings = scenespec.PreserveURLParams(input.Settings, current, input.PreserveURLParams)

	if settingsAlreadyApplied(current, input.Settings) {
		return "unchanged", sceneItemID, nil
	}

	if err := s.applySettings(input); err != nil {
		return "", 0, err
	}
	return "updated", sceneItemID, nil
}

func (s *Server) applySettings(input EnsureInputInput) error {
	overlay := true
	if input.Overlay != nil {
		overlay = *input.Overlay
	}
	if err := s.obsClient.SetSourceSettings(input.SourceName, input.Settings, overlay); err != nil {
		return fmt.Errorf("failed to apply settings to '%s': %w", input.SourceName, err)
	}
	return nil
}

// findInput returns the input with this name, or nil.
func (s *Server) findInput(sourceName string) (*obs.InputSummary, error) {
	inputs, err := s.obsClient.ListSources()
	if err != nil {
		return nil, fmt.Errorf("failed to list inputs: %w", err)
	}
	for _, in := range inputs {
		if in != nil && in.InputName == sourceName {
			return &obs.InputSummary{InputName: in.InputName, InputKind: in.InputKind}, nil
		}
	}
	return nil, nil
}

// findPlacement reports whether the source is already in the scene, and under
// which scene item id.
func (s *Server) findPlacement(sceneName, sourceName string) (int, bool, error) {
	scene, err := s.obsClient.GetSceneByName(sceneName)
	if err != nil {
		return 0, false, fmt.Errorf("failed to read scene '%s': %w", sceneName, err)
	}
	for _, src := range scene.Sources {
		if src.Name == sourceName {
			return src.ID, true, nil
		}
	}
	return 0, false, nil
}

// settingsAlreadyApplied reports whether every requested setting already holds
// the requested value.
//
// Only the keys the caller asked about are compared. The live settings object
// carries plenty the caller said nothing about, and treating those as
// differences would make every call report a change.
func settingsAlreadyApplied(current, wanted map[string]interface{}) bool {
	for k, want := range wanted {
		got, ok := current[k]
		if !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}
