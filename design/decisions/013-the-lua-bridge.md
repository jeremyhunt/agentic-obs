# ADR-013: The Lua Bridge — Shipping Code Instead of Reloading It

**Status:** Accepted
**Date:** 2026-09-21

## Context

ADR-012 closed the gap between the 151 requests obs-websocket advertises and
the ~65 `internal/obs` wraps, and left exactly two things it could not reach,
both needing code running *inside* OBS:

- **Targeted browser-source events.** `CallVendorRequest` reaches obs-browser's
  `emit_event`, but that is `DispatchJSEvent(…, nullptr)` — broadcast to every
  browser source. The targeted channel is `javascript_event` on each source's
  own proc handler, reachable only from in-process.
- **`Get/SetSourcePrivateSettings` and the scene-item pair.** obs-websocket
  offers them; goobs does not generate them.

A bridge needs a transport in both directions. obs-websocket's vendor API was
the obvious candidate — it is what every C++ plugin uses, and `vendor_event_emit`
would answer over the socket agentic-obs already holds.

**It is closed to Lua, and the spike measured why.** Reading the registration
side of obs-websocket settles that exactly one proc is global,
`obs_websocket_api_get_ph`; everything else lives on the handler it returns.
Lua can read that handler. It cannot hand it back:

```
expected 'struct proc_handler *|proc_handler_t *'
got 'os_performance_token_t *|gs_sync_t *|void *'
```

`calldata_ptr` returns a bare `void*` and SWIG will not retype it. Three
measurements make that an answer rather than a failed run:

| Probe | Result |
|---|---|
| Control: a nonexistent proc on a *typed* handler | "not registered", not a SWIG refusal — the binding calls procs fine |
| Inventory: all 41 `proc`/`calldata` symbols in obslua | casters are `calldata_source` and `calldata_sceneitem`, nothing else |
| Raw goobs event dump, harness proven with a scratch scene | no vendor events at all — not a drop on the Go side |

So the vendor API is unreachable because one pointer cannot be named, not
because obslua is too weak. `call_request` lives on that same handler and is
closed to Lua for the identical reason — the spike never reached its probe. It
would be a dead end regardless, and for a C++ bridge it still is: it returns an
opaque `obs_websocket_request_response*` with no binding to read it and no
reachable `obs_websocket_request_response_free`, so every call leaks the struct
and its two strings. That reason is read from the header, not measured.

**A second finding reframed the problem.** Iterating on an in-OBS script means
reloading it, and nothing outside one plugin can. `obs_script_reload()` is
exported, but the `obs_script_t*` handles live in a file-scope
`static ScriptData *` inside `plugins/frontend-tools`, which registers no proc,
no vendor and no hotkey. `obs-scripting.c` keeps no registry, so there is
nothing to enumerate; `obs-frontend-api.h` has no script functions; obs-websocket
has no script request; and `obslua.i` includes `obs-frontend-api.h` but **not**
`obs-scripting.h`, confirmed at runtime as `obs_script_reload bound: false`.

Reload is closed to everyone. But `obs-scripting-lua.c` calls `luaL_openlibs`
with no sandbox, confirmed at runtime as `loadstring available: true`.

## Decision

**The bridge is a fixed, thin script that runs code handed to it, and the
transport is a pair of source-settings mailboxes.**

### 1. Two sources, one per direction

```
agentic-obs ── SetInputSettings ──▶ agentic-obs-inbox
                                      │ libobs raises "update"
                                      ▼
                                    bridge runs the payload
                                      │ obs_source_update
                                      ▼
agentic-obs ◀── InputSettingsChanged ── agentic-obs-mailbox
```

Both directions are **push**. The plan had recorded the fallback as
`SetInputSettings` plus a `GetInputSettings` poll on a correlation id; the poll
is unnecessary, because `InputSettingsChanged` carries the whole new settings
object and is raised for writes made inside OBS. Measured end to end at **30 ms**,
sending `return 6 * 7` and reading `42` back.

