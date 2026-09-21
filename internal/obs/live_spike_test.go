//go:build obslive

package obs_test

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/events"
	"github.com/andreykaipov/goobs/api/events/subscriptions"
	"github.com/andreykaipov/goobs/api/requests/inputs"
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

// The receiving half of the fallback probe, which matters now that the vendor
// path is closed to Lua at the pointer round trip.
//
// The plan recorded the fallback as SetInputSettings plus a short
// GetInputSettings poll on a correlation id. The poll may be unnecessary:
// obs-websocket has an InputSettingsChanged event carrying the whole new
// settings object. What is not obvious is whether a write from *inside* OBS
// raises it -- obs_source_update defers on an active source, and a deferred
// update is where a signal goes missing. If it arrives, the fallback is push
// and costs nothing the vendor event would not have.
//
// This reads raw goobs rather than the sink, because eventFrom does not
// translate InputSettingsChanged. Teaching it to is the bridge's work; a spike
// should not need a production change to answer its own question.
func TestLiveSpikeMailboxWriteReachesTheSocket(t *testing.T) {
	const mailbox = "agentic-obs-spike-mailbox"

	address := fmt.Sprintf("%s:%s", envOr("OBS_HOST", "localhost"), envOr("OBS_PORT", "4455"))
	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1")
	}

	opts := []goobs.Option{goobs.WithEventSubscriptions(subscriptions.All)}
	if pw := os.Getenv("OBS_PASSWORD"); pw != "" {
		opts = append(opts, goobs.WithPassword(pw))
	}
	client, err := goobs.New(address, opts...)
	if err != nil {
		t.Fatalf("goobs.New: %v", err)
	}
	defer client.Disconnect()

	deadline := time.After(12 * time.Second)
	for {
		select {
		case <-deadline:
			t.Skipf("no InputSettingsChanged for %q in 12s. Either the spike script is "+
				"not loaded, or -- the answer worth having -- a Lua obs_source_update "+
				"does not raise the signal obs-websocket listens on, and the fallback "+
				"needs the GetInputSettings poll after all.", mailbox)
		case raw, ok := <-client.IncomingEvents:
			if !ok {
				t.Fatal("event channel closed")
			}
			e, isSettings := raw.(*events.InputSettingsChanged)
			if !isSettings || e.InputName != mailbox {
				continue
			}

			if _, present := e.InputSettings["seq"]; !present {
				t.Fatalf("the event arrived without the payload: %#v", e.InputSettings)
			}
			if marshalled, _ := e.InputSettings["marshalled"].(string); marshalled != "yes" {
				t.Fatalf("the event arrived without its string field: %#v", e.InputSettings)
			}

			t.Logf("ANSWER: a Lua write to a source's settings reaches the socket as "+
				"InputSettingsChanged, carrying the payload. The reply path is push, "+
				"one hop, no poll. Payload: %#v", e.InputSettings)
			return
		}
	}
}

// The round trip, which is the design itself rather than one half of it:
// agentic-obs writes Lua to the inbox, the script runs it, the answer comes
// back on the mailbox.
//
// The code sent is arithmetic on purpose. It has to be something only a real
// Lua interpreter produces, so a stubbed or echoed reply cannot pass -- and it
// must touch no OBS state, because this runs against a live instance.
func TestLiveSpikeCodeRoundTripsThroughTheBridge(t *testing.T) {
	const (
		inbox   = "agentic-obs-spike-inbox"
		mailbox = "agentic-obs-spike-mailbox"
	)

	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1")
	}
	address := fmt.Sprintf("%s:%s", envOr("OBS_HOST", "localhost"), envOr("OBS_PORT", "4455"))

	opts := []goobs.Option{goobs.WithEventSubscriptions(subscriptions.All)}
	if pw := os.Getenv("OBS_PASSWORD"); pw != "" {
		opts = append(opts, goobs.WithPassword(pw))
	}
	client, err := goobs.New(address, opts...)
	if err != nil {
		t.Fatalf("goobs.New: %v", err)
	}
	defer client.Disconnect()

	// Unique per run, because obs_source_update merges rather than replaces:
	// the mailbox still carries keys from earlier writes, and only an id this
	// run invented distinguishes our answer from a leftover.
	id := fmt.Sprintf("rt-%d", time.Now().UnixNano())
	code := "return 6 * 7"

	_, err = client.Inputs.SetInputSettings(&inputs.SetInputSettingsParams{
		InputName:     &[]string{inbox}[0],
		InputSettings: map[string]any{"id": id, "lua": code},
	})
	if err != nil {
		t.Skipf("could not write to %q (%v). Is the spike script loaded?", inbox, err)
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("sent %q to %q as id %s and no reply came back in 15s", code, inbox, id)
		case raw, ok := <-client.IncomingEvents:
			if !ok {
				t.Fatal("event channel closed")
			}
			e, isSettings := raw.(*events.InputSettingsChanged)
			if !isSettings || e.InputName != mailbox {
				continue
			}
			if got, _ := e.InputSettings["id"].(string); got != id {
				continue
			}

			if ran, _ := e.InputSettings["ok"].(bool); !ran {
				t.Fatalf("the bridge ran %q and it failed: %v", code, e.InputSettings["result"])
			}
			if got, _ := e.InputSettings["result"].(string); got != "42" {
				t.Fatalf("the bridge answered %q, want \"42\" -- it did not evaluate the code", got)
			}

			t.Logf("ANSWER: %q went in as id %s and 42 came back. The bridge runs code "+
				"handed to it over the socket, both directions push, no polling and no "+
				"script reload.", code, id)
			return
		}
	}
}
