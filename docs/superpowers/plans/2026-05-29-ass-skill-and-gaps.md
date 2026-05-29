# Advanced Scene Switcher Skill + Gap Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close four gaps in the `feat/fb-51-ass-tool-group` branch: fix a nil coercion bug, add MCP handler tests, update the stale tool count in `CLAUDE.md`, and write the Advanced Scene Switcher Claude Skill.

**Architecture:** The ASS tools live in `internal/mcp/tools_ass.go` (handlers) and `internal/obs/commands_ass.go` (OBS client). Tests follow the existing pattern in `tools_test.go`: create a server with `testutil.MockOBSClient`, call the handler directly, assert on mock state via `GetASSCalls()`. The skill file follows the condition/action mental model: MCP pushes state, Advanced Scene Switcher reacts.

**Tech Stack:** Go 1.25+, testify (assert/require), `internal/mcp/testutil.MockOBSClient`, `obs.ASSVariable`

**Spec:** `docs/superpowers/specs/2026-05-29-ass-skill-and-gaps-design.md`

---

## File Map

| Action | File |
|--------|------|
| Modify | `internal/mcp/tools_ass.go` — nil coercion fix in `coerceASSVariables` |
| Modify | `internal/mcp/tools_test.go` — add ASS handler test functions |
| Modify | `CLAUDE.md` — 81 → 85 tools, add AdvancedSceneSwitcher group |
| Create | `skills/advanced-scene-switcher/SKILL.md` — new skill |
| Modify | `scripts/verify-skills.sh` — add `advanced-scene-switcher` to `EXPECTED_SKILLS` |
| Modify | `skills/README.md` — add Advanced Scene Switcher entry |

---

## Task 1: Fix nil coercion in `coerceASSVariables`

**Files:**
- Modify: `internal/mcp/tools_ass.go:55-66`

The current code uses `fmt.Sprintf("%v", v.Value)` where `v.Value` is `any`. When the LLM passes JSON `null`, Go receives `nil`, and `fmt.Sprintf("%v", nil)` produces the string `"<nil>"` rather than `""`.

- [ ] **Step 1: Open `internal/mcp/tools_ass.go` and replace the body of `coerceASSVariables`**

Replace the loop body (lines 57-64) so it reads:

```go
func coerceASSVariables(in []ASSVariableInput) ([]obs.ASSVariable, error) {
	out := make([]obs.ASSVariable, 0, len(in))
	for i, v := range in {
		if v.Name == "" {
			return nil, fmt.Errorf("variables[%d].name must not be empty", i)
		}
		var strVal string
		if v.Value == nil {
			strVal = ""
		} else {
			strVal = fmt.Sprintf("%v", v.Value)
		}
		out = append(out, obs.ASSVariable{
			Name:  v.Name,
			Value: strVal,
		})
	}
	return out, nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /g/.git/agentic-obs && go build ./internal/mcp/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/tools_ass.go
git commit -m "fix: coerce nil ASS variable values to empty string instead of \"<nil>\""
```

---

## Task 2: MCP handler tests for Advanced Scene Switcher tools

**Files:**
- Modify: `internal/mcp/tools_test.go` — append at the end of the file

The test helper `testServer(t)` already exists (line 16). The mock exposes:
- `mock.GetASSCalls()` → `(messages []string, lastMacro string, lastVarCount int, varsSet []obs.ASSVariable)`
- `mock.SetASSError(method string, err error)` where method is `"run_macro"`, `"send_message"`, or `"set_variables"`

- [ ] **Step 1: Write the failing tests — append to `internal/mcp/tools_test.go`**

