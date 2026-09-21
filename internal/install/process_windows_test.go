//go:build windows

package install

import "testing"

var obsNames = map[string]bool{"obs64.exe": true, "obs.exe": true, "obs": true}

// This fixture is not invented: it is a real excerpt of `tasklist /fo csv
// /nh` output captured on this machine while both OBS Studio and a separate,
// independently-running agentic-obs.exe (the MCP server backing this very
// session's OBS tools) were live. It is the exact scenario that broke the
// previous whole-blob Contains check: this tool's own name is right there
// next to OBS's.
const realTasklistExcerpt = `"System Idle Process","0","Services","0","8 K"
"explorer.exe","6012","Console","1","245,004 K"
"obs64.exe","12132","Console","1","550,332 K"
"agentic-obs.exe","15276","Console","1","29,032 K"
`

func TestMatchesOBSFindsTheRealProcessAmongstItsOwn(t *testing.T) {
	if !matchesOBS([]byte(realTasklistExcerpt), obsNames) {
		t.Error("did not detect obs64.exe in a real tasklist dump that contains it")
	}
}

// The regression this guards against: a previous version matched on
// "agentic-obs.exe" alone, because it contains "obs" as a substring. This
// fixture is the same real capture with the obs64.exe row removed, so if the
// substring bug ever comes back, this is the test that catches it.
func TestMatchesOBSDoesNotMatchItsOwnProcess(t *testing.T) {
	withoutOBS := `"System Idle Process","0","Services","0","8 K"
"explorer.exe","6012","Console","1","245,004 K"
"agentic-obs.exe","15276","Console","1","29,032 K"
`
	if matchesOBS([]byte(withoutOBS), obsNames) {
		t.Error("matched with only agentic-obs.exe present -- the self-match regression is back")
	}
}

func TestMatchesOBSHandlesNoOBSAndNoAgenticObs(t *testing.T) {
	plain := `"System Idle Process","0","Services","0","8 K"
"explorer.exe","6012","Console","1","245,004 K"
`
	if matchesOBS([]byte(plain), obsNames) {
		t.Error("matched with neither process present")
	}
}

func TestMatchesOBSIsCaseInsensitiveOnTheImageNameOnly(t *testing.T) {
	// Windows image names in tasklist are consistently lowercase in
	// practice, but the comparison itself must not depend on that.
	mixedCase := `"OBS64.EXE","12132","Console","1","550,332 K"
`
	if !matchesOBS([]byte(mixedCase), obsNames) {
		t.Error("did not match OBS64.EXE case-insensitively")
	}
}

func TestMatchesOBSHandlesEmptyOutput(t *testing.T) {
	if matchesOBS([]byte(""), obsNames) {
		t.Error("matched on empty tasklist output")
	}
}
