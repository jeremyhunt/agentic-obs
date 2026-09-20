package obs

import (
	"testing"

	"github.com/andreykaipov/goobs/api/events/subscriptions"
)

// TestDefaultEventSubscriptionsCoverWhatIsHandled guards the mask against the
// failure it already had: a category left out is not an error anywhere, it is
// simply an event that never arrives, so the handler for it looks correct
// forever. Filters and Vendors were both missing.
//
// Vendors carries VendorEvent, which is the only way a plugin -- Advanced Scene
// Switcher, obs-browser, or an in-OBS bridge script -- can speak back to us.
// Without it that whole channel is one-way. (FB-62)
func TestDefaultEventSubscriptionsCoverWhatIsHandled(t *testing.T) {
	mask := DefaultEventSubscriptions()

	required := []struct {
		name string
		bit  int
		why  string
	}{
		{"Scenes", subscriptions.Scenes, "scene created/removed/changed notifications"},
		{"Outputs", subscriptions.Outputs, "recording, streaming, virtual cam, replay buffer"},
		{"Inputs", subscriptions.Inputs, "audio mute and volume changes"},
		{"SceneItems", subscriptions.SceneItems, "source visibility changes"},
		{"Transitions", subscriptions.Transitions, "transition events"},
		{"Ui", subscriptions.Ui, "studio mode changes"},
		{"Filters", subscriptions.Filters, "filter enable and settings changes"},
		{"Vendors", subscriptions.Vendors, "VendorEvent -- the only inbound channel from a plugin"},
	}

	for _, r := range required {
		if mask&r.bit == 0 {
			t.Errorf("default subscriptions omit %s (%s); events in that category never arrive", r.name, r.why)
		}
	}
}

// TestHighVolumeSubscriptionsAreNotOnByDefault is the other half: subscribing to
// everything is as wrong as subscribing to too little, just less visibly.
// SceneItemTransformChanged fires continuously while an item is dragged, and
// InputVolumeMeters arrives tens of times a second whether or not anything is
// listening. Both are opt-in in obs-websocket for that reason, and neither has a
// consumer here.
func TestHighVolumeSubscriptionsAreNotOnByDefault(t *testing.T) {
	mask := DefaultEventSubscriptions()

	excluded := []struct {
		name string
		bit  int
	}{
		{"InputVolumeMeters", subscriptions.InputVolumeMeters},
		{"InputActiveStateChanged", subscriptions.InputActiveStateChanged},
		{"InputShowStateChanged", subscriptions.InputShowStateChanged},
		{"SceneItemTransformChanged", subscriptions.SceneItemTransformChanged},
	}

	for _, e := range excluded {
		if mask&e.bit != 0 {
			t.Errorf("default subscriptions include high-volume %s; it should be opt-in", e.name)
		}
	}
}

// TestConnectionConfigOverridesTheDefaultMask proves the field is honoured
// rather than decorative -- a config option that silently does nothing is the
// same class of defect as the missing category above.
func TestConnectionConfigOverridesTheDefaultMask(t *testing.T) {
	custom := subscriptions.Scenes | subscriptions.Vendors

	c := NewClient(ConnectionConfig{Host: "localhost", Port: "4455", EventSubscriptions: custom})
	if c.eventSubscriptions != custom {
		t.Errorf("client used %v, want the configured %v", c.eventSubscriptions, custom)
	}

	// Zero means "not set", not "subscribe to nothing" -- otherwise every caller
	// that builds a ConnectionConfig without thinking about events gets silence.
	d := NewClient(ConnectionConfig{Host: "localhost", Port: "4455"})
	if d.eventSubscriptions != DefaultEventSubscriptions() {
		t.Errorf("an unset mask gave %v, want the default %v", d.eventSubscriptions, DefaultEventSubscriptions())
	}
}
