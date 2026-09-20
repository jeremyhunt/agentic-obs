package mcp

import "fmt"

// if_exists policies for the typed create_* tools.
const (
	ifExistsError  = "error"  // refuse; the default
	ifExistsUpdate = "update" // apply the settings to what is there
	ifExistsSkip   = "skip"   // leave it alone and say so
)

// createTypedSource creates a source, or applies the caller's if_exists policy
// when the name is already taken.
//
// The five typed creators each build their own settings map and then do the same
// thing with it. Keeping the policy here means it cannot be applied to some and
// forgotten on others -- the failure mode that made tool counts drift into four
// different values.
//
// The default is error, which is what these tools have always done. The plan
// called for update, but that was written before ensure_input existed; with both
// in place, create meaning create and ensure meaning ensure is the clearer pair,
// and silently turning a create into a write is the more expensive default to
// get wrong. (FB-72)
func (s *Server) createTypedSource(sceneName, sourceName, kind string, settings map[string]interface{}, ifExists string) (string, int, error) {
	switch ifExists {
	case "", ifExistsError, ifExistsUpdate, ifExistsSkip:
	default:
		return "", 0, fmt.Errorf("unknown if_exists value %q; use %q, %q or %q",
			ifExists, ifExistsError, ifExistsUpdate, ifExistsSkip)
	}

	existing, err := s.findInput(sourceName)
	if err != nil {
		return "", 0, err
	}

	if existing == nil {
		id, err := s.obsClient.CreateInput(sceneName, sourceName, kind, settings)
		if err != nil {
			return "", 0, fmt.Errorf("failed to create source '%s': %w", sourceName, err)
		}
		return "created", id, nil
	}

	if existing.InputKind != kind {
		return "", 0, fmt.Errorf(
			"source '%s' already exists as kind '%s', not '%s'; "+
				"rename one of them, or remove the existing source first",
			sourceName, existing.InputKind, kind)
	}

	switch ifExists {
	case "", ifExistsError:
		return "", 0, fmt.Errorf(
			"source '%s' already exists in OBS; pass if_exists=%q to apply these settings to it, "+
				"or if_exists=%q to leave it as it is",
			sourceName, ifExistsUpdate, ifExistsSkip)

	case ifExistsSkip:
		// Report the placement if it has one here, so a caller that skipped can
		// still position or inspect it without another lookup.
		id, _, err := s.findPlacement(sceneName, sourceName)
		if err != nil {
			return "", 0, err
		}
		return "skipped", id, nil

	default: // update
		// Identical to ensure_input from here, and deliberately the same code:
		// two implementations of create-or-update would be two chances to get
		// "unchanged" wrong.
		return s.ensureInput(EnsureInputInput{
			SceneName:  sceneName,
			SourceName: sourceName,
			InputKind:  kind,
			Settings:   settings,
		}, true)
	}
}
