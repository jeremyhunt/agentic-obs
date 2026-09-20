package mcp

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/ironystock/agentic-obs/internal/obs"
)

// composedRoles is the list OBSClient is built from, repeated here so the test
// has something independent to compare against. Adding a role to OBSClient means
// adding it here too -- that edit is the point, because it is the moment someone
// has to decide which role a new capability belongs to.
var composedRoles = []reflect.Type{
	reflect.TypeOf((*obs.Connector)(nil)).Elem(),
	reflect.TypeOf((*obs.SceneReader)(nil)).Elem(),
	reflect.TypeOf((*obs.SceneWriter)(nil)).Elem(),
	reflect.TypeOf((*obs.SceneItemReader)(nil)).Elem(),
	reflect.TypeOf((*obs.SceneItemWriter)(nil)).Elem(),
	reflect.TypeOf((*obs.InputReader)(nil)).Elem(),
	reflect.TypeOf((*obs.InputConfigurer)(nil)).Elem(),
	reflect.TypeOf((*obs.AudioController)(nil)).Elem(),
	reflect.TypeOf((*obs.FilterManager)(nil)).Elem(),
	reflect.TypeOf((*obs.OutputController)(nil)).Elem(),
	reflect.TypeOf((*obs.StudioController)(nil)).Elem(),
	reflect.TypeOf((*obs.TransitionController)(nil)).Elem(),
	reflect.TypeOf((*obs.HotkeyTrigger)(nil)).Elem(),
	reflect.TypeOf((*obs.Screenshotter)(nil)).Elem(),
	reflect.TypeOf((*obs.StatusReader)(nil)).Elem(),
	reflect.TypeOf((*obs.CanvasReader)(nil)).Elem(),
	reflect.TypeOf((*obs.ScenePresetOperator)(nil)).Elem(),
	reflect.TypeOf((*obs.AdvancedSceneSwitcherController)(nil)).Elem(),
	reflect.TypeOf((*obs.EventSource)(nil)).Elem(),
}

// TestOBSClientIsExactlyTheUnionOfRoles is what makes "declare it on a role, not
// here" a rule rather than a comment.
//
// OBSClient was 76 hand-maintained method signatures. Nothing stopped a method
// being added to it and to nothing else, and nothing connected it to the client
// that had to implement it. Now a method reaches OBSClient only by belonging to
// a role, and this fails if anyone declares one inline.
func TestOBSClientIsExactlyTheUnionOfRoles(t *testing.T) {
	iface := reflect.TypeOf((*OBSClient)(nil)).Elem()

	onInterface := map[string]bool{}
	for i := 0; i < iface.NumMethod(); i++ {
		onInterface[iface.Method(i).Name] = true
	}

	inRoles := map[string]bool{}
	for _, role := range composedRoles {
		for i := 0; i < role.NumMethod(); i++ {
			inRoles[role.Method(i).Name] = true
		}
	}

	for _, name := range sortedKeys(onInterface) {
		if !inRoles[name] {
			t.Errorf("OBSClient declares %s inline; move it to a role in internal/obs/roles.go", name)
		}
	}
	for _, name := range sortedKeys(inRoles) {
		if !onInterface[name] {
			t.Errorf("%s is on a composed role but missing from OBSClient; the role is not embedded", name)
		}
	}

	// A count that only ever moves deliberately. If this is the only failure,
	// the surface grew or shrank and the rest of the suite agreed -- update it.
	if len(onInterface) != 82 {
		t.Errorf("OBSClient exposes %d methods, expected 82", len(onInterface))
	}
}

// TestEveryRoleIsSatisfiedByTheMock pins the other direction. obs.Client is
// checked at compile time in roles.go; the mock is what tests actually run
// against, and a mock that lags a role makes the role untestable.
func TestEveryRoleIsSatisfiedByTheMock(t *testing.T) {
	mockType := reflect.TypeOf(testutil.NewMockOBSClient())

	for _, role := range composedRoles {
		if !mockType.Implements(role) {
			t.Errorf("the test mock does not implement %s", role.Name())
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
