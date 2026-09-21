//go:build !windows

package install

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// obsIsRunningFallback asks the OS for its process list via ps.
//
// macOS has no /proc, so on that platform this is not a fallback for an edge
// case -- it is the only detection OBSIsRunning has.
func obsIsRunningFallback(names map[string]bool) bool {
	out, err := exec.Command("ps", "-A", "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return matchesOBS(out, names)
}

// matchesOBS compares each process's base name, lowercased, exactly against
// names.
//
// ps -o comm= can report either a bare name ("OBS") or a full bundle path
// (/Applications/OBS.app/Contents/MacOS/OBS) depending on how the process
// was launched, and macOS commonly capitalizes it, so a case-sensitive exact
// match against the raw line silently never fired there -- the guard passed
// while OBS was genuinely running. filepath.Base plus lowercasing fixes
// that. It must stay an exact comparison, not a substring one: this
// process's own name, "agentic-obs", contains "obs" as a substring (it ends
// in it), and so does a plausible neighbour like "Obsidian" -- a substring
// check would make the guard fire on every invocation of this tool itself,
// or on any machine that happens to run Obsidian, regardless of whether OBS
// is running at all. Factored out from obsIsRunningFallback so
// process_other_test.go can drive it directly without needing ps.
func matchesOBS(psOutput []byte, names map[string]bool) bool {
	for _, line := range strings.Split(string(psOutput), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(line))
		if names[base] {
			return true
		}
	}
	return false
}
