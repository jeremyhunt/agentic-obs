# Design: Advanced Scene Switcher Skill + Gap Closure

**Date:** 2026-05-29  
**Scope:** agentic-obs `feat/fb-51-ass-tool-group` branch  
**Status:** Approved, ready for implementation

---

## Context

agentic-obs was forked to add Advanced Scene Switcher (ASS) plugin support (4 new tools: `ass_run_macro`, `ass_send_message`, `ass_set_variables`, `ass_set_variable`). The implementation stack is complete (obs layer, MCP handlers, interface, mock, storage, config) but four gaps remain before the feature is shippable:

1. No MCP-layer handler tests
2. No Claude Skill for Advanced Scene Switcher
3. Stale tool count in `CLAUDE.md`
4. `nil` value coercion bug in `coerceASSVariables`

The MCP server is intended as the **primary driver for OBS-related scripting**, with Advanced Scene Switcher as the reaction engine. Primary consumer context: League of Legends streaming (kill-type overlays, auto-clips, camera zoom triggers, milestone alerts) and Twitch-WhisperPy persona automation.

---

## Gap 1: MCP Handler Tests

**File:** `internal/mcp/tools_test.go`  
**Pattern:** Follow existing handler test pattern — create server with `MockOBSClient`, invoke handler via JSON tool call, assert via `mock.GetASSCalls()` and `mock.SetASSError()`.

### Test cases

**`ass_run_macro`**
- Success: name recorded, variables recorded, coercion verified (number → string, bool → string, nil → `""`)
- Empty name rejected before OBS call
- OBS error propagated as tool error

**`ass_send_message`**
- Success: message recorded
- Empty message rejected before OBS call
- OBS error propagated

**`ass_set_variables`**
- Success: all vars recorded in order
- Empty list rejected
- Blank variable name inside list rejected

**`ass_set_variable`**
- Success: delegates to set_variables, single entry recorded
- (Validation covered by set_variables tests via delegation)

---

## Gap 2: Advanced Scene Switcher Skill

**File:** `skills/advanced-scene-switcher/SKILL.md`  
**Model:** Condition/action — MCP is the signal source, Advanced Scene Switcher is the reaction engine.

### Structure

**1. When to Use This Skill**  
Trigger phrases ("run my macro", "set an Advanced Scene Switcher variable", "trigger the scene automation", "send a message to Advanced Scene Switcher") and the general case: whenever you want OBS to react to an event originating outside OBS — game state, audio triggers, Riot API data, bot keywords, screenshot parse results.

**2. The Condition/Action Model**  
One prose diagram:
- *Conditions* live in Advanced Scene Switcher and evaluate on a poll loop (variables, websocket messages, scene state)
- *Actions* are the macros Advanced Scene Switcher fires when conditions match
- *MCP's role* is to push inputs: set variables, send messages, or trigger macros directly when the target is known

This is the loop: external event → MCP tool call → Advanced Scene Switcher state change → condition match → macro fires → OBS effect.

**3. Tool Decision Logic**  
- Know exactly which macro to fire → `ass_run_macro` (set variables atomically via the `variables` field if needed)
- Push state and let Advanced Scene Switcher conditions decide → `ass_set_variable` / `ass_set_variables`
- Fan-out: multiple macros should react to the same event → `ass_send_message`

**4. Patterns with examples**  
Drawn from project use cases:

| Scenario | Tool | Call |
|---|---|---|
| Pentakill overlay | `ass_set_variable` | `{"name": "kill_type", "value": "penta"}` |
| Kill milestone alert | `ass_set_variable` | `{"name": "milestone", "value": "100_kills"}` |
| Auto-clip on keyword | `ass_run_macro` | `{"name": "save_clip"}` |
| Camera zoom on trigger word | `ass_run_macro` | `{"name": "zoom_face_cam"}` |
| WhisperPy persona speaking | `ass_run_macro` | `{"name": "persona_sonic_speaking", "variables": [{"name": "active_persona", "value": "sonic"}]}` |
| Stream-started fan-out | `ass_send_message` | `{"message": "stream_started"}` |

**5. Constraints**  
- Fire-and-forget: no introspection (cannot list macros or variables — caller must know names)
- Macro names and variable names are case-sensitive; must match Advanced Scene Switcher config exactly
- All variable values are coerced to strings on the wire (numbers and bools are accepted by the tools and converted)
- Advanced Scene Switcher rejects unknown macros gracefully (no error return)

**6. Integration with Other Skills**  
- **streaming-assistant** may hand off to this skill when the user wants event-driven OBS behavior rather than one-shot tool calls
- agentic-obs *automation rules* (internal, `create_automation_rule`) and Advanced Scene Switcher macros (OBS plugin) are complementary: automation rules operate on OBS events inside agentic-obs; Advanced Scene Switcher macros operate on conditions inside OBS itself

### Additional required changes
- Add `advanced-scene-switcher` to `EXPECTED_SKILLS` in `scripts/verify-skills.sh` (4 → 5)
- Add entry to `skills/README.md`

---

## Gap 3: Stale Tool Count

**File:** `agentic-obs/CLAUDE.md`  
- Line 9: `81 Tools` → `85 Tools`
- Group list: add `AdvancedSceneSwitcher` (10th group)
- Bump last-updated date to 2026-05-29

`help_content.go` computes the total dynamically from per-group constants — already correct, no change needed.  
`G:/.git/CLAUDE.md` (workspace) does not mention tool counts — no change needed.

---

## Gap 4: nil Coercion Fix

**File:** `internal/mcp/tools_ass.go`, `coerceASSVariables` function  

**Problem:** `fmt.Sprintf("%v", nil)` produces the string `"<nil>"`. If the LLM passes JSON `null` as a variable value, Advanced Scene Switcher receives `"<nil>"` instead of `""`.

**Fix:** Add an explicit nil check before the `fmt.Sprintf` call:

```go
var strVal string
if v.Value == nil {
    strVal = ""
} else {
    strVal = fmt.Sprintf("%v", v.Value)
}
```

Covered by the new `ass_run_macro` test case: nil value → empty string recorded in mock.

---

## Implementation Order

1. Fix `coerceASSVariables` nil bug (smallest, unblocks accurate test assertions)
2. Write MCP handler tests (verify fix is covered)
3. Update `agentic-obs/CLAUDE.md` tool count
4. Write `skills/advanced-scene-switcher/SKILL.md`
5. Update `scripts/verify-skills.sh` and `skills/README.md`
