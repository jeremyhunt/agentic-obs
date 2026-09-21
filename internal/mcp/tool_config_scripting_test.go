//go:build scripting

package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withScriptingGroup makes sure "Scripting" is in the metadata for this test.
//
// init() publishes it only when the process was STARTED with
// AGENTIC_OBS_SCRIPTING=1, which t.Setenv cannot arrange after the fact -- so
// the entry is injected when it is missing and taken back out afterwards.
func withScriptingGroup(t *testing.T) {
	t.Helper()
	if _, ok := toolGroupMetadata["Scripting"]; ok {
		return
	}
	toolGroupMetadata["Scripting"] = &ToolGroupMetadata{
		Name:      "Scripting",
		ToolNames: []string{"run_lua_in_obs"},
	}
	ToolGroupOrder = append(ToolGroupOrder, "Scripting")
	t.Cleanup(func() {
		delete(toolGroupMetadata, "Scripting")
		ToolGroupOrder = ToolGroupOrder[:len(ToolGroupOrder)-1]
	})
}

// set_tool_config used to echo the request back, so disabling Scripting
// reported success while changing nothing. The failure that makes this worth a
// test: an operator disables it, sees "disabled", hands the session on, and
// run_lua_in_obs is still live.
func TestSetToolConfigReportsThatScriptingDidNotChange(t *testing.T) {
	t.Setenv("AGENTIC_OBS_SCRIPTING", "1")
	withScriptingGroup(t)

	server, _, _ := testServerForToolConfig(t)

	_, result, err := server.handleSetToolConfig(context.Background(), nil, SetToolConfigInput{
		Group:   "Scripting",
		Enabled: false,
		Persist: true,
	})
	require.NoError(t, err)

	res, ok := result.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, true, res["new_state"], "the tool is still served, so the report must say enabled")
	assert.Equal(t, false, res["requested_state"], "what was asked for is reported separately")
	assert.Equal(t, false, res["persisted"], "nothing of this group's was persisted")

	message, _ := res["message"].(string)
	assert.Contains(t, message, "NOT changed")
	assert.Contains(t, message, "AGENTIC_OBS_SCRIPTING", "the response must name what actually controls this")

	assert.True(t, server.getGroupEnabled("Scripting"), "the channel is unchanged")
}

// Enabling an already-enabled Scripting is not a lie, so it reports normally.
func TestSetToolConfigReportsScriptingEnabledWhenItIs(t *testing.T) {
	t.Setenv("AGENTIC_OBS_SCRIPTING", "1")
	withScriptingGroup(t)

	server, _, _ := testServerForToolConfig(t)

	_, result, err := server.handleSetToolConfig(context.Background(), nil, SetToolConfigInput{
		Group:   "Scripting",
		Enabled: true,
	})
	require.NoError(t, err)

	res, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, res["new_state"])
	assert.NotContains(t, res, "requested_state", "nothing diverged, so there is nothing to explain")
}

// The ordinary groups are unaffected: they still report what was asked,
// because for them the write actually takes.
func TestSetToolConfigStillReportsOrdinaryGroups(t *testing.T) {
	server, _, _ := testServerForToolConfig(t)

	_, result, err := server.handleSetToolConfig(context.Background(), nil, SetToolConfigInput{
		Group:   "Layout",
		Enabled: false,
	})
	require.NoError(t, err)

	res, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, false, res["new_state"])
	assert.NotContains(t, res, "requested_state")
	assert.False(t, server.getGroupEnabled("Layout"))
}
