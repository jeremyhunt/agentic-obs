package obs

import "testing"

// TestAlignmentValuesMatchLibobs pins the numbers against their source.
//
// The values are from libobs/obs-defs.h, not from reading a transform and
// assuming. They are bit flags, so a wrong one is not a wrong answer but a
// different corner -- an item anchored bottom-right instead of top-left looks
// merely misplaced, and every test that compares a transform to itself still
// passes. (FB-74)
func TestAlignmentValuesMatchLibobs(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
		note string
	}{
		{"OBS_ALIGN_CENTER", AlignCenter, 0, "centre is the absence of edge flags, not a flag"},
		{"OBS_ALIGN_LEFT", AlignLeft, 1, "1 << 0"},
		{"OBS_ALIGN_RIGHT", AlignRight, 2, "1 << 1"},
		{"OBS_ALIGN_TOP", AlignTop, 4, "1 << 2"},
		{"OBS_ALIGN_BOTTOM", AlignBottom, 8, "1 << 3"},
	}

	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d (%s)", c.name, c.got, c.want, c.note)
		}
	}

	// The value libobs gives a new scene item, and the one a live OBS reports
	// for one. It was a bare 5 in the codebase before these constants existed.
	if AlignTopLeft != 5 {
		t.Errorf("AlignTopLeft = %d, want 5; a fresh scene item reports 5", AlignTopLeft)
	}
}

// TestAlignmentFlagsAreDistinctBits: they are OR-combined, so any two sharing a
// bit would make one silently imply the other.
func TestAlignmentFlagsAreDistinctBits(t *testing.T) {
	flags := map[string]int{
		"left": AlignLeft, "right": AlignRight, "top": AlignTop, "bottom": AlignBottom,
	}
	for aName, a := range flags {
		for bName, b := range flags {
			if aName >= bName {
				continue
			}
			if a&b != 0 {
				t.Errorf("%s (%d) and %s (%d) share a bit", aName, a, bName, b)
			}
		}
	}
}

// TestBoundsTypeValidation covers the set OBS accepts. The names come from
// obs_transform_info in obs-studio's scene reference.
func TestBoundsTypeValidation(t *testing.T) {
	for _, valid := range BoundsTypes {
		if !IsValidBoundsType(valid) {
			t.Errorf("%s is a real bounds type but was rejected", valid)
		}
	}

	for _, invalid := range []string{"", "OBS_BOUNDS_FIT", "scale_inner", "OBS_BOUNDS_COVER"} {
		if IsValidBoundsType(invalid) {
			t.Errorf("%q is not a bounds type OBS defines", invalid)
		}
	}

	// The two most likely to be confused, kept apart deliberately: inner fits
	// the whole source inside the box, outer covers the box and overflows.
	if BoundsScaleInner == BoundsScaleOuter {
		t.Error("inner and outer must be different bounds types")
	}
}

// TestBoundsTypeNoneMatchesTheNormaliser: FB-64 introduced BoundsTypeNone
// separately, before these constants existed. Two spellings of the same string
// is how they drift apart.
func TestBoundsTypeNoneMatchesTheNormaliser(t *testing.T) {
	if BoundsTypeNone != BoundsNone {
		t.Errorf("BoundsTypeNone (%q) and BoundsNone (%q) must be the same value", BoundsTypeNone, BoundsNone)
	}
}
