package obs

// The Lua bridge's transport source names.
//
// They are declared here, in the lowest layer, rather than in internal/bridge
// where the transport itself lives, because internal/bridge imports this
// package: a guard in internal/obs cannot import the constants back out of it
// without a cycle. internal/bridge aliases these two (InboxSource,
// MailboxSource), so there is still exactly one literal for each name and a
// rename cannot leave a guard pointing at a stale copy.
//
// Why anything guards them at all: the transport is a pair of sources
// addressed by NAME, and agentic-obs drives it by writing {id, lua, args} to
// the inbox's settings. So anything that can write an arbitrary settings map
// to an arbitrary source name can run arbitrary code inside the OBS process --
// reached without the scripting channel's build tag, without
// AGENTIC_OBS_SCRIPTING and without its per-call confirmation. That is a
// different class of hazard from the destructive-but-bounded requests the
// passthrough's deny-list guards, which is why the names are reserved
// separately from it. See ADR-012 and ADR-013 decision 6.
const (
	// BridgeInboxSource carries commands in, BridgeMailboxSource answers out.
	BridgeInboxSource   = "agentic-obs-inbox"
	BridgeMailboxSource = "agentic-obs-mailbox"
)

// IsBridgeTransport reports whether name is one of the bridge's two transport
// sources.
//
// The comparison is exact, because OBS source names are: obs_get_source_by_name
// does not fold case or trim, so "Agentic-OBS-Inbox" and "agentic-obs-inbox "
// address nothing and are ordinary names a caller may legitimately use.
func IsBridgeTransport(name string) bool {
	return name == BridgeInboxSource || name == BridgeMailboxSource
}