```go
// ── Advanced Scene Switcher handler tests ────────────────────────────────────

func TestHandleASSRunMacro(t *testing.T) {
	t.Run("triggers macro by name", func(t *testing.T) {
		server, mock := testServer(t)

		input := ASSRunMacroInput{Name: "pentakill_overlay"}
		_, result, err := server.handleASSRunMacro(context.Background(), nil, input)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		_, lastMacro, varCount, _ := mock.GetASSCalls()
		assert.Equal(t, "pentakill_overlay", lastMacro)
		assert.Equal(t, 0, varCount)
	})

	t.Run("passes variables atomically with macro", func(t *testing.T) {
		server, mock := testServer(t)

		input := ASSRunMacroInput{
			Name: "persona_speaking",
			Variables: []ASSVariableInput{
				{Name: "active_persona", Value: "sonic"},
				{Name: "kill_count", Value: 42},
				{Name: "is_live", Value: true},
				{Name: "cleared", Value: nil},
			},
		}
		_, _, err := server.handleASSRunMacro(context.Background(), nil, input)

		require.NoError(t, err)
		_, lastMacro, varCount, varsSet := mock.GetASSCalls()
		assert.Equal(t, "persona_speaking", lastMacro)
		assert.Equal(t, 4, varCount)
		assert.Equal(t, "sonic", varsSet[0].Value)
		assert.Equal(t, "42", varsSet[1].Value)   // int coerced to string
		assert.Equal(t, "true", varsSet[2].Value) // bool coerced to string
		assert.Equal(t, "", varsSet[3].Value)     // nil coerced to ""
	})

	t.Run("rejects empty macro name", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleASSRunMacro(context.Background(), nil, ASSRunMacroInput{Name: ""})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name must not be empty")
	})

	t.Run("propagates OBS error", func(t *testing.T) {
		server, mock := testServer(t)
		mock.SetASSError("run_macro", errors.New("macro not found"))

		_, _, err := server.handleASSRunMacro(context.Background(), nil, ASSRunMacroInput{Name: "missing"})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "macro not found")
	})
}

func TestHandleASSSendMessage(t *testing.T) {
	t.Run("sends message successfully", func(t *testing.T) {
		server, mock := testServer(t)

		input := ASSSendMessageInput{Message: "stream_started"}
		_, result, err := server.handleASSSendMessage(context.Background(), nil, input)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		messages, _, _, _ := mock.GetASSCalls()
		require.Len(t, messages, 1)
		assert.Equal(t, "stream_started", messages[0])
	})

	t.Run("rejects empty message", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleASSSendMessage(context.Background(), nil, ASSSendMessageInput{Message: ""})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "message must not be empty")
	})

	t.Run("propagates OBS error", func(t *testing.T) {
		server, mock := testServer(t)
		mock.SetASSError("send_message", errors.New("plugin not loaded"))

		_, _, err := server.handleASSSendMessage(context.Background(), nil, ASSSendMessageInput{Message: "ping"})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "plugin not loaded")
	})
}

func TestHandleASSSetVariables(t *testing.T) {
	t.Run("sets all variables in order", func(t *testing.T) {
		server, mock := testServer(t)

		input := ASSSetVariablesInput{
			Variables: []ASSVariableInput{
				{Name: "kill_type", Value: "penta"},
				{Name: "milestone", Value: "100_kills"},
			},
		}
		_, result, err := server.handleASSSetVariables(context.Background(), nil, input)

		require.NoError(t, err)
		assert.NotNil(t, result)

		_, _, _, varsSet := mock.GetASSCalls()
		require.Len(t, varsSet, 2)
		assert.Equal(t, "kill_type", varsSet[0].Name)
		assert.Equal(t, "penta", varsSet[0].Value)
		assert.Equal(t, "milestone", varsSet[1].Name)
		assert.Equal(t, "100_kills", varsSet[1].Value)
	})

	t.Run("rejects empty variables list", func(t *testing.T) {
		server, _ := testServer(t)

		_, _, err := server.handleASSSetVariables(context.Background(), nil, ASSSetVariablesInput{Variables: nil})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not be empty")
	})

	t.Run("rejects blank variable name", func(t *testing.T) {
		server, _ := testServer(t)

		input := ASSSetVariablesInput{
			Variables: []ASSVariableInput{{Name: "", Value: "oops"}},
		}
		_, _, err := server.handleASSSetVariables(context.Background(), nil, input)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name must not be empty")
	})

	t.Run("propagates OBS error", func(t *testing.T) {
		server, mock := testServer(t)
		mock.SetASSError("set_variables", errors.New("plugin not loaded"))

		input := ASSSetVariablesInput{
			Variables: []ASSVariableInput{{Name: "x", Value: "1"}},
		}
		_, _, err := server.handleASSSetVariables(context.Background(), nil, input)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "plugin not loaded")
	})
}

func TestHandleASSSetVariable(t *testing.T) {
	t.Run("delegates to set_variables with single entry", func(t *testing.T) {
		server, mock := testServer(t)

		input := ASSSetVariableInput{Name: "active_persona", Value: "sonic"}
		_, result, err := server.handleASSSetVariable(context.Background(), nil, input)

		require.NoError(t, err)
		assert.NotNil(t, result)

		_, _, _, varsSet := mock.GetASSCalls()
		require.Len(t, varsSet, 1)
		assert.Equal(t, "active_persona", varsSet[0].Name)
		assert.Equal(t, "sonic", varsSet[0].Value)
	})
}
```

- [ ] **Step 2: Add `"errors"` to the import block at the top of `tools_test.go`**

The existing imports are (lines 1-13):

```go
import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/ironystock/agentic-obs/internal/storage"
)
```

- [ ] **Step 3: Run the new tests to confirm they fail (nil bug not fixed yet is already done — tests should pass after Task 1)**

```bash
cd /g/.git/agentic-obs && go test ./internal/mcp/... -run "TestHandleASS" -v
```

Expected: all subtests PASS (Task 1 already fixed the nil bug).

- [ ] **Step 4: Run the full test suite to confirm no regressions**

```bash
go test ./... 
```