**Two sources rather than one, deliberately.** A single source echoes every
write back to whoever made it: agentic-obs would receive its own commands as
`InputSettingsChanged`, and the bridge would re-run its own replies as commands.
That is the identity problem ADR-010 solved once for automation rules, and a
second source means nobody solves it twice.

A command is identified by its **`id`**, not by the signal. Creating a source
raises `update` too, and OBS coalesces writes. `obs_source_update` *merges*
rather than replaces, so a mailbox retains keys from earlier replies — only an
id the caller invented distinguishes this answer from a leftover.

### 2. The capability is "run this", not a command vocabulary

A fixed vocabulary would need a bridge change, and therefore a manual reload,
for every new thing the bridge can do — which is the problem, not the solution.
Shipping code inverts it: the bridge stays stable and stupid, and the
experiments ride on top. This is also strictly more capable than the reload tool
we cannot build, so the closed door costs nothing.

### 3. Errors are values, never raised

Compile failures and runtime failures both return `{ok=false, result=…}` through
`pcall`. A bridge that dies on a bad payload has to be reloaded by hand, and
that is the one repair this design cannot perform on itself.

### 4. The chunk is sandboxed and bounded

`luaL_openlibs` opens `os` and `io`, so an unsandboxed chunk can read files and
`os.execute`. The chunk therefore runs under `setfenv` with an environment
carrying `obslua` and the safe stdlib, and **not** `os`, `io`, `package`,
`require`, `dofile`, `loadfile` or `debug`.

It is also bounded by instruction count via `debug.sethook` — the same call
FB-86 made for runaway automation rules — which stops an *ordinary* runaway
loop. It is not a hard bound, and an earlier draft of this ADR said it was. A
chunk that wraps its own loop in its own `pcall` catches the error the hook
raises like any other error, so the hook re-arms, the loop continues, and OBS
stays wedged with no upper bound: Lua 5.1 gives a hook no way to raise an error
a script-level `pcall` cannot catch, and a count hook cannot yield across the C
boundary either, so a coroutine would not help. The real backstop in that case
is the Go side's 2 s transport timeout, which lets agentic-obs report failure
and carry on — it does not unwedge OBS. A bridge that was not itself Lua could
close this; this one cannot.

**What this is and is not.** It is blast-radius reduction against an agent's
mistakes. It is **not** a security boundary against whoever holds the
obs-websocket password: they can already drive all of OBS, and could install
their own script. Lua sandboxes have known escapes, and this one is defense in
depth, not a guarantee.

### 5. The chunk runs on the video thread

`obs_source_update` on a video source only bumps `defer_update_count`; the
`update` signal is raised later by `obs_source_deferred_update`, called from
`obs_source_video_tick` → `tick_sources` → the video thread. obslua timers are
no escape: `lua_tick` is registered as a tick callback and runs there too.

**All obslua execution occupies OBS's render loop**, so a chunk that blocks
drops frames on a live stream. The bridge inherits this from OBS scripting
generally rather than introducing it — every script in the workspace already has
it — but an agent authoring chunks makes it much easier to hit, which is the
other reason for the instruction bound above.

### 6. The transport's source names are reserved

Addressing the transport by name is what makes it simple, and it is also what
makes it reachable. Any tool that writes an arbitrary settings map to a source
chosen by name can write `{id, lua, args}` to `agentic-obs-inbox` and read the
answer out of `agentic-obs-mailbox` — which is the whole protocol, reached
without the scripting channel's build tag, environment variable or per-call
confirmation. `set_source_settings` did exactly that, from the default-enabled
Sources group.

The two names are therefore refused by every tool that names a source and writes
it: the general-purpose ones — `set_source_settings`, `ensure_input`,
`remove_source`, `duplicate_source` — and the six typed creators, five of which
share `createTypedSource` and one of which (`create_audio_input`) does not.
The creators are not an eval route, since they build their own settings maps,
but `create_color_source` with `if_exists: "update"` matched the inbox exactly —
it really is a `color_source_v3`, which is what the Lua creates — and fell
through the kind check into `ensureInput`, the unexported worker that carries no
guard of its own. The guards are in `internal/mcp/bridge_reserved.go`, compared
against `obs.IsBridgeTransport` so a rename cannot leave a stale literal behind. The two
literals live in `internal/obs` rather than in `internal/bridge`, because
`internal/bridge` imports `internal/obs` and the passthrough's guard is in
`internal/obs/dispatch.go`; `internal/bridge` aliases them, so there is still
one spelling of each name. Reads stay open: they expose nothing
`get_obs_status`'s `bridge` field does not already report.

