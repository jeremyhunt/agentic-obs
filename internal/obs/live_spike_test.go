//go:build obslive

package obs_test

import (
	"sync"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// The receiving half of the bridge spike (bridge/spike/agentic-obs-spike.lua).
//
// The spike's question is whether SWIG marshals a Lua-held obs_data_t* through
// calldata_set_ptr's void*, and the Script Log cannot answer it: the emit call
// returns success either way. Only the far end knows, because a dropped pointer
// arrives as a vendor event with empty data rather than as a failure.
//
// So this listens on the connection agentic-obs already holds and reads what
// actually came out the other side. If it passes, an in-OBS script can answer
// over one socket with no polling and no second transport, and ADR-013 can be
// written. If the event arrives with no fields, the bridge takes the
// SetInputSettings plus GetInputSettings-poll fallback instead.

const spikeVendor = "agentic-obs-spike"

// spikeSink collects vendor events from the spike and nothing else.
type spikeSink struct {
	mu     sync.Mutex
	events []map[string]interface{}
}

func (s *spikeSink) HandleEvent(e obs.Event) {
	if e.Type != obs.EventTypeVendorEvent {
		return
	}
	if name, _ := e.Payload["vendor_name"].(string); name != spikeVendor {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e.Payload)
}

func (s *spikeSink) first() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return s.events[0]
}

func TestLiveSpikeVendorEventCarriesItsPayload(t *testing.T) {
	client := liveClient(t)

	sink := &spikeSink{}
	client.SetEventSink(sink)

	// The spike emits every two seconds, so a listener started at any moment
	// catches one well inside this window. Waiting rather than triggering is
	// deliberate: a Lua script cannot serve a vendor *request*, which is the
	// constraint the whole bridge design is shaped around.
	deadline := time.Now().Add(12 * time.Second)
	var payload map[string]interface{}
	for time.Now().Before(deadline) {
		if payload = sink.first(); payload != nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if payload == nil {
		t.Skipf("no %q vendor event in 12s. Load bridge/spike/agentic-obs-spike.lua "+
			"through OBS Tools -> Scripts and run this again; if it is loaded, read the "+
			"Script Log, because a failure to register the vendor is the earlier answer.",
			spikeVendor)
	}

	if got, _ := payload["event_type"].(string); got != "probe" {
		t.Errorf("event_type is %q, want \"probe\"", got)
	}

	data, ok := payload["event_data"].(map[string]interface{})
	if !ok {
		t.Fatalf("event_data is %T, not an object", payload["event_data"])
	}

	// This is the whole spike. An empty object means vendor_event_emit ran and
	// the obs_data_t did not survive the trip through void*.
	if len(data) == 0 {
		t.Fatalf("ANSWER: the event arrived with NO data. SWIG did not marshal the " +
			"obs_data_t through calldata_set_ptr, so the bridge cannot reply over " +
			"vendor_event_emit and needs the SetInputSettings fallback.")
	}

	if _, present := data["seq"]; !present {
		t.Errorf("the payload arrived without seq: %#v", data)
	}
	if marshalled, _ := data["marshalled"].(string); marshalled != "yes" {
		t.Errorf("the payload arrived without its string field: %#v", data)
	}

	t.Logf("ANSWER: a Lua-held obs_data_t survives calldata_set_ptr. "+
		"The bridge can reply over vendor_event_emit on the existing connection, "+
		"with no polling and no second transport. Payload: %#v", data)
}
