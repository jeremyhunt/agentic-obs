//go:build !windows

package install

import (
	"os/exec"
	"strings"
)

func obsIsRunningFallback(names map[string]bool) bool {
	out, err := exec.Command("ps", "-A", "-o", "comm=").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if names[strings.TrimSpace(line)] {
			return true
		}
	}
	return false
}
