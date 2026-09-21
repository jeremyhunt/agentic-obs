# ADR-010: Self-Write Suppression in the Automation Engine (FB-85)

**Status:** Accepted
**Date:** 2026-09-21

## Context

The automation engine reacts to OBS events and writes to OBS. OBS announces
every write as an event. So a rule whose trigger and whose action touch the same
thing feeds itself:

```
operator hides a source
  -> source_visibility_changed
  -> rule "keep it visible" fires
  -> SetSceneItemEnabled(visible=true)
  -> source_visibility_changed          <- our own write
  -> rule "keep it visible" fires       <- again
```

This is not an edge case and it is not new. A rule triggering on
`source_visibility_changed` with a `set_visibility` action is the obvious way to
express "this must stay visible", and it has always looped. Measured before the
fix: **one external event produced 2,300 writes in 300 ms.**

**Cooldown does not solve it.** `cooldown_ms` defaults to 0, so the loop is the
out-of-the-box behaviour rather than something a user opts into. And when
cooldown is set, it throttles genuine events exactly as hard as it throttles
echoes, because it counts rather than identifies: a rule with a 500 ms cooldown
ignores a real operator action that happens to land inside the window. Cooldown
is a rate limit. This is an identity problem.

## Decision

**The engine records each state-setting write before making it, and the
dispatcher drops the first incoming event that matches.**

- A `writeKey` is built from the typed payload: `(kind, scene, item, value)`.
  The **value is part of the key** — without it, hiding something we had just
  shown would read as our own echo and be ignored, so the engine would stop
  answering the operator precisely when they were correcting it.
- Entries are **counted, not flagged**. Two writes of the same thing produce two
  events, and collapsing them would let the second through to re-trigger.
- Entries are **consumed on match**. Leaving one in place would turn the
  suppressor into a filter that swallows every event of that shape for its
  lifetime.
- Entries **expire after 500 ms**. A write can fail and OBS can coalesce two
  changes into one announcement, so an expected echo may never arrive. An entry
  that waited indefinitely would eat a genuine event minutes later, silently —
  worse than a missed suppression, because it is invisible.
- The check sits **ahead of the rule loop**, not inside it. The event is not
  ours "for this rule"; it is ours. A second rule watching the same event would
  otherwise act on what the first one caused.
- The suppressor shares the engine's **clock seam**, so a fake clock moves both.
  Two notions of `now` in one engine is the condition the seam exists to
  prevent.

Cooldown stays what it is: throttling, documented as such, unchanged.

## Consequences

A rule that reacts to what it does now converges instead of looping — one
external event produces exactly one write. An operator flicking a source back
and forth is still answered, which is the failure suppression could have
introduced and the reason for consume-on-match.

**Toggles are only partly covered.** `toggle_visibility` cannot be recorded in
advance because the value it lands on is not known until it has landed;
recording afterwards from the returned state leaves a window in which the event
overtakes the entry. That window is not closed here on purpose: converting a
toggle into a read-then-set would change what the action means, and a rule that
toggles in response to its own toggle is oscillating by construction. That is
the circuit breaker's problem. `set_visibility` is the action for a rule that
wants a state (see FB-55, where `set_visibility` was silently a toggle).

**Rejected: detaching the event callback during a write**, which is how
`rse/obs-scripts` handles the same problem in Lua. It is racy under concurrency,
and in this codebase the event fan-out is shared — detaching would also blind
the thumbnail cache and the MCP resource notifications for the duration of every
write.

**The circuit breaker landed alongside this (FB-86)** and is the backstop for
exactly the gap above. It counts executions per rule over a sliding second and
disables one that crosses twenty, recording `error=oscillation`. It does not
need to understand *why* a rule is running away, which is the point: the hotkey
case is unkeyable by construction, because the engine cannot know what a hotkey
does.

Two things that fix taught us. **A breaker must stop the loop before it records
that it tripped** -- the first version wrote to SQLite first, and a runaway rule
had already saturated the same file with its own execution rows, so every
recovery write came back `SQLITE_BUSY` and the rule stayed enabled. And the
underlying reason was that the database had **no `busy_timeout`**, so any
concurrent writer failed instantly rather than waiting; that was silently losing
execution records under any burst, not just this one.

**Per-rule debounce landed too (FB-87)**, via `debounce_ms` in a rule's
trigger_config -- JSON, so no schema change, which matters because `migrate()`
forbids ALTER TABLE. It is trailing-edge: the rule waits for the events to stop
and runs once on the last one. That is the opposite of cooldown, which runs on
the *first* event and ignores the rest, and so acts on the state before the
burst rather than the state the operator ended up in. The two compose -- the
cooldown is checked when the debounced run fires, so a burst cannot consume a
cooldown without the rule having executed.

This is what grew the clock seam: debounce needs `AfterFunc`, and a debounce
test that waited out real durations would be as slow and as flaky as the
cooldown test the seam was created to fix. The fake clock now fires timers in
deadline order when advanced, and does it after releasing its own lock, because
a debounce callback asks the clock what time it is.

**Numbering note:** the design plan assigned this ADR the number 012, on the
assumption that 009–011 would be taken by earlier increments. 009 was already in
use by the Advanced Scene Switcher tool group, and the earlier increments shipped
without ADRs, so this is 010 — the next free number.