**Two further routes address a source by name, and both are now closed** — each
at its own layer, because neither passes through those helpers.

- `call_obs_request` — Core group, default-enabled, not elicited — would issue
  `SetInputSettings` against any source. ADR-012 gave it a deny-list keyed on
  the request *type*, which cannot see a target, and denying `SetInputSettings`
  wholesale was not an option: it is the request `set_source_settings` wraps,
  and `CreateInput`, `SetInputName` and `CreateSceneItem` reach the same two
  names anyway. So `checkRequestAllowed` now takes the request data too and
  refuses on the target. A `*Name` or `*Uuid` key holding a reserved value is a
  target, at any depth; everything else in the payload is content, so a text
  source whose text is `"agentic-obs-inbox"` is still writable. `Get*` requests
  are left alone, and everything that is not a `Get*` — including a request
  this build has never seen — is treated as a write. Uuids are resolved at call
  time, because `GetInputList` is a read, stays open, and hands back the
  inbox's `inputUuid`. ADR-012 now records this as a second rule alongside the
  deny-list.
- `apply_scene_spec` writes the settings of any source a caller-supplied spec
  names, once `dry_run=false`. A captured spec never names the transport, which
  belongs to no scene; a hand-authored one can. `scenespec.Apply` now refuses
  such a spec whole rather than skipping that one source — a partial apply
  reported as a success leaves a scene neither the spec nor the operator
  describes — and checks placements as well as sources, since an item alone
  would have `ensurePlacements` put the transport into a live scene. Dry runs
  and `diff_scene_spec` stay open; they write nothing, and planning is how a
  caller discovers a stored spec is contaminated. The refusal is also gated on
  the field mask, which is not a nicety: `apply_scene_preset` shares this
  reconciler with `fields: ["enabled"]`, and under that mask `ensureSources`,
  `ensurePlacements`, `reconcileOrder` and `prune` never run, so the only write
  available is `SetSceneItemEnabled` on a placement OBS already holds. It
  provably cannot write, create or place a source, and refusing it would make a
  scene containing the transport un-appliable for nothing. `fieldMask.writesSources`
  names the four aspects that can reach a source — source, placement, settings,
  filters — and they are exactly the four write paths in `apply.go` that take a
  source name rather than a scene item id.

**What this achieves, and what it does not.** Together these close *addressing
the transport by name or by uuid through this server's tools*. Four things that
does not say.

- **It is not a claim about scene item ids.** `DuplicateSceneItem` takes
  `sceneName` and a numeric `sceneItemId`, so `call_obs_request` can still copy
  an existing placement of the transport into another scene without naming it —
  `sourceRefsIn` reads strings, and a number is invisible to it. No settings are
  written, so it is not an eval route; it is a way to litter, and it needs the
  transport to be placed in a scene in the first place, which the reservation
  now refuses everywhere it can be asked for.
- **It does not make the transport unreachable.** A settings write that never
  names it — a future tool, an automation action, a code path added in this repo
  without the guard in mind — reaches it exactly as before. The guards are call
  sites and two chokepoints, not a capability boundary the type system enforces.
- **`call_vendor_request` does not go through any of this.** It bypasses
  `CallRequest` entirely (`internal/mcp/vendor.go` → `CallVendorRequest`) and
  carries a vendor-defined payload, so a key that does not end in `Name` or
  `Uuid` passes through untouched. It is not a route to *this* bridge — the Lua
  script registers no vendor, because the vendor API being closed to Lua is the
  whole reason this bridge exists — but `ass_run_macro` is a default-enabled
  tool reaching an operator-configured execution channel in Advanced Scene
  Switcher, and pretending otherwise would be the same overstatement this
  decision has already had to correct twice.
