//go:build windows

package install

import (
	"os/exec"
	"strings"
)

// obsIsRunningFallback asks Windows for the task list. tasklist is present on
// every supported Windows and needs no elevation.
func obsIsRunningFallback(names map[string]bool) bool {
	out, err := exec.Command("tasklist", "/fo", "csv", "/nh").Output()
	if err != nil {
		return false // cannot tell; the command warns instead of blocking
	}
	lower := strings.ToLower(string(out))
	for name := range names {
		if strings.Contains(lower, strings.ToLower(name)) {
			return true
		}
	}
	return false
}
