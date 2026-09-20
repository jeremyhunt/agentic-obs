package obs

import "testing"

// TestVideoSettingsFPS covers the one piece of arithmetic in the canvas report.
//
// OBS stores frame rate as a fraction, because 59.94 and 29.97 are 60000/1001
// and 30000/1001 and cannot be written any other way. Reporting the numerator
// alone -- an easy mistake, since it reads as 60 for the common cases -- would
// be silently wrong for exactly the NTSC rates people actually stream at.
//
// A zero denominator is not merely a division guard: obs-websocket omits zero
// fields (the response tags are all omitempty), so an unset value arrives as
// zero rather than absent. (FB-69)
func TestVideoSettingsFPS(t *testing.T) {
	tests := []struct {
		name        string
		numerator   float64
		denominator float64
		want        float64
	}{
		{"whole frame rate", 60, 1, 60},
		{"NTSC 59.94", 60000, 1001, 60000.0 / 1001.0},
		{"NTSC 29.97", 30000, 1001, 30000.0 / 1001.0},
		{"film", 24, 1, 24},
		{"zero denominator reports zero rather than dividing", 60, 0, 0},
		{"nothing reported at all", 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := VideoSettings{FPSNumerator: tc.numerator, FPSDenominator: tc.denominator}
			if got := v.FPS(); got != tc.want {
				t.Errorf("FPS() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestVideoSettingsIsDownscaled reports whether OBS is scaling the canvas on its
// way out, which is the thing a layout decision actually turns on: assets are
// authored against the base resolution, not the output one.
func TestVideoSettingsIsDownscaled(t *testing.T) {
	tests := []struct {
		name                     string
		baseW, baseH, outW, outH float64
		want                     bool
	}{
		{"same size", 1920, 1080, 1920, 1080, false},
		{"downscaled", 2560, 1440, 1920, 1080, true},
		{"upscaled counts as scaled", 1280, 720, 1920, 1080, true},
		{"unreported", 0, 0, 0, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := VideoSettings{
				BaseWidth: tc.baseW, BaseHeight: tc.baseH,
				OutputWidth: tc.outW, OutputHeight: tc.outH,
			}
			if got := v.IsScaled(); got != tc.want {
				t.Errorf("IsScaled() = %v, want %v", got, tc.want)
			}
		})
	}
}
