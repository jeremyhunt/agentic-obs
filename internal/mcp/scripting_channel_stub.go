//go:build !scripting

package mcp

// registerScriptingChannel does nothing in the default build.
//
// run_lua_in_obs runs arbitrary code inside the OBS process, so it is compiled
// out rather than merely switched off. A tool group would not do: set_tool_config
// is a Meta tool that is always enabled and can turn any group on, so a group
// the model can see is a group the model can enable. Build with
// `-tags scripting` and set AGENTIC_OBS_SCRIPTING=1 to include it.
func registerScriptingChannel(s *Server) {}

// scriptingToolNames is empty here: the default build serves no scripting
// tools.
func scriptingToolNames() []string { return nil }
