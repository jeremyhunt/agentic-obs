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
`require`, `dofile`, `loadfile`, `debug` — or LuaJIT's own `ffi`, `jit` and
`bit`. `ffi` matters more than the rest of that list put together: `ffi.cdef`
plus `ffi.load` reach any DLL on the machine, a bigger hole than `os.execute`.
`obs-scripting-lua.c` calls `luaopen_ffi` right after `luaL_openlibs`, so the
table is live in this process — as `package.loaded.ffi` rather than a global —
which is why withholding `package` and `require` is what actually closes the
route, and why the sandboxed environment must never grow either of them back.

It is also bounded by instruction count via `debug.sethook` — the same call
FB-86 made for runaway automation rules — which stops an *ordinary* runaway
loop. **That claim shipped false.** OBS embeds LuaJIT, not vanilla Lua 5.1:
`obslua` ships as `lua51.dll` because LuaJIT is Lua-5.1-ABI-compatible, so the
filename, the syntax and the manual all say "5.1," and the interpreter that
actually runs a hot loop is a different program than the one they describe. A
LuaJIT count hook is checked by the bytecode interpreter only — once a loop
compiles into a trace, at roughly 56 iterations, the trace is native code that
never looks at the hook again. `TestLiveBridgeBoundsRunawayChunks`
(`internal/bridge/live_test.go`) caught this against a real OBS: `while true do
end` pegged the video thread at 7.31 CPU-seconds in 5 wall-seconds and needed a
force-stop, because the hook fired zero times.

`run()` now calls `jit.off(chunk, true)` before executing anything, which keeps
the chunk interpreted so the hook stays live. The `true` matters: it recurses
into the chunk's nested function prototypes, which is what covers a loop the
chunk hides inside a function of its own — checked directly against OBS's
`lua51.dll` rather than assumed, a loop nested that way kept compiling silently
under a one-argument `jit.off(chunk)` and only started tripping the hook once a
second argument was supplied at all. Since the sandbox withholds `load` and
`loadstring`, those nested prototypes are all the Lua a chunk can reach;
everything else it calls is a C function, which LuaJIT cannot trace into
regardless. The cost is real — interpreted arithmetic measured 2-3x slower than
compiled on this machine — and it is paid on every chunk; the guard is `if jit
then`, so the script still loads under a vanilla Lua 5.1 that has no `jit`
table.

**It shipped false a second time, after that fix.** With `jit.off` applied the
hook did fire against a real OBS — confirmed in OBS's own log, about 500 times
a second — and the loop still would not stop: `while true do end` burned 9.55
CPU-seconds in 4 wall-seconds and needed a second force-stop. The 255 log
lines it produced were missing this bridge's own `[agentic-obs]` prefix, which
is what pointed at the real cause: `obs-scripting-lua.c`'s
`add_hook_functions()` replaces the *global* `error` and `print` with its own
C functions before a script's file is ever read, and neither does what its
name promises — `hook_error` reads the message, logs it through OBS's own
script-error path, and returns normally, exactly like `print`. No `lua_error`,
no unwind. Nothing in the bridge script is `setfenv`'d away from that global
scope except the sandboxed chunk itself, so the budget hook's own
`error("the chunk exceeded its instruction budget", 2)` call was never raising
anything — it was logging, under OBS's prefix instead of the bridge's, and
letting the interpreter carry on to the next instruction, which is why the
hook could fire forever without ever bounding anything. `run()` now signals
with `assert(false, msg)` instead. OBS never touches `assert`, and `assert`
raises through the C API directly (`lua_error`, or LuaJIT's own
`lj_err_callermsg`) rather than through the Lua-callable global `error`, so it
works regardless of what that global currently is. Verified directly against
OBS's `lua51.dll` with `error` and `print` replaced exactly as
`add_hook_functions()` replaces them (a throwaway ctypes harness, not
committed): the pre-fix hook does not stop `while true do end` at all inside
an 8-second bound; the `assert`-based one stops it in under a millisecond,
repeatably. The same replacement means a **chunk's own** `error()` call —
which the sandbox deliberately exposes — has the identical problem and is
*not* fixed by this change: a chunk that calls `error("something failed")` to
signal its own failure logs to OBS's Script Log and keeps running past that
line rather than failing the call. That is a separate, pre-existing defect
from the instruction budget, newly surfaced by this same investigation and
left unresolved here — see the comment beside `error = error` in
`make_env()`.

Even fixed twice over, it is not a hard bound, and an earlier draft of this
ADR said it was. A chunk that wraps its own loop in its own `pcall` catches
the error the hook raises like any other error; on its own that only costs
one pass through the loop, since `pcall` ends the protected call rather than
resuming what it caught, but a chunk that re-enters `pcall` from an
unprotected outer loop (`while true do pcall(risky) end`) gets a fresh
protected call every pass, and the hook has no way to reach past that — it can
only raise into the nearest `pcall`, and that one belongs to the chunk. A
coroutine does not change this: wrapping the whole chunk in one only moves the
*outermost* catch, and does nothing to whatever `pcall` the chunk installs for
itself on the inside, which is the one actually catching each firing — checked
directly against the `assert`-based hook above, both on the main thread and
inside a `coroutine.resume`, rather than assumed. Nor does the budget cover
one long C call — the hook counts VM instructions, so time inside
`string.rep`, a pathological `string.find` pattern, or a blocking `obslua`
call is not counted at all. The real backstop in any of these cases is the Go
side's 2 s transport timeout, which lets agentic-obs report failure and carry
on — it does not unwedge OBS. A bridge that was not itself Lua could close
this; this one cannot.

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

