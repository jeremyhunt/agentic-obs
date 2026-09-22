# The Lua Bridge — Design

**Date:** 2026-09-21
**Status:** Approved, not yet implemented
**Decision record:** [ADR-013](../../../design/decisions/013-the-lua-bridge.md)

## Problem

ADR-012 left two capabilities unreachable, both needing code running *inside*
OBS: targeted browser-source events (`javascript_event` on a source's own proc
handler, as opposed to obs-browser's broadcast-only `emit_event`) and the
`Get/SetSourcePrivateSettings` pair that obs-websocket offers but goobs does not
generate.

ADR-013 recorded what the 6a spike measured: obs-websocket's vendor API is
closed to Lua, script reload is closed to everyone outside `frontend-tools`, and
the workable transport is a pair of source-settings mailboxes — push in both
directions, 30 ms round trip. This spec turns that into a build.

The measurements are in ADR-013 and are not repeated here.

## Constraints

Settled during design; each one shapes the architecture.

| Constraint | Consequence |
|---|---|
| Always-on fixture for every user | Install must be near-automatic; presence detection is mandatory |
| Private branch gets the scripting channel; upstream gets curated tools only | Separation must be compiler-enforced, not a promise |
| The gate must sit outside the model's reach | `set_tool_config` is a Meta tool and can enable any group, so the gate is a build tag plus an env var |
| Scripts are stored **per scene collection** | Switching collections unloads the bridge; install touches every collection; re-probe on switch |
| All obslua runs on the video thread | Chunks must be bounded; no polling or watchdog timers |

## Architecture

```
bridge/agentic-obs-bridge.lua      # the fixed script: prelude + runner (go:embed'd asset)
internal/bridge/
  transport.go                     # framing, correlation, serialisation, timeout
  presence.go                      # handshake, version check, collection-switch re-probe
  snippets/*.lua                   # curated snippets, go:embed'd (arrives with the first curated tool)
internal/mcp/
  bridge_tools.go                  # curated tools — untagged, upstream-safe (arrives with the first curated tool)
  scripting_channel.go             # //go:build scripting  → run_lua_in_obs
  scripting_channel_stub.go        # //go:build !scripting → no-op registration
main.go                            # install-bridge / uninstall-bridge subcommands
```

### The build-tag split

Server startup calls `registerScriptingChannel(s)` unconditionally; the two
files decide what it means. Plain `go build` compiles the stub, so **the
upstream binary carries no `run_lua_in_obs`**, and if a curated tool ever grows
a dependency on it the untagged build fails to compile.

The build tag alone was never the whole story, and this section used to say it
was. The transport is a pair of sources addressed by *name*, so any tool that
writes an arbitrary settings map to an arbitrary source is a second way in:
`set_source_settings` — default-enabled, never elicited — could write
`{id, lua, args}` to `agentic-obs-inbox` and read the answer out of
`agentic-obs-mailbox` on an untagged binary. Those two names are therefore
reserved against the general-purpose tools
(`internal/mcp/bridge_reserved.go`).

Stated accurately, and no more than that: the default binary contains no
*unreviewed* eval path, and the transport's source names are reserved against
every tool through which a caller names a source or a scene and something is
written — nineteen of them.

That list was got wrong twice by writing it out by hand, so it is no longer
written out by hand. `internal/mcp/bridge_surface_test.go` drives every tool
carrying a `source_name`, `scene_name`, `input_name` or `dest_scene_name` and
fails for any that neither refuses a reserved name nor carries a written reason
it need not. Nineteen refuse; twenty-two are exempt with a reason. The two
misses it was built after are instructive: `create_color_source` with
`if_exists: "update"` reached `ensureInput`, the unexported worker, past a kind
check that could not refuse it because the inbox really is a `color_source_v3`;
and eight more tools — the four filter ones, mute, volume, the properties
button and `create_scene` — were each already refused through
`call_obs_request` while their own typed tool let them through.

**The two further routes this section used to name as open are now closed**, at
their own layers rather than in `bridge_reserved.go`, because neither passes
through it.

- `call_obs_request`, a raw obs-websocket passthrough (Core group,
  default-enabled, not elicited), would issue `SetInputSettings` against any
  source. `checkRequestAllowed` (`internal/obs/dispatch.go`) now takes the
  request data as well as its type and refuses any request whose payload
  addresses a reserved name: a `*Name` or `*Uuid` key holding one, at any depth.
  Content is not a target, so a text source whose text is
  `"agentic-obs-inbox"` is still writable. `Get*` requests are left alone and
  everything else is treated as a write, including a request this build has
  never seen. Uuids are resolved at call time, because `GetInputList` stays open
  and hands back the inbox's.
- `apply_scene_spec` writes the settings of any source a caller-supplied spec
  names, with `dry_run=false`. `scenespec.Apply` now refuses such a spec whole —
  skipping the one source would report a success for an apply that did not do
  what the document asked. Dry runs and `diff_scene_spec` stay open; they write
  nothing.

Stated accurately again: what is closed is *addressing the transport by name or
by uuid through this server's tools*. Not by scene item id — `DuplicateSceneItem`
copies an existing placement with a number and writes no settings. Not against a
settings write added later without this in mind, which reaches the transport as
`set_source_settings` once did; `createTypedSource` is the worked example, since
it was missed the first time. Not `call_vendor_request`, which bypasses
`CallRequest` altogether with a vendor-defined payload — no route to *this*
bridge, whose whole premise is that the vendor API is closed to Lua, but
`ass_run_macro` reaches an operator-configured execution channel. And not
against whoever holds the obs-websocket password, who can write those settings
directly or load their own script.

The runtime gate is the env var `AGENTIC_OBS_SCRIPTING=1`, deliberately *not* a
value in SQLite: tool configuration lives there and `set_tool_config` can write
it. Even on a `-tags scripting` build the tool stays unregistered unless the
process was started with the variable set.

## Protocol

Two JSON objects. There is no `op` field and no command vocabulary — curated
tools and the scripting channel use the identical path, differing only in who
supplies the `lua`.

**Command**, written to `agentic-obs-inbox` via `SetInputSettings`:

```json
{ "id": "…", "lua": "<source>", "args": { … } }
```

**Reply**, written to `agentic-obs-mailbox` via `obs_source_update`:

```json
{ "id": "<echoed>", "ok": true, "result": { … } }
```

`args` is the injection defence and the reason the curated path is safer than
raw eval. The snippet is a **constant**; parameters arrive as a data table the
sandbox exposes as `args`. Nothing is ever concatenated into `lua`. A scene
named `]] os.execute("…")` is then just a string.

## The bridge script

A fixed asset. It changes only when the protocol changes, never to add a
capability — that is what makes the design work.

### Prelude responsibilities

1. **Sandbox**, built once at load. Exposes `obslua` whole (reaching OBS
   internals is the point), plus `string`, `table`, `math` and the safe
   builtins. Excludes `os`, `io`, `package`, `require`, `dofile`, `loadfile`,
   `load`/`loadstring`, `debug`, `setfenv`/`getfenv`, and the `raw*` /
   `*metatable` family. The last group matters: in Lua 5.1 `getmetatable("")`
   reaches the shared string metatable, so leaving it in lets a chunk poison
   every string in the process.
2. **Instruction bound**, a `debug.sethook` count hook installed by the prelude
   from *outside* the sandbox — which is how `debug` can be withheld from the
   chunk while still bounding it. Sized to one frame, fixed in the bridge, not
   settable by the caller. **Starting value: 2,000,000 instructions**, roughly
   a frame's worth, to be tuned once a real snippet enumerates a large
   collection. It is a frame-budget guard, not a security control.
3. **Result marshalling.** The chunk returns a Lua value; the prelude walks it
   into an `obs_data_t`, capped at **8 levels deep and 64 KB encoded**.
   Functions and userdata are refused as `ok:false` rather than silently
   stringified, and exceeding a cap is an `ok:false` too — never a truncated
   result that reads as success.
4. **Error wrapping.** Compile failure, runtime error and budget exhaustion all
   return `ok:false` with a message. The bridge never raises — a bridge that
   dies on a bad payload needs a manual reload, the one repair it cannot
   perform on itself.

### Lifecycle

Creates both sources on load, releases both on unload. They belong to no scene,
so they never appear in the Sources dock; they are visible only in "Add Existing
Source" and the websocket input list.

**No watchdog.** Recreating deleted sources would mean Lua running forever on
the video thread. Absence is detected on use and reported.

## Transport semantics

**Serialisation.** Exactly one command outstanding, guarded by a mutex, with a
~2 s timeout against a 30 ms measured round trip. This is correctness, not
caution: `obs_source_update` *merges*, so two rapid writes can coalesce into one
`update` signal and silently lose a command. Replies whose `id` does not match
the outstanding request are dropped.

**Presence** needs no protocol. If the bridge is not loaded the inbox source does
not exist, so `SetInputSettings` fails immediately with an obs-websocket error —
detection without waiting for a timeout.

**Version** is one probe shipping `return bridge_version`, re-run on
`CurrentSceneCollectionChanged`. A mismatch means the installed `.lua` is stale
relative to the binary; the remedy is re-running `install-bridge`.

## Install

`agentic-obs install-bridge [--all | --collection NAME] [--dry-run]`, with
`uninstall-bridge` for symmetry.

Writes the embedded `.lua` to a stable per-user path — `%APPDATA%/agentic-obs/bridge/`
on Windows, `$XDG_DATA_HOME/agentic-obs/bridge/` (falling back to
`~/.local/share/...`) elsewhere — then adds it to
`modules.scripts-tool` in each target collection. Four safeguards, because this
file holds the user's entire scene setup:

- **Refuses to run while OBS is running.** OBS rewrites the collection on save
  and would clobber the edit.
- **Timestamped backup** of every file touched.
- **Idempotent.** Re-running does not duplicate the entry; upgrades are a
  re-run.
- **Number-safe round-trip.** JSON is decoded with `UseNumber()`, never into
  `float64`. Scene-item IDs are `int64` and a naive decode mangles anything past
  2^53. This is the detail that would corrupt a config.

## MCP surface (v1)

- `run_lua_in_obs(lua, args?)` — build-tagged, env-gated, elicited per call,
  chunk recorded to action history.
- `get_obs_status` gains `bridge: {present, version, collection}`.

`run_lua_in_obs` gets its **own `Scripting` tool group**. ADR-012 rejected a new
group for three tools on edit-site cost, but here the group *is* the separation
boundary: it appears and disappears with the build tag, so `list_tool_groups`
stays self-describing and upstream has no such group at all.

The three controls are not redundant and do not overlap. The **build tag**
decides whether the code exists in the binary; the **env var** decides whether
it registers at startup; the **tool group** only lets a user turn an
already-permitted tool off. `set_tool_config` can therefore disable the group
but can never bring it into being.

## Changes to existing code

Both are forced by the design and are small.

1. **`eventFrom` gains `InputSettingsChanged`.** It is currently untranslated, so
   it reaches no sink. Once translated it becomes a visible OBS event reaching
   automation triggers and MCP notifications — so **the bridge's two sources must
   be filtered as plumbing**, or the transport spams the engine ADR-010 exists to
   protect.
2. **The event sink becomes composable.** It is a single field owned by
   `NewEventHandler`; the bridge needs to be a peer listener rather than a
   special case inside `handleOBSEventNotification`.

Two existing tests also need the tagged build accounted for:
`TestRegisteredToolsMatchMetadata` compares served tools against
`toolGroupMetadata`, and `HelpToolCount` is hand-written. Both break under
`-tags scripting` unless the metadata entry and help text are registered from
the tagged file.

## Testing

Split along the language boundary, because CGO is out (ADR-001) and Go cannot
reach the Lua directly.

| Layer | Approach |
|---|---|
| Framing, correlation, timeout, stale-reply drop | Go unit tests against the existing fake; no OBS |
| **Sandbox** | Asserted from outside over the real transport in the `obslive` suite: ship `return os == nil` and assert `ok:true`; likewise `loadstring`, `debug`, the metatable family |
| Instruction bound | Ship a loop; assert budget exhaustion rather than a hung OBS |
| Install | Fixture collection files: backup created, idempotency, large integers preserved byte-for-byte, unrelated keys untouched |
| End to end | Round trip, absence when unloaded, version mismatch, collection-switch re-probe |

Testing the sandbox in the real interpreter rather than a mock is the point: a
sandbox asserted against a fake proves nothing about Lua 5.1.

## Out of scope for v1

- **Curated capability tools.** `emit_browser_event` and the private-settings
  pair land next, on a transport that will by then be proven.
- **A watchdog** recreating deleted transport sources.
- **Upstreaming.** The untagged build ships a transport nothing calls, so the
  PR is not viable until at least one curated capability exists — otherwise it
  is all mechanism and no feature.

## Risks

- **The scripting channel is arbitrary code execution inside the OBS process**,
  by design. The sandbox reduces the blast radius of an agent's mistake; it is
  not a boundary against an adversary, and Lua sandbox escapes are a known
  class. It is no boundary at all against whoever holds the obs-websocket
  password — they could install their own script.
- **A chunk stalls rendering.** All obslua runs on the video thread. The
  instruction bound catches runaway loops, not one slow call.
- **`pcall` does not catch memory faults.** obslua hands out real pointers; a
  double-release or use-after-free takes the OBS process down mid-stream.
- **Install edits the file holding the user's scenes.** Mitigated by requiring
  OBS closed, backups, idempotency and `UseNumber()` — not eliminated.
- **A user can delete the transport sources**, breaking the bridge until reload.
  Detected on use, reported, not defended against.