- **It is not a boundary against whoever holds the obs-websocket password**, who
  can write those settings directly or load a script of their own.

## Alternatives rejected

- **The vendor API.** Measured closed to Lua. It remains the right transport for
  a C++ bridge, and this ADR does not foreclose one.
- **A C++ plugin bridge.** It reaches the vendor API, can own script handles via
  `obs_script_create`, and could expose reload properly. It also needs a build
  toolchain and per-platform distribution, against a Lua script that ships as one
  file and needs neither. Worth revisiting if the bridge grows past what Lua can
  express — not to buy back a reload we no longer need.
- **A fixed command vocabulary over the same transport.** Every new capability
  becomes a bridge edit plus a manual reload. It trades the whole benefit away to
  avoid `loadstring`, and it does not even remove the sandboxing question, since
  the vocabulary would still be driving OBS internals.
- **Polling `GetInputSettings` on a correlation id.** The plan's recorded
  fallback, obsoleted by measurement: `InputSettingsChanged` is push and carries
  the payload.
- **A single source for both directions.** Cheaper by one source, and it
  reintroduces the ADR-010 echo problem in a place where it would be diagnosed
  from scratch.
- **Upstreaming script control to obs-websocket.** The missing primitive is
  script ownership/enumeration in `obs-scripting`; obs-websocket would hit the
  same static-variable wall today. Two PRs across two repos, and the only route
  that reaches scripts the *operator* loaded. Worth filing, not worth waiting for.

## Consequences

### Positive
- The two gaps ADR-012 left are reachable, and so is anything else in-process,
  without a new bridge version each time.
- The iteration loop closes: changing what OBS does no longer requires a human
  to click **Tools → Scripts → ⟳**.
- Both directions are push and measured, so the transport has no unproven half —
  which is the failure this spike existed to prevent.

### Negative
- The bridge executes code an LLM wrote, inside OBS, on the render thread. The
  sandbox and the instruction bound reduce the blast radius of a mistake; neither
  makes it safe to point at an OBS you do not own.
- **The instruction bound is not a hard bound.** It stops an ordinary runaway
  loop and nothing more: a chunk whose loop sits inside its own `pcall` swallows
  the hook's error, the hook re-arms, and OBS stays blocked indefinitely. The Go
  side's timeout lets agentic-obs report the failure; it cannot free the render
  thread. Recovering from that needs a human to close OBS.
- Installing the bridge means two source names are no longer the operator's to
  use. `agentic-obs-inbox` and `agentic-obs-mailbox` are refused by the write
  and structural tools whether or not the bridge is installed, since nothing
  here can tell an operator's identically-named source from the transport. Now
  that `call_obs_request` refuses them too, the reservation extends to anything
  bearing those names — a scene, a filter, a profile — because the guard reads
  the payload's `*Name` keys rather than knowing what kind of thing each one
  addresses.
- The reservation is a list of call sites plus two chokepoints, not a capability
  the type system enforces. A settings write added later without it in mind
  reaches the transport exactly as `set_source_settings` once did. The tests name
  the routes; nothing stops a new one. `createTypedSource` is the proof rather
  than the hypothesis: it was missed on the first pass because the guard was put
  on `handleEnsureInput` and the create tools reach the same worker underneath.
- Chunks are untyped and unvalidated on the way in. A malformed one returns
  `ok=false` rather than a schema error.
- Two sources exist in the operator's collection that belong to no scene. They
  render nowhere, but they are visible in the source list and can be deleted by
  hand, which silently breaks the transport.
- The transport is only as private as obs-websocket's password, and it widens
  what that password is worth.

### Neutral
- `InputSettingsChanged` has no translation in `eventFrom` yet; the bridge needs
  one, and the spike read raw goobs to avoid a production change answering its
  own question.
- Payloads ride JSON source settings, so chunk size is bounded by whatever
  obs-websocket and OBS accept there. Not measured; no workflow is near it.
