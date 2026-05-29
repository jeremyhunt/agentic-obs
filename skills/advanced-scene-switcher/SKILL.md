---
name: advanced-scene-switcher
description: Activate this skill when users need to trigger Advanced Scene Switcher macros, set plugin variables, or send websocket messages to OBS. Triggers include requests like "run my macro", "set the scene switcher variable", "trigger the gaming overlay", "send a message to Advanced Scene Switcher", or when another skill hands off because the user wants event-driven OBS behavior rather than a one-shot tool call. This skill treats the MCP server as the signal source and Advanced Scene Switcher as the reaction engine.
---

# Advanced Scene Switcher

Expert guidance for driving Advanced Scene Switcher (OBS plugin) through the MCP server. The MCP server pushes state; Advanced Scene Switcher reacts by evaluating conditions and firing macros.

## When to Use This Skill

Activate the **advanced-scene-switcher** skill when users request:

- **Macro triggering**
  - "Run my pentakill macro"
  - "Trigger the scene effect"
  - "Fire the persona_sonic_speaking macro"
  - "Execute my zoom macro"

- **Variable-driven automation**
  - "Set the kill_type variable to penta"
  - "Update the active_persona variable"
  - "Push the current game state to Advanced Scene Switcher"

- **Websocket message fan-out**
  - "Send stream_started to Advanced Scene Switcher"
  - "Broadcast the scene_change event"
  - "Trigger all macros listening for this message"

- **Event-driven OBS behavior**
  - Any request where an external event (game state, bot audio, Riot API, screenshot parse) should cause OBS to react automatically
  - Handoffs from **streaming-assistant** when the user wants conditional/event-driven logic rather than one-shot tool calls

## The Condition/Action Model

Advanced Scene Switcher is a **reaction engine**. It does not receive direct commands — it evaluates *conditions* on a continuous poll loop and fires *macros* (sequences of actions) when conditions match.

The MCP server's role is to **push inputs** into that system:

```
External event
    │
    ▼
MCP tool call  ──────────────────────────────────┐
    │                                             │
    ├─ ass_set_variable / ass_set_variables       │  Push state;
    │   → Advanced Scene Switcher variable        │  let conditions
    │     updated; conditions re-evaluate         │  decide what fires
    │                                             │
    ├─ ass_run_macro                              │  Direct trigger;
    │   → Named macro fires immediately           │  you know exactly
    │     (conditions bypassed)                   │  which one
    │                                             │
    └─ ass_send_message                           │  Fan-out;
        → All macros whose "Websocket message     │  multiple macros
          received" condition matches will fire   │  react
```

Think of it this way:
- **Variables** are shared state. You set them; Advanced Scene Switcher's conditions read them on the next evaluation pass.
- **Macros** are named action sequences. You can trigger them directly (bypassing conditions) or let conditions do it.
- **Messages** are broadcast events. Any macro configured with "Websocket message received = X" fires when you send X.

## Tool Decision Logic

**Use `ass_run_macro` when:**
- You know the exact macro to fire
- You want to pass variables to the macro atomically (set variables *and* trigger in one call)
- You want to bypass conditions and fire immediately

**Use `ass_set_variable` or `ass_set_variables` when:**
- You want to push state and let Advanced Scene Switcher's existing conditions decide what fires
- Multiple macros might care about the same variable
- You're updating several pieces of state at once (use `ass_set_variables` for batch updates)

**Use `ass_send_message` when:**
- Multiple macros should react to the same event
- You don't know (or don't want to hardcode) which specific macros should fire
- You want an event-driven fan-out pattern

## Available Tools

- `ass_run_macro` — Trigger a named macro directly. Optional `variables` field sets variables atomically before the macro runs.
- `ass_send_message` — Broadcast a websocket message string. All macros with a matching "Websocket message received" condition will fire.
- `ass_set_variables` — Bulk-update plugin variables without firing any macro. Let conditions react on their next evaluation pass.
- `ass_set_variable` — Set a single variable by name. Ergonomic shortcut for `ass_set_variables` with one entry.

## Patterns with Examples

### Pattern 1: Direct macro trigger (known target)

```
User: "Trigger the pentakill overlay"

1. ass_run_macro:
   - name: "pentakill_overlay"
   Confirm: "Triggered macro 'pentakill_overlay'"
```

