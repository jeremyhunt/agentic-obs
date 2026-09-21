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
// case -- it is the only detection OBSIsRunning has. ps -o comm= can report
// either a bare name ("OBS") or a full bundle path
// (/Applications/OBS.app/Contents/MacOS/OBS) depending on how the process was
// launched, and macOS commonly capitalizes it, so a case-sensitive exact
// match against "obs" silently never fired there -- the guard passed while
// OBS was actually running. Taking each line's base name and comparing
// case-insensitively mirrors what process_windows.go already does against
// its own tasklist output; over-matching (e.g. a hypothetical process with
// "obs" elsewhere in its name) is the safe direction, same as Windows.
func obsIsRunningFallback(names map[string]bool) bool {
	out, err := exec.Command("ps", "-A", "-o", "comm=").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(line))
		for name := range names {
			if strings.Contains(base, strings.ToLower(name)) {
				return true
			}
		}
	}
	return false
}
