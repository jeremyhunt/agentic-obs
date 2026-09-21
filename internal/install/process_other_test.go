//go:build !windows

package install

import "testing"

var unixOBSNames = map[string]bool{"obs64.exe": true, "obs.exe": true, "obs": true}

func TestMatchesOBSCases(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		// macOS realistic cases -- the exact bug this was written for. The
		// old code did an exact, case-sensitive match against the raw line,
		// so none of these ever matched.
		{"macOS bare capitalized name", "OBS\n", true},
		{"macOS full bundle path", "/Applications/OBS.app/Contents/MacOS/OBS\n", true},
		{"macOS bundle path, lowercase leaf", "/Applications/OBS.app/Contents/MacOS/obs\n", true},
		// Linux's actual binary name is normally already lowercase.
		{"Linux lowercase name", "obs\n", true},
		{"OBS among many other processes", "bash\nFinder\nDropbox\nOBS\nfinder\nSpotlight\n", true},
		// The regression this file guards against: this tool's own process
		// name, and a plausible neighbour, both contain "obs" as a
		// substring and must NOT match under exact comparison.
		{"this tool's own process name", "agentic-obs\n", false},
		{"this tool's own process, full path", "/usr/local/bin/agentic-obs\n", false},
		{"a note-taking app with obs as a substring", "Obsidian\n", false},
		{"agentic-obs alongside the real OBS", "bash\nagentic-obs\nOBS\n", true},
		// Must not match when OBS genuinely isn't running.
		{"no OBS running", "bash\nFinder\nDropbox\nfinder\nSpotlight\n", false},
		{"empty output", "", false},
		{"only blank lines", "\n\n\n", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchesOBS([]byte(c.output), unixOBSNames)
			if got != c.want {
				t.Errorf("matchesOBS(%q) = %v, want %v", c.output, got, c.want)
			}
		})
	}
}
