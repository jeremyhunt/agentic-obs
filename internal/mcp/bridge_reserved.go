package mcp

import (
	"fmt"

	"github.com/ironystock/agentic-obs/internal/bridge"
)

// The bridge's transport sources are reserved against the general-purpose
// tools.
//
// The transport is a pair of sources addressed by NAME (internal/bridge:
// InboxSource and MailboxSource). Anything that can write an arbitrary
// settings map to an arbitrary source name can therefore write
// {id, lua, args} to the inbox and read the answer back out of the mailbox --
// which is the scripting channel's exact payload, reached without its build
// tag, without AGENTIC_OBS_SCRIPTING, and without the per-call confirmation.
// set_source_settings is in the default-enabled Sources group, so on a stock
// build that was arbitrary in-process code execution through an always-on
// tool.
//
// Removing or duplicating the transport is not an escalation, but ADR-013
// already lists deleting the inbox as a hazard: it breaks the bridge with no
// error anywhere. Both are refused here for the same reason.
//
// Reads (get_source_settings, list_sources) are deliberately left alone. They
// expose nothing that get_obs_status's bridge field does not already report.
//
// This does NOT make the transport unreachable, and should not be read as if
// it did. Two tools still write a source by name and are not guarded here:
// call_obs_request, a raw obs-websocket passthrough that will issue
// SetInputSettings against anything, and apply_scene_spec, which writes
// whatever source a caller-supplied spec names. Guarding either means deciding
// what a deliberately unrestricted escape hatch may reach -- a design question,
// recorded in ADR-013 decision 6 rather than answered here.

// isBridgeTransport reports whether name is one of the bridge's two transport
// sources. It compares against the constants so a rename in internal/bridge
// cannot leave this guard pointing at a stale literal.
func isBridgeTransport(name string) bool {
	return name == bridge.InboxSource || name == bridge.MailboxSource
}

// errBridgeTransportWrite refuses a settings write to the transport.
func errBridgeTransportWrite(tool, name string) error {
	return fmt.Errorf(
		"%q is the Lua bridge's transport and is not writable through %s: a settings write there runs code "+
			"inside OBS, which is what the scripting channel's build tag, AGENTIC_OBS_SCRIPTING and per-call "+
			"confirmation exist to gate. Use run_lua_in_obs to run Lua in OBS",
		name, tool)
}

// errBridgeTransportChange refuses a structural change to the transport.
func errBridgeTransportChange(tool, name string) error {
	return fmt.Errorf(
		"%q is the Lua bridge's transport and is not changeable through %s: removing or copying it breaks the "+
			"bridge with no error anywhere. Use 'agentic-obs uninstall-bridge' to take the bridge out",
		name, tool)
}