### Pattern 2: Variable push + atomic macro trigger

```
User: "The persona Sonic just started speaking"

1. ass_run_macro:
   - name: "persona_speaking"
   - variables: [
       {"name": "active_persona", "value": "sonic"},
       {"name": "is_speaking",    "value": "true"}
     ]
   Confirm: "Triggered macro 'persona_speaking' with active_persona=sonic"
```

### Pattern 3: State push, let conditions react

```
User: "Set the kill type to penta and update the kill count"

1. ass_set_variables:
   - variables: [
       {"name": "kill_type",  "value": "penta"},
       {"name": "kill_count", "value": "5"}
     ]
   Confirm: "Set 2 Advanced Scene Switcher variable(s): kill_type, kill_count"
   Note: Any macro whose condition checks kill_type == "penta" will fire on the next evaluation pass.
```

### Pattern 4: Broadcast fan-out

```
User: "Signal that the stream has started"

1. ass_send_message:
   - message: "stream_started"
   Confirm: "Sent websocket message 'stream_started' — all macros listening for this message will react"
```

### Pattern 5: Milestone alert

```
User: "I just hit 100 kills on Aatrox"

1. ass_set_variables:
   - variables: [
       {"name": "milestone",       "value": "100_kills"},
       {"name": "milestone_champ", "value": "Aatrox"}
     ]
   Or, if there's a dedicated macro:
2. ass_run_macro:
   - name: "milestone_alert"
   - variables: [{"name": "milestone_champ", "value": "Aatrox"}]
```

### Pattern 6: Camera zoom on spoken trigger

```
User: "Zoom in on the face cam"

1. ass_run_macro:
   - name: "zoom_face_cam"
   Confirm: "Triggered macro 'zoom_face_cam'"
```

## Constraints

- **Fire-and-forget:** The tools return immediately after sending the request. There is no confirmation that a macro ran or a condition matched — Advanced Scene Switcher does not return execution results over obs-websocket.
- **No introspection:** You cannot list macros, list variables, or read variable values. The caller must know the names they want to use.
- **Case-sensitive names:** Macro names and variable names must exactly match what's configured in Advanced Scene Switcher. `Pentakill_Overlay` ≠ `pentakill_overlay`.
- **String values:** All variable values are stored as strings inside Advanced Scene Switcher. The tools accept numbers and booleans and coerce them (`42` → `"42"`, `true` → `"true"`, `null`/`nil` → `""`).
- **Unknown macros:** Advanced Scene Switcher silently ignores `ass_run_macro` calls for macro names it does not recognize. No error is returned. Double-check the name if nothing happens.

## Integration with Other Skills

The **advanced-scene-switcher** skill collaborates with:

- **streaming-assistant**: Hands off here when the user wants event-driven or conditional OBS behavior. streaming-assistant handles one-shot tool calls and session management; Advanced Scene Switcher handles reactive automation.
- agentic-obs **automation rules** (`create_automation_rule`) are a complementary but different layer: automation rules operate on OBS events *inside agentic-obs*; Advanced Scene Switcher macros operate on conditions *inside OBS itself*. Both can be active simultaneously.

**Handoff pattern:**
```
User: "Auto-show the pentakill overlay whenever I get a multi-kill"

streaming-assistant: "This is conditional/reactive behavior — Advanced Scene
Switcher is the right tool. Let me hand off to the advanced-scene-switcher
skill to set up the variable push side of this."

[Handoff: use ass_set_variable to push kill_type whenever a kill event arrives;
 the Advanced Scene Switcher macro that shows the overlay must already be
 configured in the plugin to react to that variable]
```

## Common Pitfalls

1. **Wrong macro name casing**: Advanced Scene Switcher names are case-sensitive. If a macro doesn't fire, verify the exact name in the Advanced Scene Switcher UI.
2. **Expecting instant condition evaluation**: After `ass_set_variables`, conditions evaluate on Advanced Scene Switcher's poll interval (typically 300ms). The effect is not instant.
3. **Using `ass_run_macro` when you mean `ass_set_variable`**: Direct macro triggers bypass conditions. If the macro has conditions that gate its actions, use variables instead and let conditions decide.
4. **Batch updates one-at-a-time**: When updating multiple variables together, use `ass_set_variables` (bulk) rather than multiple `ass_set_variable` calls to ensure they're set atomically.
