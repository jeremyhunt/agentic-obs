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
//
// tool names the caller for the refusal below. It is a parameter rather than a
// literal because the guard belongs here, once, for the same reason the policy
// does: five copies is five chances to forget one.
func (s *Server) createTypedSource(tool, sceneName, sourceName, kind string, settings map[string]interface{}, ifExists string) (string, int, error) {
	// Before if_exists is even validated, because every branch below reaches
	// the Lua bridge's transport and the dangerous one does not look dangerous.
	// if_exists=update ends in s.ensureInput, the unexported worker, which
	// carries no guard of its own -- only handleEnsureInput does. And the kind
	// check above it does not stop the caller: the inbox really is a
	// color_source_v3, which is what the bridge's Lua creates, so
	// create_color_source matches it exactly and falls straight through to the
	// update path. With the bridge absent, the create branch plants a decoy
	// that collides with the real transport the day it is installed. See
	// bridge_reserved.go.
	if isBridgeTransport(sourceName) {
		return "", 0, errBridgeTransportWrite(tool, sourceName)
	}

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
