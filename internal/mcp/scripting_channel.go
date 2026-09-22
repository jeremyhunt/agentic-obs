//go:build scripting

package mcp

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// scriptingEnabled reports whether the operator opted in.
//
// The value must be exactly "1". Anything else is off, because a variable left
// half-set should not open arbitrary code execution. It is an environment
// variable rather than stored configuration on purpose: tool configuration
// lives in SQLite and set_tool_config can write it, so a stored flag would be
// a gate the model could open for itself.
func scriptingEnabled() bool {
	return os.Getenv("AGENTIC_OBS_SCRIPTING") == "1"
}

// RunLuaInput is the scripting channel's input.
type RunLuaInput struct {
	Lua  string         `json:"lua" jsonschema:"Lua source to run inside OBS. It is compiled and run in a sandbox with obslua available; os, io and debug are not. Return a value to get it back."`
	Args map[string]any `json:"args,omitempty" jsonschema:"Values the chunk reads as the args table. Pass data here rather than building it into the source."`
}

// init publishes the Scripting group, but only when the channel is actually
// on. The group and the tool are gated on the same check so they cannot
// disagree -- a group advertising a tool the server does not serve is exactly
// what TestRegisteredToolsMatchMetadata exists to catch.
//
// The default build has no such group at all, so list_tool_groups does not
// hint at a capability that is not there.
func init() {
	if !scriptingEnabled() {
		return
	}

	toolGroupMetadata["Scripting"] = &ToolGroupMetadata{
		Name: "Scripting",
		Description: "Runs Lua inside the OBS process through the bridge. Present only in builds made with " +
			"-tags scripting and started with AGENTIC_OBS_SCRIPTING=1.",
		ToolNames: []string{"run_lua_in_obs"},
	}
	ToolGroupOrder = append(ToolGroupOrder, "Scripting")

	// TestHelpContentCompleteness fails for any registered tool with no help
	// entry, so the help travels with the tool rather than in help_tools.go
	// where the default build would also carry it.
	toolHelpContent["run_lua_in_obs"] = runLuaHelp
}

// scriptingToolNames lets the count tests (TestHelpToolCountMatchesRegisteredTools,
// TestTotalToolCountMatchesMetadata) account for a tool that exists only in
// this build -- and, like everything else in this file, only when the
// operator actually turned it on. It must share init()'s exact condition:
// HelpToolCount deliberately excludes this tool, so the moment this list and
// toolGroupMetadata's "Scripting" entry disagreed about when they're present,
// those tests would go back to failing under a real deployment
// (AGENTIC_OBS_SCRIPTING=1 set before the process starts, not toggled
// mid-test with t.Setenv).
func scriptingToolNames() []string {
	if !scriptingEnabled() {
		return nil
	}
	return []string{"run_lua_in_obs"}
}

const runLuaHelp = `run_lua_in_obs — run Lua inside the OBS process

Compiles and runs a chunk through the agentic-obs bridge. Returns whatever the
chunk returns.

  lua   Source to run. ` + "`return`" + ` a value to receive it.
  args  Data the chunk reads as the args table. Put parameters here; building
        them into the source is how a scene name becomes code.

The chunk runs sandboxed: obslua is available; os, io, package, debug,
LuaJIT's own ffi/jit/bit, and the loadstring/setfenv family are not.

What comes back:

  * Only the FIRST return value. The chunk is run through pcall and only its
    first result is read, so ` + "`return a, b`" + ` loses b. Return a table instead.
  * A Lua array arrives as an object. Every key is stringified on the way out,
    so ` + "`return {10, 20}`" + ` comes back as {"1": 10, "2": 20}, not a list.
  * Limits are failures, never truncation: deeper than 8 levels, or over 64 KB
    (keys counted too), comes back ok=false -- as does a function or userdata.

It runs on OBS's render thread, so a slow chunk drops frames. OBS's Lua is
LuaJIT, which stops checking an instruction hook the moment a loop compiles to
native code, so every chunk runs interpreted (jit.off) to keep the budget
below real. OBS also replaces the global error/print with logging shims
before a script loads, so the budget signals via assert rather than error --
error would silently log and let the chunk keep running instead of stopping
it, and the same shim means a chunk's own error() call only logs rather than
failing the call that made it. A chunk is interrupted after 2,000,000
instructions, which stops an ordinary runaway loop -- but a loop the chunk
re-enters via its own pcall (in an unprotected outer loop of its own) catches
that interruption every time and keeps running, or one spent inside a single
long call (e.g. string.rep) is not counted at all, and OBS stays wedged until
someone closes it.`

func registerScriptingChannel(s *Server) {
	if !scriptingEnabled() {
		return
	}

	log.Println("scripting channel ENABLED: run_lua_in_obs runs arbitrary code inside OBS")

	mcpsdk.AddTool(s.mcpServer,
		&mcpsdk.Tool{
			Name: "run_lua_in_obs",
			Description: "Run Lua inside the OBS process through the agentic-obs bridge. " +
				"This executes arbitrary code in OBS on its render thread: a slow chunk drops frames. " +
				"Pass parameters in args rather than building them into the source.",
		},
		s.handleRunLuaInOBS,
	)
}

func (s *Server) handleRunLuaInOBS(ctx context.Context, request *mcpsdk.CallToolRequest, input RunLuaInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	if input.Lua == "" {
		return nil, nil, fmt.Errorf("lua is required")
	}

	// Per call, not once per session. The environment variable authorises the
	// capability; this authorises the specific chunk, and the chunk is the
	// part that varies.
	confirmed, err := ElicitConfirmation(ctx, getSession(request),
		fmt.Sprintf("Run this Lua inside OBS?\n\n%s", input.Lua))
	if err != nil {
		return nil, nil, err
	}
	if !confirmed {
		return nil, CancelledResult("run_lua_in_obs"), nil
	}

	result, err := s.bridge.Run(ctx, input.Lua, input.Args)
	if err != nil {
		// A transport failure is evidence the cached presence is stale.
		s.bridge.Invalidate()
		s.recordAction("run_lua_in_obs", "Run Lua in OBS", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	// The whole chunk goes into the history. "Ran Lua" is already harder to
	// audit than a named request; recording only the outcome would make it
	// impossible.
	s.recordAction("run_lua_in_obs", "Run Lua in OBS", input, result, result.OK, time.Since(start))
	return nil, result, nil
}