Expected: all tests pass, no failures.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_test.go
git commit -m "test: add MCP handler tests for Advanced Scene Switcher tools"
```

---

## Task 3: Update stale tool count in `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md:9` and the last line

- [ ] **Step 1: Edit `CLAUDE.md` line 9**

Change:
```
**Current Status:** 81 Tools | 4 Resources | 14 Prompts | 4 Skills
```
To:
```
**Current Status:** 85 Tools | 4 Resources | 14 Prompts | 5 Skills
```

(Skills count moves from 4 → 5 after Task 4 adds the Advanced Scene Switcher skill.)

- [ ] **Step 2: Update the group list in the Key Technologies table**

Find the row:
```
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` | 1.1.0 |
```

That table is not the right place. Instead find the project structure comment that lists tool groups. In `CLAUDE.md` the groups are referenced in the MCP Capabilities Summary section. Update:

```
| Automation | 9 | `list_automation_rules`, `create_automation_rule`, `trigger_automation_rule` |
```

Add a new row after it:
```
| AdvancedSceneSwitcher | 4 | `ass_run_macro`, `ass_send_message`, `ass_set_variables`, `ass_set_variable` |
```

- [ ] **Step 3: Bump the last-updated footer**

Change:
```
**Last Updated:** 2025-12-19 | **Go:** 1.25.5 | **MCP SDK:** 1.1.0 | **goobs:** 1.5.6
```
To:
```
**Last Updated:** 2026-05-29 | **Go:** 1.25.5 | **MCP SDK:** 1.1.0 | **goobs:** 1.5.6
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update tool count to 85 and add AdvancedSceneSwitcher group to CLAUDE.md"
```

---

## Task 4: Write the Advanced Scene Switcher skill

**Files:**
- Create: `skills/advanced-scene-switcher/SKILL.md`

- [ ] **Step 1: Create the directory and write `SKILL.md`**

```bash
mkdir -p /g/.git/agentic-obs/skills/advanced-scene-switcher
```

Write `skills/advanced-scene-switcher/SKILL.md` with the following content:

```markdown
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
```

- [ ] **Step 2: Verify frontmatter**

The file must start with `---`, contain `name: advanced-scene-switcher` and `description:`, and close with `---`. Open the file and visually confirm.

- [ ] **Step 3: Commit**

```bash
git add skills/advanced-scene-switcher/SKILL.md
git commit -m "feat: add Advanced Scene Switcher Claude skill (condition/action model)"
```

---

## Task 5: Update `verify-skills.sh` and `skills/README.md`

**Files:**
- Modify: `scripts/verify-skills.sh:17-22`
- Modify: `skills/README.md`

- [ ] **Step 1: Add `advanced-scene-switcher` to `EXPECTED_SKILLS` in `verify-skills.sh`**

Change:
```bash
EXPECTED_SKILLS=(
    "streaming-assistant"
    "scene-designer"
    "audio-engineer"
    "preset-manager"
)
```
To:
```bash
EXPECTED_SKILLS=(
    "streaming-assistant"
    "scene-designer"
    "audio-engineer"
    "preset-manager"
    "advanced-scene-switcher"
)
```

- [ ] **Step 2: Add skill entry to `skills/README.md`**

After the `### 5. Studio Mode Operator` section (after line 159, before `## Skill Selection Guide`), insert:

```markdown
### 6. Advanced Scene Switcher (`advanced-scene-switcher`)

**When to use**: Triggering Advanced Scene Switcher macros, pushing plugin variables, broadcasting websocket messages to OBS automation

**Key capabilities**:
- Direct macro triggering with optional atomic variable pre-set
- Plugin variable updates (single or bulk) to drive condition-based automation
- Websocket message broadcast for fan-out to multiple macros
- Condition/action mental model: MCP pushes state, Advanced Scene Switcher reacts
- League of Legends streaming examples: kill-type overlays, milestone alerts, auto-clips
- WhisperPy persona automation: active_persona variable, speaking-state macros

**Tools used**: `ass_run_macro`, `ass_send_message`, `ass_set_variables`, `ass_set_variable`

**Best for**: Users who have Advanced Scene Switcher macros configured in OBS and want the MCP server to drive them from external events (game state, bot audio, Riot API data, screenshot parse results).

---
```

- [ ] **Step 3: Add `advanced-scene-switcher` to the Skill Selection Guide table in `skills/README.md`**

Locate the table (around line 163) and add two rows:

```markdown
| "Run my pentakill macro" | `advanced-scene-switcher` |
| "Set the kill_type variable to penta" | `advanced-scene-switcher` |
```

- [ ] **Step 4: Run the validation script from the repo root**

```bash
cd /g/.git/agentic-obs && sh scripts/verify-skills.sh
```

Expected output:
```
==========================================
Claude Skills Validation
==========================================

Expected skills: 5
  - streaming-assistant
  - scene-designer
  - audio-engineer
  - preset-manager
  - advanced-scene-switcher
...
All skill validation checks passed!
```

- [ ] **Step 5: Run the full test suite one final time**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add scripts/verify-skills.sh skills/README.md
git commit -m "chore: register advanced-scene-switcher skill in verify script and README"
```
