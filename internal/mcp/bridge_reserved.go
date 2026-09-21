package mcp

import (
	"fmt"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// The bridge's transport sources are reserved against the general-purpose
// tools.
//
// The transport is a pair of sources addressed by NAME (internal/obs:
// BridgeInboxSource and BridgeMailboxSource, aliased by internal/bridge as
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
// The two routes this file used to name as open are now closed, each at its
// own layer rather than here, because neither passes through these helpers:
// call_obs_request refuses a request whose payload addresses the transport by
// name or by uuid (internal/obs/dispatch.go), and apply_scene_spec refuses a
// spec that names it once dry_run is false (internal/scenespec/apply.go).
// Together those close addressing the transport by name through this server's
// tools. What none of it touches, and what no amount of guarding here could:
// whoever holds the obs-websocket password can write those settings directly,
// or load a script of their own.
//
// isBridgeTransport delegates to obs.IsBridgeTransport rather than comparing
// the constants again, so there is one predicate for all four guard sites and
// no second copy to drift.
func isBridgeTransport(name string) bool {
	return obs.IsBridgeTransport(name)
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
