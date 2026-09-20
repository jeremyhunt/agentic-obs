package obs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// allEventTypes is every EventType this package defines. It is hand-listed
// because Go cannot enumerate constants, and that is useful here: adding an
// event kind forces a decision about whether it changes a published resource,
// rather than letting the answer default to "no" unnoticed.
var allEventTypes = []EventType{
	EventTypeSceneCreated,
	EventTypeSceneRemoved,
	EventTypeSceneChanged,
	EventTypeRecordingStarted,
	EventTypeRecordingStopped,
	EventTypeRecordingPaused,
	EventTypeRecordingResumed,
	EventTypeRecordingFileChanged,
	EventTypeStreamingStarted,
	EventTypeStreamingStopped,
	EventTypeVirtualCamStarted,
	EventTypeVirtualCamStopped,
	EventTypeReplayBufferSaved,
	EventTypeInputMuteChanged,
	EventTypeSourceVisibilityChanged,
	EventTypeTransitionStarted,
	EventTypeStudioModeChanged,
	EventTypePreviewSceneChanged,
}

// TestAllEventTypesIsComplete makes the hand-list above trustworthy.
//
// Without it the list is a claim, not a guard: adding an event kind and
// forgetting to list it makes the rule test pass while saying nothing about the
// new kind. That is precisely how the tool-count constants drifted into four
// different values, so the same answer applies -- read the source and compare.
func TestAllEventTypesIsComplete(t *testing.T) {
	declared, err := declaredEventTypes("events.go")
	if err != nil {
		t.Fatalf("reading event type declarations: %v", err)
	}

	listed := map[EventType]bool{}
	for _, et := range allEventTypes {
		listed[et] = true
	}

	for _, name := range declared {
		if !listed[name] {
			t.Errorf("%s is declared in events.go but missing from allEventTypes; "+
				"decide whether it changes a published resource and add it", name)
		}
	}
	if len(declared) != len(allEventTypes) {
		t.Errorf("events.go declares %d event types, allEventTypes has %d",
			len(declared), len(allEventTypes))
	}
}

// declaredEventTypes parses the source for `EventTypeX EventType = "..."`
// constants, so the test reads what exists rather than what someone remembered.
func declaredEventTypes(path string) ([]EventType, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	var found []EventType
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "EventType" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				found = append(found, EventType(strings.Trim(lit.Value, `"`)))
			}
		}
	}
	return found, nil
}

// TestOnlyResourceAffectingEventsTriggerUpdates states the rule exactly, in both
// directions: an event triggers a resources/updated notification if and only if
// it changes what obs://scene/{name} serialises.
//
// The "only if" half is the one worth guarding. A notification for a resource
// whose content has not changed is not harmless -- the client re-reads, finds
// the same bytes, and learns to trust the signal less.
//
// Filter events are the live example. FB-62 subscribed to them, and the plan
// called for mapping them here, but obs://scene/{name} publishes id, name, type,
// enabled, visible, locked and the transform -- no filter state at all. So a
// filter change cannot alter that resource, and notifying on it would be noise.
// Mapping them becomes correct only if the resource is enriched to carry
// filters, which the plan's own review rejected on round-trip cost. (FB-63)
func TestOnlyResourceAffectingEventsTriggerUpdates(t *testing.T) {
	triggers := map[EventType]bool{
		// Switching scenes changes which scene is current, published as isActive.
		EventTypeSceneChanged: true,
		// Hiding or showing an item changes that item's "visible" and "enabled"
		// in the scene's own representation.
		EventTypeSourceVisibilityChanged: true,
		// Moving the preview changes isPreview on two scenes. This one only
		// qualifies because the field was added alongside it (FB-65) -- the event
		// and the field are the same change, and shipping either alone would be
		// wrong: the field without the event goes stale, the event without the
		// field is a notification about nothing.
		EventTypePreviewSceneChanged: true,
	}

	for _, et := range allEventTypes {
		want := triggers[et]
		if got := ShouldTriggerResourceUpdated(et); got != want {
			if want {
				t.Errorf("%s should trigger a resource update; it changes obs://scene/{name}", et)
			} else {
				t.Errorf("%s triggers a resource update but does not change obs://scene/{name}; "+
					"notifying on it teaches clients the signal is unreliable", et)
			}
		}
	}
}

// TestUnknownEventTypesDoNotTriggerUpdates covers the default branch. Events
// arrive from a subscription mask deliberately wider than the set translated
// here, so an unrecognised type is routine rather than exceptional.
func TestUnknownEventTypesDoNotTriggerUpdates(t *testing.T) {
	for _, et := range []EventType{"", "source_filter_enabled", "vendor_event", "not_a_real_event"} {
		if ShouldTriggerResourceUpdated(et) {
			t.Errorf("unrecognised event %q triggered a resource update", et)
		}
	}
}
