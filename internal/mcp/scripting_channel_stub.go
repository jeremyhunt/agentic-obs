//go:build !scripting

package mcp

// registerScriptingChannel does nothing in the default build.
//
// run_lua_in_obs runs arbitrary code inside the OBS process, so it is compiled
// out rather than merely switched off. A tool group would not do: set_tool_config
// is a Meta tool that is always enabled and can turn any group on, so a group
// the model can see is a group the model can enable. Build with
// `-tags scripting` and set AGENTIC_OBS_SCRIPTING=1 to include it.
//
// Compiling the tool out is not by itself the whole gate, and claiming it was
// overstated the case. The bridge's transport is addressed by source name, so
// a general-purpose settings write reaches it in any build; those two names
// are reserved separately, in bridge_reserved.go. What this file guarantees is
// narrower: the default binary carries no unreviewed eval path.
func registerScriptingChannel(s *Server) {}

// scriptingToolNames is empty here: the default build serves no scripting
// tools.
func scriptingToolNames() []string { return nil }

// scriptingEnabled is always false here, with no env var check at all -- so
// the string "AGENTIC_OBS_SCRIPTING" does not appear in this binary either.
// getGroupEnabled's "Scripting" case (tool_config.go) needs this symbol in
// every build, even though the untagged build never adds a "Scripting" entry
// to toolGroupMetadata for that case to be reached against.
func scriptingEnabled() bool { return false }
