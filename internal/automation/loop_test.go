package automation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ironystock/agentic-obs/internal/storage"
)

// The hazard here is neither hypothetical nor new: a rule that triggers on
// source_visibility_changed and acts by setting visibility has always
// re-entered HandleEvent through its own write, and cooldown is off by default.
// These tests feed the engine its own echo the way OBS does, and check that it
// stops.

// echoingClient re-emits every write as the event OBS would send. That is the
// whole mechanism the engine has to survive, so the test has to reproduce it
// rather than assert around it.
type echoingClient struct {
	*MockOBSClient

	mu     sync.Mutex
	engine *AutomationEngine
	writes int
}

func (c *echoingClient) SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error {
	if err := c.MockOBSClient.SetSceneItemEnabled(sceneName, sceneItemID, enabled); err != nil {
		return err
	}

	c.mu.Lock()
	c.writes++
	engine := c.engine
	c.mu.Unlock()

	// OBS announces every write, including this one.
	engine.HandleEvent(EventPayload{
		EventType: EventSourceVisibilityChanged,
		Data: map[string]interface{}{
			"scene_name":    sceneName,
			"scene_item_id": sceneItemID,
			"visible":       enabled,
		},
		Timestamp: time.Now(),
	})
	return nil
}

func (c *echoingClient) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

func TestEngineDoesNotFeedItselfThroughItsOwnWrite(t *testing.T) {
	engine, client, cleanup := newLoopEngine(t)
	defer cleanup()

	// One change from outside starts it.
	engine.HandleEvent(externalVisibility())

	// A loop would be unbounded, so waiting a fixed moment and counting is the
	// measurement: there is no "done" to wait for, only a number that either
	// settles or does not.
	time.Sleep(300 * time.Millisecond)

	if got := client.writeCount(); got != 1 {
		t.Errorf("one external event produced %d writes; the engine's own echo is "+
			"re-triggering the rule that caused it", got)
	}
}

func TestEngineStillAnswersTheOperator(t *testing.T) {
	engine, client, cleanup := newLoopEngine(t)
	defer cleanup()

	// Two separate external changes, the second identical in shape to the echo
	// the first produced. Eating it would be the failure suppression can
	// introduce: an operator flicking a source back and forth looks exactly
	// like this, and ignoring them is worse than the loop.
	engine.HandleEvent(externalVisibility())
	time.Sleep(300 * time.Millisecond)
	engine.HandleEvent(externalVisibility())
	time.Sleep(300 * time.Millisecond)

	if got := client.writeCount(); got != 2 {
		t.Errorf("two external events produced %d writes, want 2; the second was "+
			"mistaken for our own echo", got)
	}
}

// externalVisibility is a change somebody else made: the item was hidden, and
// the rule exists to put it back.
func externalVisibility() EventPayload {
	return EventPayload{
		EventType: EventSourceVisibilityChanged,
		Data: map[string]interface{}{
			"scene_name":    "Game",
			"scene_item_id": 7,
			"visible":       false,
		},
		Timestamp: time.Now(),
	}
}

func newLoopEngine(t *testing.T) (*AutomationEngine, *echoingClient, func()) {
	t.Helper()

	db, cleanupDB := testAutomationDB(t)

	// The rule that closes the loop: it reacts to visibility by setting
	// visibility, with no cooldown, which is the default.
	_, err := db.CreateAutomationRule(context.Background(), storage.AutomationRule{
		Name:        "keep it visible",
		Enabled:     true,
		TriggerType: TriggerTypeEvent,
		TriggerConfig: map[string]interface{}{
			"event_type": EventSourceVisibilityChanged,
		},
		Actions: []storage.RuleAction{
			{Type: ActionTypeSetVisibility, Parameters: map[string]interface{}{
				"scene_name": "Game",
				"source_id":  7,
				"visible":    true,
			}},
		},
	})
	require.NoError(t, err)

	client := &echoingClient{MockOBSClient: NewMockOBSClient()}
	engine := NewAutomationEngine(db, client)
	client.engine = engine

	require.NoError(t, engine.Start())

	return engine, client, func() {
		engine.Stop()
		cleanupDB()
	}
}
