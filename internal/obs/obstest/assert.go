package obstest

import (
	"reflect"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// derivedTransformFields are computed by OBS from the source's own size. They are
// read-only, so a round-trip cannot be expected to preserve them.
var derivedTransformFields = map[string]bool{
	"Width":        true,
	"Height":       true,
	"SourceWidth":  true,
	"SourceHeight": true,
}

// assertSettableFieldsEqual compares every settable field of two transforms by
// reflection rather than by an explicit list.
//
// That is the point: a field added to obs.SceneItemTransform is compared
// automatically, so this row keeps catching dropped fields without anyone
// remembering to extend it. Naming fields explicitly is how FB-54 stayed hidden --
// the tests only checked what someone had thought to check.
func assertSettableFieldsEqual(t *testing.T, want, got *obs.SceneItemTransform) {
	t.Helper()

	if got == nil {
		t.Fatal("transform is nil")
	}

	wv, gv := reflect.ValueOf(*want), reflect.ValueOf(*got)
	typ := wv.Type()

	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if derivedTransformFields[name] {
			continue
		}
		w, g := wv.Field(i).Interface(), gv.Field(i).Interface()
		if !reflect.DeepEqual(w, g) {
			t.Errorf("%s: set %v, read back %v", name, w, g)
		}
	}
}
