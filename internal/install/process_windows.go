//go:build windows

package install

import (
	"bytes"
	"encoding/csv"
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
	return matchesOBS(out, names)
}

// matchesOBS parses tasklist's CSV rows and compares each row's image-name
// field (the first column) case-insensitively against names.
//
// It must look at only that field, not substring-match the whole blob: this
// process's own name, "agentic-obs.exe", is in that blob on every single
// invocation (tasklist lists itself), and it contains "obs" as a substring.
// A whole-blob Contains check made the guard unconditionally true regardless
// of whether OBS was running at all -- worse than no guard, since a check
// that always trips is one users learn to --force past. Factored out from
// obsIsRunningFallback so process_windows_test.go can drive it with real
// captured tasklist output without needing to run tasklist itself.
func matchesOBS(csvOutput []byte, names map[string]bool) bool {
	reader := csv.NewReader(bytes.NewReader(csvOutput))
	reader.FieldsPerRecord = -1 // rows aren't guaranteed uniform; don't abort the scan over it
	for {
		record, err := reader.Read()
		if err != nil {
			break // EOF, or a row too broken to parse -- either way, done
		}
		if len(record) == 0 {
			continue
		}
		image := strings.ToLower(strings.TrimSpace(record[0]))
		if names[image] {
			return true
		}
	}
	return false
}
