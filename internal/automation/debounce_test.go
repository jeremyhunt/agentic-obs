package automation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ironystock/agentic-obs/internal/storage"
)

// Debounce is for rules that are correct and not looping, and still do far more
// work than they need to. Showing a stack of nine layers fires a
// source_visibility_changed rule nine times in a few milliseconds; the rule
// wants to run once, at the end.
//
// It is not cooldown. Cooldown runs the rule on the *first* event and ignores
// the rest, so it acts on the state before the burst. Debounce runs it on the
// *last*, which is the state the operator ended up in.

func TestDebouncedRuleRunsOnceForABurst(t *testing.T) {
	engine, mock, clk, cleanup := newDebounceEngine(t, 200)
	defer cleanup()

	for i := 0; i < 9; i++ {
		engine.HandleEvent(debounceEvent("Game"))
	}
	drain(t, engine)

	if got := len(mock.GetActions()); got != 0 {
		t.Fatalf("the rule ran %d times during the burst; it should be waiting", got)
	}

	clk.Advance(250 * time.Millisecond)
	settleActions(t, mock, 1)

	if got := len(mock.GetActions()); got != 1 {
		t.Errorf("nine events produced %d executions, want 1", got)
	}
}

func TestDebounceResetsWhileEventsKeepArriving(t *testing.T) {
	engine, mock, clk, cleanup := newDebounceEngine(t, 200)
	defer cleanup()

	// An event every 150ms for a while. The window never elapses, so the rule
	// never runs -- a trailing-edge debounce waits for quiet, and this is the
	// behaviour that makes it wrong for anything needing a guaranteed response.
	for i := 0; i < 5; i++ {
		engine.HandleEvent(debounceEvent("Game"))
		drain(t, engine)
		clk.Advance(150 * time.Millisecond)
	}

	if got := len(mock.GetActions()); got != 0 {
		t.Errorf("the rule ran %d times while events kept resetting the window", got)
	}

	// Quiet at last.
	clk.Advance(250 * time.Millisecond)
	settleActions(t, mock, 1)
	if got := len(mock.GetActions()); got != 1 {
		t.Errorf("after the events stopped the rule ran %d times, want 1", got)
	}
}

func TestSeparateBurstsEachGetTheirOwnRun(t *testing.T) {
	engine, mock, clk, cleanup := newDebounceEngine(t, 200)
	defer cleanup()

	for burst := 0; burst < 3; burst++ {
		for i := 0; i < 4; i++ {
			engine.HandleEvent(debounceEvent("Game"))
		}
		drain(t, engine)
		clk.Advance(250 * time.Millisecond)
		settleActions(t, mock, burst+1)
	}

	if got := len(mock.GetActions()); got != 3 {
		t.Errorf("three separate bursts produced %d executions, want 3", got)
	}
}

func TestARuleWithoutDebounceStillRunsImmediately(t *testing.T) {
	engine, mock, _, cleanup := newDebounceEngine(t, 0)
	defer cleanup()

	engine.HandleEvent(debounceEvent("Game"))
	settleActions(t, mock, 1)

	// Debounce is opt-in. Making every rule wait would change the timing of
	// every existing configuration, and a rule that switches scene on a
	// recording-started event has to fire now.
	if got := len(mock.GetActions()); got != 1 {
		t.Errorf("an undebounced rule ran %d times immediately, want 1", got)
	}
}

func TestStoppingTheEngineDropsAPendingDebounce(t *testing.T) {
	engine, mock, clk, cleanup := newDebounceEngine(t, 200)
	defer cleanup()

	engine.HandleEvent(debounceEvent("Game"))
	drain(t, engine)

	engine.Stop()

	// Checked here, before the clock moves. Cancelling is Stop's job, so it has
	// to be done by the time Stop returns -- and once the clock advances the
	// timer fires and clears its own entry, which empties the map whether it was
	// cancelled or merely ignored. The assertion only means something before
	// that happens.
	//
	// It is not pedantry: runDebounced refuses to act on a stopped engine, so
	// the action count below stays at zero either way, while a real
	// time.AfterFunc left pending holds a runtime timer alive for its full
	// duration.
	engine.mu.Lock()
	pending := len(engine.debounced)
	engine.mu.Unlock()
	if pending != 0 {
		t.Errorf("%d debounce timers were still pending when Stop returned", pending)
	}

	clk.Advance(500 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	// And a timer that fired after Stop would execute a rule against an engine
	// that has released its OBS client, after the process believed it had
	// finished.
	if got := len(mock.GetActions()); got != 0 {
		t.Errorf("a pending debounce fired %d times after the engine stopped", got)
	}
}

// --- helpers ---------------------------------------------------------

func debounceEvent(scene string) EventPayload {
	return EventPayload{
		EventType: EventSceneChanged,
		Data:      map[string]interface{}{"scene_name": scene},
		Timestamp: time.Now(),
	}
}

// drain waits for the engine's event channel to be processed. The channel hop
// is real concurrency, so it is waited on; the debounce window is a deadline,
// so it is advanced.
func drain(t *testing.T, engine *AutomationEngine) {
	t.Helper()
	time.Sleep(60 * time.Millisecond)
}

func settleActions(t *testing.T, mock *MockOBSClient, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.GetActions()) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newDebounceEngine(t *testing.T, debounceMs int) (*AutomationEngine, *MockOBSClient, *fakeClock, func()) {
	t.Helper()

	db, cleanupDB := testAutomationDB(t)

	trigger := map[string]interface{}{"event_type": EventSceneChanged}
	if debounceMs > 0 {
		trigger["debounce_ms"] = debounceMs
	}

	_, err := db.CreateAutomationRule(context.Background(), storage.AutomationRule{
		Name:          "debounced",
		Enabled:       true,
		TriggerType:   TriggerTypeEvent,
		TriggerConfig: trigger,
		Actions: []storage.RuleAction{
			{Type: ActionTypeTriggerHotkey, Parameters: map[string]interface{}{
				"hotkey_name": "OBSBasic.SelectScene",
			}},
		},
	})
	require.NoError(t, err)

	mock := NewMockOBSClient()
	engine := NewAutomationEngine(db, mock)
	clk := newFakeClock()
	engine.setClock(clk)

	require.NoError(t, engine.Start())

	stopped := false
	return engine, mock, clk, func() {
		if !stopped {
			engine.Stop()
			stopped = true
		}
		cleanupDB()
	}
}
