package obs

import "testing"

// TestNormaliseBoundsOnlyTouchesUnusedBounds pins the narrow scope of the fix.
// Normalisation exists so that a transform OBS itself reported can be written
// back; it must not quietly reshape a bounding box a caller actually asked for.
func TestNormaliseBoundsOnlyTouchesUnusedBounds(t *testing.T) {
	tests := []struct {
		name         string
		inType       string
		inW, inH     float64
		wantType     string
		wantW, wantH float64
	}{
		{
			name:   "unset type becomes NONE and dimensions are made writable",
			inType: "", inW: 0, inH: 0,
			wantType: BoundsTypeNone, wantW: 1, wantH: 1,
		},
		{
			name:   "what OBS reports for an item with no bounding box",
			inType: BoundsTypeNone, inW: 0, inH: 0,
			wantType: BoundsTypeNone, wantW: 1, wantH: 1,
		},
		{
			name:   "NONE with real dimensions is left alone",
			inType: BoundsTypeNone, inW: 800, inH: 600,
			wantType: BoundsTypeNone, wantW: 800, wantH: 600,
		},
		{
			name:   "a real bounds mode is never rewritten, even at zero",
			inType: "OBS_BOUNDS_SCALE_INNER", inW: 0, inH: 0,
			wantType: "OBS_BOUNDS_SCALE_INNER", wantW: 0, wantH: 0,
		},
		{
			name:   "a real bounds mode with real dimensions is untouched",
			inType: "OBS_BOUNDS_MAX_ONLY", inW: 1920, inH: 1080,
			wantType: "OBS_BOUNDS_MAX_ONLY", wantW: 1920, wantH: 1080,
		},
		{
			name:   "a fractional dimension under NONE is still raised to the minimum",
			inType: BoundsTypeNone, inW: 0.5, inH: 0.5,
			wantType: BoundsTypeNone, wantW: 1, wantH: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotW, gotH := NormaliseBounds(tc.inType, tc.inW, tc.inH)
			if gotType != tc.wantType || gotW != tc.wantW || gotH != tc.wantH {
				t.Errorf("got (%q, %v, %v), want (%q, %v, %v)",
					gotType, gotW, gotH, tc.wantType, tc.wantW, tc.wantH)
			}
		})
	}
}
