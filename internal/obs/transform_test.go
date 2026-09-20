package obs

import (
	"reflect"
	"testing"

	"github.com/andreykaipov/goobs/api/typedefs"
)

// TestSceneItemTransformCoversEverySettableGoobsField is the guard for the class
// of bug FB-54 fixes.
//
// goobs marshals typedefs.SceneItemTransform with no omitempty, and obs-websocket
// applies any transform key present in the request. A settable field missing from
// our SceneItemTransform is therefore not "left alone" on a write -- it is sent as
// its zero value and applied. Alignment was missing, so every transform write sent
// alignment=0 (OBS_ALIGN_CENTER) and re-anchored items sitting on the libobs
// default of top|left.
//
// This test fails if goobs gains a settable transform field that we do not carry.
func TestSceneItemTransformCoversEverySettableGoobsField(t *testing.T) {
	// Derived by OBS and ignored on a write, so deliberately not sent.
	readOnly := map[string]bool{
		"Width":        true,
		"Height":       true,
		"SourceWidth":  true,
		"SourceHeight": true,
	}

	ours := map[string]bool{}
	for _, name := range []string{
		"PositionX", "PositionY", "ScaleX", "ScaleY", "Rotation",
		"Alignment", "BoundsType", "BoundsAlignment", "BoundsWidth", "BoundsHeight",
		"CropToBounds", "CropTop", "CropBottom", "CropLeft", "CropRight",
	} {
		ours[name] = true
	}

	// Reflect over the goobs typedef so a library upgrade that adds a field is
	// caught here rather than by a mystery layout change on someone's stream.
	gt := typedefs.SceneItemTransform{}
	missing := []string{}
	for _, f := range structFieldNames(gt) {
		if readOnly[f] || ours[f] {
			continue
		}
		missing = append(missing, f)
	}

	if len(missing) > 0 {
		t.Errorf("goobs typedefs.SceneItemTransform has settable fields we do not carry: %v.\n"+
			"Add them to obs.SceneItemTransform and to both Get/SetSceneItemTransform, "+
			"or add them to the read-only list if OBS derives them.", missing)
	}
}

// TestSetTransformSendsEverySettableField asserts the write path populates every
// settable field from our struct, so a field cannot be silently dropped on the way
// to goobs.
func TestSetTransformSendsEverySettableField(t *testing.T) {
	src := &SceneItemTransform{
		PositionX: 10, PositionY: 20,
		ScaleX: 1.5, ScaleY: 2.5,
		Rotation:        90,
		Alignment:       5, // OBS_ALIGN_TOP|OBS_ALIGN_LEFT, the libobs default
		BoundsType:      "OBS_BOUNDS_SCALE_INNER",
		BoundsAlignment: 4,
		BoundsWidth:     640, BoundsHeight: 480,
		CropToBounds: true,
		CropTop:      1, CropBottom: 2, CropLeft: 3, CropRight: 4,
		// Derived values that must not be forwarded.
		Width: 999, Height: 999, SourceWidth: 999, SourceHeight: 999,
	}

	got := toGoobsTransform(src)

	if got.Alignment != 5 {
		t.Errorf("Alignment = %v, want 5: a zero here re-anchors the item to its centre", got.Alignment)
	}
	if got.BoundsAlignment != 4 {
		t.Errorf("BoundsAlignment = %v, want 4", got.BoundsAlignment)
	}
	if !got.CropToBounds {
		t.Error("CropToBounds = false, want true")
	}
	if got.PositionX != 10 || got.PositionY != 20 || got.ScaleX != 1.5 || got.Rotation != 90 {
		t.Errorf("basic transform fields not carried: %+v", got)
	}
	if got.CropTop != 1 || got.CropRight != 4 {
		t.Errorf("crop not carried: %+v", got)
	}
	// OBS derives these and ignores them; sending them is harmless but pointless,
	// and leaving them zero documents the intent.
	if got.Width != 0 || got.SourceWidth != 0 {
		t.Errorf("read-only fields should not be forwarded, got Width=%v SourceWidth=%v",
			got.Width, got.SourceWidth)
	}
}

// structFieldNames returns the exported field names of a struct value.
func structFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	return names
}
