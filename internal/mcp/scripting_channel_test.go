//go:build scripting

package mcp

import (
	"testing"
)

func TestScriptingChannelStaysOffWithoutTheEnvVar(t *testing.T) {
	t.Setenv("AGENTIC_OBS_SCRIPTING", "")

	if scriptingEnabled() {
		t.Fatal("the scripting channel reported enabled with the variable unset")
	}
}

func TestScriptingChannelTurnsOnWithTheEnvVar(t *testing.T) {
	t.Setenv("AGENTIC_OBS_SCRIPTING", "1")

	if !scriptingEnabled() {
		t.Fatal("the scripting channel stayed off with AGENTIC_OBS_SCRIPTING=1")
	}
}

// Anything other than the documented value is treated as off. A half-set
// variable must not open this.
func TestScriptingChannelIgnoresOtherValues(t *testing.T) {
	for _, value := range []string{"0", "false", "yes", "true", " "} {
		t.Setenv("AGENTIC_OBS_SCRIPTING", value)
		if scriptingEnabled() {
			t.Errorf("AGENTIC_OBS_SCRIPTING=%q enabled the channel", value)
		}
	}
}
