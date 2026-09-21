# ADR-012: The Extension Channel — Vendor Requests and a Raw-Request Passthrough (FB-77, FB-78, FB-81)

**Status:** Accepted
**Date:** 2026-09-21

## Context

obs-websocket 5.7.4 advertises **151 requests**. `internal/obs` wraps roughly
**65** of them as typed methods, each chosen because some workflow wanted it.
The remaining eighty-odd were never unreachable by design — only unwritten.

That gap is not evenly spread. Reading Bitfocus Companion's obs-studio module —
the most widely deployed OBS control surface there is — showed whole areas
agentic-obs could not touch at all: media playback (`TriggerMediaInputAction`,
`GetMediaInputStatus`, cursor control), profiles and scene collections,
projectors and the properties/filters/interact dialogs, record split and
chapters, stream service settings, generic outputs, `GetStats`,
`TriggerHotkeyByKeySequence`.

Two ways to close it:

1. Write eighty more typed wrappers. Eighty near-identical files, each one
   stale the next time OBS adds a request.
2. Ship an escape hatch.

**Companion, with a far larger user base and a decade of feedback, shipped the
escape hatch** — "Custom Command *(Request data must be valid JSON. See the
obs-websocket protocol documentation)*" — next to its curated actions. It
concluded it cannot enumerate everything. For an LLM client that can read the
protocol reference, a passthrough is *more* useful than it is for a human
clicking buttons, not less.

Separately, third-party OBS plugins expose their own request types through
`CallVendorRequest`, and there is no way to enumerate what vendors exist. The
workspace already depends on one: WhisperPy calls
`AdvancedSceneSwitcherRunMacro` directly, bypassing agentic-obs entirely.

## Decision

**Three channels, none of which requires a new typed method per capability.**

### 1. `call_obs_request` — every obs-websocket request, from a derived registry

The registry is **derived by reflection over goobs, not declared**. goobs
generates, per request, a params type carrying `GetRequestName()` and a response
type embedding `api.ResponseCommon` whose exported `GetRaw()` holds the server's
raw `responseData`. Both shapes are exported, so walking the category subclients
reconstructs the whole request table **using nothing private** and without
forking goobs.

Two method shapes exist and both must be handled: `func(recv, *XxxParams)` where
the request has required fields, and `func(recv, ...*XxxParams)` where every
field is optional. Matching only the variadic form finds 78 of 147 and *looks
like a working registry* — which is exactly why the shape is asserted rather
than assumed.

**A deny-list, deliberately narrow.** The passthrough reaches everything, which
includes the handful of requests whose typed tools exist specifically so a
destructive act is confirmable. Routing around those while looking like a
feature is the one failure mode this tool can introduce that the rest of the
surface cannot:

| Denied | Use instead |
|---|---|
| `RemoveScene`, `RemoveInput`, `RemoveSceneItem`, `RemoveSourceFilter` | the tool that confirms first |
| `SetCurrentSceneCollection`, `RemoveProfile` | nothing — left to the operator |

The last two have no wrapper on purpose: switching collections tears down and
rebuilds every source in OBS, and there is no way to undo it from here.

Calls route through the same middleware as every other tool, so action history
and elicitation still apply, and `list_obs_requests` makes the surface
discoverable rather than something an agent has to guess at.

### 2. `call_vendor_request` — the plugin channel

One tool covers every third-party plugin: obs-browser, Advanced Scene Switcher,
obs-ndi, anything shipped after this was written. Vendors **cannot be
enumerated** over the protocol, so `get_obs_status.extensions` probes known
vendors with a benign request rather than pretending to list them.

Vendor events are the inbound half. The subscription mask gained
`subscriptions.Vendors` (and `Filters`, previously missing), a typed
`VendorEvent` reaches the sink, and automation gained a matching trigger with
`vendor_name` / `event_type` filters plus `ActionTypeCallVendorRequest`. A rule
can now be started by a plugin and can answer one.

### 3. obs-browser as the overlay transport

`obs-browser` registers a websocket vendor in `obs_module_post_load()`: vendor
`"obs-browser"`, request `"emit_event"`, payload
`{ event_name: string, event_data: object }`, delivered to the page as
`window.addEventListener(name, e => e.detail)`. That is a supported, already
shipped agent-to-overlay channel needing no new OBS-side code, and channel 2
reaches it with no new tool.

One property matters and is easy to get wrong: the vendor call is
**broadcast-only**. `DispatchJSEvent(…, nullptr)` sends to *every* browser
source, so pages must filter on the event name. A *targeted* channel exists —
each browser source's own proc handler registers
`javascript_event(eventName, jsonString)` — but it is reachable only from inside
OBS, which is the Lua bridge's job, not this one's.

### 4. The gap is measured every run, not remembered

`TestLiveRequestCoverageIsMeasured` reads the server's own
`GetVersion.availableRequests` and compares it against what this build can
issue. No hand-maintained table, so it cannot drift: a request added by a future
OBS shows up as uncovered the first time the live suite runs.

It reads that list **through the passthrough itself**, deliberately — if
`CallRequest` is broken, the test cannot report a false clean bill of health.

Four requests are expected missing and are named rather than tolerated
silently: `Get/SetSourcePrivateSettings` and `Get/SetSceneItemPrivateSettings`.
obs-websocket offers them; goobs does not generate them. They need a goobs
contribution or the Lua bridge. If that list ever changes, the test says so.

## Alternatives rejected

- **Writing a typed wrapper for each of the remaining requests.** Eighty files
  that duplicate goobs' own generated surface and go stale on the next OBS
  release. Promote one to an ergonomic wrapper only when a real workflow uses it
  twice.
- **Forking or patching goobs to expose a dispatch table.** Unnecessary: both
  shapes the registry needs are already exported. A fork would be a permanent
  maintenance cost for something reflection reaches today.
- **A new `Vendor` tool group.** A tool group is a dozen edit sites across
  config, metadata, help and docs. Core and Sources absorb these three tools.
- **`emit_browser_event` as its own tool.** No overlay page in the workspace
  listens for obs-browser DOM events today, so it would ship with zero
  consumers. The generic vendor call plus a documented recipe covers it, and
  obs-browser already pushes `obsSceneChanged`, `obsSourceVisibleChanged` and
  every streaming/recording state change into pages with no involvement from
  this server at all.

## Consequences

### Positive
- The whole Companion gap table is reachable without a single new typed method,
  and so is every request a future OBS adds.
- Third-party plugins are reachable in both directions, including as automation
  triggers.
- "Drive every OBS control surface" became a number a test checks rather than a
  sentence in a design document.

### Negative
- A raw request is untyped on the way in and on the way out: the agent is
  responsible for the payload shape, and a mistake surfaces as an obs-websocket
  error rather than a schema failure.
- The deny-list is a policy, and policies drift from the tools they defend. Each
  entry names its wrapper so the connection is visible in the error the caller
  gets.
- Probing for vendors is a guess dressed as a list. It reports what it found,
  never what exists.

### Neutral
- `list_obs_requests` makes the raw surface self-describing, so an agent does
  not need the protocol reference open to use it — though reading the reference
  is still the right move for payload shapes.