The two names are therefore refused by **every tool through which a caller names
a source or a scene and something is written** — nineteen of them as this is
written, and the number is deliberately not the point. Three successive
revisions of this paragraph named a count and were wrong within a round:
`create_audio_input` was missed once, then `create_source_filter`,
`remove_source_filter`, `toggle_source_filter`, `set_source_filter_settings`,
`toggle_input_mute`, `set_input_volume`, `press_source_properties_button`,
`create_scene` and `remove_scene` were missed the round after. Every one of
those was already refused through `call_obs_request` and allowed through its own
typed tool, so the passthrough and the tool surface disagreed about the same OBS
request.

**So the list is not maintained by hand any more.**
`internal/mcp/bridge_surface_test.go` drives every tool that lets a caller name
a source or a scene and fails for any that neither refuses nor carries a written
reason it need not — nineteen refusals and twenty-two reasoned exemptions, each
checked by calling the tool rather than by reading its schema. Adding a twentieth
tool without a guard fails that test. It is the tool-surface counterpart of
ADR-012's `TestEverySourceAddressingFieldMatchesTheKeyRule`, and it exists
because a list of guarded call sites is not a property, it is a thing someone
has to keep remembering.

Two of those refusals are worth their own sentence. The typed creators are not
an eval route, since they build their own settings maps, but
`create_color_source` with `if_exists: "update"` matched the inbox exactly — it
really is a `color_source_v3`, which is what the Lua creates — and fell through
the kind check into `ensureInput`, the unexported worker that carries no guard.
And `create_scene`/`remove_scene` are refused because OBS keeps scenes and
sources in **one** namespace: a scene called `agentic-obs-inbox` collides with
the transport rather than sitting beside it.

The filter, mute, volume and properties-button refusals do not stop an
escalation — none of them reaches the inbox's settings, and only a settings
write raises the `update` signal the Lua listens for. They are refused so that
the reservation is *one rule* — agentic-obs does not write the transport —
rather than a per-tool judgement about which writes happen to be harmless. That
judgement is precisely what was got wrong twice.

The guards are in `internal/mcp/bridge_reserved.go`, compared
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
- **OBS's Lua is LuaJIT, not vanilla Lua 5.1, and that silently defeated the
  instruction bound until a live test caught it.** `obslua` ships as
  `lua51.dll` because LuaJIT is Lua-5.1-ABI-compatible; the filename, the
  syntax and the manual all say "5.1," and nothing about reading the Lua 5.1
  manual would have surfaced the difference. LuaJIT checks a `debug.sethook`
  count hook in its bytecode interpreter only — once a loop compiles into a
  trace, at roughly 56 iterations, the hook is never consulted again. Before
  `run()` called `jit.off(chunk, true)`, `while true do end` pegged OBS's video
  thread at 7.31 CPU-seconds in 5 wall-seconds and needed a force-stop, because
  the hook fired zero times. The fix keeps every chunk interpreted, at a
  measured 2-3x arithmetic cost, and depends on the sandbox continuing to
  withhold `load`/`loadstring`: those are what make a chunk's own nested
  function prototypes — which `jit.off(chunk, true)`'s recursive argument
  covers — the only Lua code the chunk can ever hand the interpreter.
- **The bound was defeated a second, independent way even after that fix, and
  a second live test caught it too.** `obs-scripting-lua.c` replaces the
  *global* `error` and `print` before a script ever loads; its replacement
  logs and returns rather than raising, so the budget hook's own
  `error(msg, 2)` call was never actually an error — confirmed on a real OBS
  as ~500 hook fires a second with none of the bridge's own `[agentic-obs]`
  log prefix, while `while true do end` kept running until it was
  force-stopped a second time. `run()` now signals with `assert(false, msg)`,
  which OBS does not touch and which raises through the C API directly. The
  same replacement means a chunk's own `error()` call — which the sandbox
  deliberately exposes — still only logs rather than failing the call; that
  is a separate, pre-existing defect this fix does not resolve.
- **Even fixed twice over, the instruction bound is not a hard bound.** It
  stops an ordinary runaway loop and nothing more: a chunk that re-enters its
  own `pcall` from an unprotected outer loop gets a fresh protected call every
  pass, and the hook can only raise into the nearest one, which is the
  chunk's own — a coroutine does not change this, since it moves only the
  outermost catch. One long C call (`string.rep`, a pathological
  `string.find` pattern, a blocking `obslua` call) is not counted by the hook
  at all either, since it counts VM instructions, not wall time. The Go
  side's timeout lets agentic-obs report the failure; it cannot free the
  render thread. Recovering from either needs a human to close OBS.
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
