package automation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ironystock/agentic-obs/internal/storage"
)

// hotkeyLoopClient makes a hotkey change the scene, which is a loop the
// suppressor cannot key.
//
// That is the point of choosing it. Suppression works by recording what a write
// will make OBS announce, and the engine cannot know what a hotkey does -- it is
// a name handed to OBS, and whatever it triggers comes back as an event with no
// entry waiting for it. Testing the breaker against a loop suppression already
// closes would prove nothing about the breaker.
type hotkeyLoopClient struct {
	*MockOBSClient

	mu     sync.Mutex
	engine *AutomationEngine
	fired  int
}

func (c *hotkeyLoopClient) TriggerHotkeyByName(hotkeyName string) error {
	if err := c.MockOBSClient.TriggerHotkeyByName(hotkeyName); err != nil {
		return err
	}

	c.mu.Lock()
	c.fired++
	engine := c.engine
	c.mu.Unlock()

	// The hotkey switched the scene, and OBS says so.
	engine.HandleEvent(EventPayload{
		EventType: EventSceneChanged,
		Data:      map[string]interface{}{"scene_name": "Game"},
		Timestamp: time.Now(),
	})
	return nil
}

func (c *hotkeyLoopClient) fireCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

func TestEngineDisablesARuleThatWillNotStop(t *testing.T) {
	db, cleanup := testAutomationDB(t)
	defer cleanup()

	ctx := context.Background()
	ruleID, err := db.CreateAutomationRule(ctx, storage.AutomationRule{
		Name:        "hotkey loop",
		Enabled:     true,
		TriggerType: TriggerTypeEvent,
		TriggerConfig: map[string]interface{}{
			"event_type": EventSceneChanged,
		},
		Actions: []storage.RuleAction{
			{Type: ActionTypeTriggerHotkey, Parameters: map[string]interface{}{
				"hotkey_name": "OBSBasic.SelectScene",
			}},
		},
	})
	require.NoError(t, err)

	client := &hotkeyLoopClient{MockOBSClient: NewMockOBSClient()}
	engine := NewAutomationEngine(db, client)
	client.engine = engine

	require.NoError(t, engine.Start())
	defer engine.Stop()

	engine.HandleEvent(EventPayload{
		EventType: EventSceneChanged,
		Data:      map[string]interface{}{"scene_name": "Game"},
		Timestamp: time.Now(),
	})

	// Long enough for an unchecked loop to run to thousands.
	time.Sleep(500 * time.Millisecond)

	fired := client.fireCount()
	if fired == 0 {
		t.Fatal("the rule never fired, so this test is not measuring anything")
	}
	if fired > 200 {
		t.Errorf("the rule fired %d times; the breaker did not stop it", fired)
	}

	// Disabled in the database, not only in memory. A runaway rule that came
	// back on the next restart would be a worse bug than the one being fixed.
	stored, err := db.GetAutomationRule(ctx, ruleID)
	require.NoError(t, err)
	if stored.Enabled {
		t.Error("the rule is still enabled in the database")
	}

	// And the operator has to be able to find out why. A rule that switched
	// itself off with no record is indistinguishable from one that never ran.
	executions, err := db.GetRecentRuleExecutions(ctx, 50)
	require.NoError(t, err)

	found := false
	for _, exec := range executions {
		if exec.RuleID == ruleID && exec.Error == oscillationError {
			found = true
			if exec.Status != "failed" {
				t.Errorf("the oscillation record has status %q", exec.Status)
			}
		}
	}
	if !found {
		t.Errorf("no execution row records why the rule was disabled; %d rows exist",
			len(executions))
	}
}

func TestEngineLeavesABusyRuleAlone(t *testing.T) {
	db, cleanup := testAutomationDB(t)
	defer cleanup()

	ctx := context.Background()
	ruleID, err := db.CreateAutomationRule(ctx, storage.AutomationRule{
		Name:        "busy but fine",
		Enabled:     true,
		TriggerType: TriggerTypeEvent,
		TriggerConfig: map[string]interface{}{
			"event_type": EventSceneChanged,
		},
		Actions: []storage.RuleAction{
			{Type: ActionTypeSetMute, Parameters: map[string]interface{}{
				"input_name": "Microphone",
				"muted":      true,
			}},
		},
	})
	require.NoError(t, err)

	mock := NewMockOBSClient()
	engine := NewAutomationEngine(db, mock)
	require.NoError(t, engine.Start())
	defer engine.Stop()

	// A run of genuine external events, below the threshold. Disabling this
	// rule would be the tool breaking a working setup, which is worse than the
	// loop because the operator did nothing wrong.
	for i := 0; i < oscillationThreshold-1; i++ {
		engine.HandleEvent(EventPayload{
			EventType: EventSceneChanged,
			Data:      map[string]interface{}{"scene_name": "Game"},
			Timestamp: time.Now(),
		})
	}
	time.Sleep(300 * time.Millisecond)

	stored, err := db.GetAutomationRule(ctx, ruleID)
	require.NoError(t, err)
	if !stored.Enabled {
		t.Errorf("a rule that fired %d times in a burst was disabled",
			oscillationThreshold-1)
	}
}
