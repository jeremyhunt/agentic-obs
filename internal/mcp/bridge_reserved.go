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
// spec that names it, when the apply is not a dry run and its field mask can
// reach a source (internal/scenespec/apply.go).
//
// Together those close addressing the transport BY NAME OR BY UUID through
// this server's tools. Three things they do not close, stated here because
// this file is what the rest of the documentation cites. A scene item id is
// not a name: call_obs_request can still copy an existing placement of the
// transport with DuplicateSceneItem. call_vendor_request does not pass through
// any of this. And whoever holds the obs-websocket password can write those
// settings directly, or load a script of their own.
//
// There is no fixed number of guard sites, and there should not be one written
// down: the count has been wrong at every previous revision of this comment.
// internal/mcp/bridge_surface_test.go is the authority instead -- it drives
// every tool that lets a caller name a source or a scene and fails for any
// that neither refuses nor carries a written reason it need not.
//
// isBridgeTransport delegates to obs.IsBridgeTransport rather than comparing
// the constants again, so every guard site shares one predicate with
// internal/obs and internal/scenespec, with no second copy to drift.
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

// errBridgeTransportReserved refuses a write to the transport that is not
// itself an escalation.
//
// Filters, mute, volume and a properties button all write the transport source
// without reaching its settings, so none of them runs Lua: only the inbox's own
// update signal does that, and only a settings write raises it. They are
// refused anyway, for two reasons worth saying out loud rather than leaving to
// be re-derived. The reservation is then ONE rule -- agentic-obs does not write
// the transport -- instead of a per-tool judgement about which writes happen to
// be harmless, and that judgement is exactly what was got wrong twice. And
// call_obs_request already refuses every one of these requests, so without this
// the same operation was denied through the passthrough and allowed through its
// typed tool.
func errBridgeTransportReserved(tool, name string) error {
	return fmt.Errorf(
		"%q is the Lua bridge's transport and agentic-obs does not write it through %s. This particular "+
			"write does not run code -- only a settings write on the inbox does that -- but the transport "+
			"is reserved against every tool that writes it by name, so that the rule is one rule and so "+
			"that this tool agrees with call_obs_request, which refuses the same request. Use "+
			"'agentic-obs uninstall-bridge' to take the bridge out",
		name, tool)
}

// errBridgeTransportSceneName refuses a scene under a reserved name.
//
// OBS keeps scenes and sources in ONE namespace -- a scene is an obs_source_t,
// and obs_get_source_by_name does not distinguish them -- so a scene called
// agentic-obs-inbox collides with the transport rather than sitting beside it.
// What OBS does with the collision is not worth finding out on a live machine.
func errBridgeTransportSceneName(tool, name string) error {
	return fmt.Errorf(
		"%q is the Lua bridge's transport and is not available as a scene name through %s: OBS keeps scenes "+
			"and sources in one namespace, so a scene under that name collides with the transport. Pick "+
			"another name, or use 'agentic-obs uninstall-bridge' to take the bridge out",
		name, tool)
}
