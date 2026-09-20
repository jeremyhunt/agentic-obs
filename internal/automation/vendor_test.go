package automation

import (
	"context"
	"testing"

	"github.com/ironystock/agentic-obs/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The automation side of the vendor channel, which makes it bidirectional in
// practice rather than in principle: a rule can call a plugin, and a plugin's
// event can trigger a rule. (FB-78)

// engineWithRule starts an engine holding one rule.
func engineWithRule(t *testing.T, rule storage.AutomationRule) (*AutomationEngine, *MockOBSClient) {
	t.Helper()

	db, cleanup := testAutomationDB(t)
	t.Cleanup(cleanup)

	_, err := db.CreateAutomationRule(context.Background(), rule)
	require.NoError(t, err)

	mock := NewMockOBSClient()
	engine := NewAutomationEngine(db, mock)
	require.NoError(t, engine.Start())
	t.Cleanup(func() { engine.Stop() })

	return engine, mock
}

func TestCallVendorRequestAction(t *testing.T) {
	t.Run("calls the vendor with the parameters given", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		result := executor.ExecuteAction(Action{
			Type: ActionTypeCallVendorRequest,
			Parameters: map[string]interface{}{
				"vendor_name":  "AdvancedSceneSwitcher",
				"request_type": "AdvancedSceneSwitcherRunMacro",
				"request_data": map[string]interface{}{"macro": "PersonaShow_Sonic"},
			},
		}, 0)

		require.True(t, result.Success, "action failed: %s", result.Error)
		assert.Contains(t, mock.GetActions(),
			"call_vendor_request:AdvancedSceneSwitcher/AdvancedSceneSwitcherRunMacro")
	})

	t.Run("request_data is optional", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		// Plenty of vendor requests take no payload; requiring one would make
		// the common case awkward for nothing.
		result := executor.ExecuteAction(Action{
			Type: ActionTypeCallVendorRequest,
			Parameters: map[string]interface{}{
				"vendor_name": "obs-browser", "request_type": "emit_event",
			},
		}, 0)

		assert.True(t, result.Success, "action failed: %s", result.Error)
	})

	t.Run("a missing parameter fails the action rather than calling nothing", func(t *testing.T) {
		mock := NewMockOBSClient()
		executor := NewExecutor(mock)

		for _, params := range []map[string]interface{}{
			{"request_type": "x"},
			{"vendor_name": "y"},
		} {
			result := executor.ExecuteAction(Action{
				Type: ActionTypeCallVendorRequest, Parameters: params,
			}, 0)
			assert.False(t, result.Success)
			assert.NotEmpty(t, result.Error)
		}
		assert.Empty(t, mock.GetActions(), "nothing should have been sent")
	})

	t.Run("a failing vendor call fails the action", func(t *testing.T) {
		mock := NewMockOBSClient()
		mock.failNextCall = true
		executor := NewExecutor(mock)

		// A rule that stepped over a failed plugin call would leave the
		// operator believing a macro ran. Dropped from an earlier draft of this
		// file and caught by mutation: swallowing the error left every test
		// here green. (FB-78)
		result := executor.ExecuteAction(Action{
			Type: ActionTypeCallVendorRequest,
			Parameters: map[string]interface{}{
				"vendor_name": "AdvancedSceneSwitcher", "request_type": "RunMacro",
			},
		}, 0)

		assert.False(t, result.Success, "a vendor error must fail the action")
		assert.NotEmpty(t, result.Error)
	})
}

func TestVendorEventTriggersRules(t *testing.T) {
	t.Run("a vendor event reaches a matching rule", func(t *testing.T) {
		engine, mock := engineWithRule(t, storage.AutomationRule{
			Name:        "on any vendor event",
			Enabled:     true,
			TriggerType: TriggerTypeEvent,
			TriggerConfig: map[string]interface{}{
				"event_type": EventVendorEvent,
			},
			Actions: []storage.RuleAction{
				{Type: ActionTypeSetScene, Parameters: map[string]interface{}{"scene_name": "Live"}},
			},
		})

		engine.HandleEvent(EventPayload{
			EventType: EventVendorEvent,
			Data: map[string]interface{}{
				"vendor_name": "AdvancedSceneSwitcher",
				"event_type":  "MacroRun",
			},
		})

		requireActionEventually(t, mock, "set_scene:Live")
	})

	t.Run("filters on vendor_name so one plugin does not fire another's rules", func(t *testing.T) {
		engine, mock := engineWithRule(t, storage.AutomationRule{
			Name:        "only ASS",
			Enabled:     true,
			TriggerType: TriggerTypeEvent,
			TriggerConfig: map[string]interface{}{
				"event_type":   EventVendorEvent,
				"event_filter": map[string]interface{}{"vendor_name": "AdvancedSceneSwitcher"},
			},
			Actions: []storage.RuleAction{
				{Type: ActionTypeSetScene, Parameters: map[string]interface{}{"scene_name": "Live"}},
			},
		})

		// Without the filter, any plugin emitting anything would set off every
		// vendor rule -- and obs-browser emits on its own schedule.
		engine.HandleEvent(EventPayload{
			EventType: EventVendorEvent,
			Data:      map[string]interface{}{"vendor_name": "obs-browser", "event_type": "MacroRun"},
		})
		assertActionCountStaysAt(t, mock, 0)

		engine.HandleEvent(EventPayload{
			EventType: EventVendorEvent,
			Data:      map[string]interface{}{"vendor_name": "AdvancedSceneSwitcher", "event_type": "MacroRun"},
		})
		requireActionEventually(t, mock, "set_scene:Live")
	})

	t.Run("filters on the vendor's own event_type", func(t *testing.T) {
		engine, mock := engineWithRule(t, storage.AutomationRule{
			Name:        "only MacroRun",
			Enabled:     true,
			TriggerType: TriggerTypeEvent,
			TriggerConfig: map[string]interface{}{
				"event_type": EventVendorEvent,
				"event_filter": map[string]interface{}{
					"vendor_name": "AdvancedSceneSwitcher",
					"event_type":  "MacroRun",
				},
			},
			Actions: []storage.RuleAction{
				{Type: ActionTypeSetScene, Parameters: map[string]interface{}{"scene_name": "Live"}},
			},
		})

		// A vendor emits several event types; a rule usually wants one of them.
		engine.HandleEvent(EventPayload{
			EventType: EventVendorEvent,
			Data:      map[string]interface{}{"vendor_name": "AdvancedSceneSwitcher", "event_type": "SomethingElse"},
		})
		assertActionCountStaysAt(t, mock, 0)

		engine.HandleEvent(EventPayload{
			EventType: EventVendorEvent,
			Data:      map[string]interface{}{"vendor_name": "AdvancedSceneSwitcher", "event_type": "MacroRun"},
		})
		requireActionEventually(t, mock, "set_scene:Live")
	})
}
