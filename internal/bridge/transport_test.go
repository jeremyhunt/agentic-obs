package bridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// errTestNoSuchInput is what obs-websocket returns when the bridge is not
// loaded: the inbox source simply does not exist.
var errTestNoSuchInput = errors.New("no such input: agentic-obs-inbox")

// fakeCaller records inbox writes and optionally replies through the transport,
// standing in for OBS plus the Lua script.
type fakeCaller struct {
	mu      sync.Mutex
	writes  []map[string]interface{}
	err     error
	onWrite func(settings map[string]interface{})
}

func (f *fakeCaller) SetSourceSettings(name string, settings map[string]interface{}, overlay bool) error {
	f.mu.Lock()
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		return err
	}
	f.writes = append(f.writes, settings)
	onWrite := f.onWrite
	f.mu.Unlock()

	if onWrite != nil {
		go onWrite(settings)
	}
	return nil
}

// reply builds the event the bridge would produce for a given command.
func reply(id string, ok bool, value interface{}, errMsg string) obs.Event {
	return obs.Event{
		Type: obs.EventTypeInputSettingsChanged,
		Payload: map[string]interface{}{
			"input_name": MailboxSource,
			"settings": map[string]interface{}{
				"id": id, "ok": ok, "result": value, "error": errMsg,
			},
		},
	}
}

func TestRunSendsCommandAndReturnsTheReply(t *testing.T) {
	var transport *Transport
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		transport.HandleEvent(reply(s["id"].(string), true, "42", ""))
	}
	transport = New(caller)

	got, err := transport.Run(context.Background(), "return 6 * 7", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.OK || got.Value != "42" {
		t.Fatalf("got %+v, want ok with 42", got)
	}

	// The command must carry all three fields every time. Settings writes
	// merge, so omitting args would leave the previous call's args in place.
	if len(caller.writes) != 1 {
		t.Fatalf("wrote %d commands, want 1", len(caller.writes))
	}
	sent := caller.writes[0]
	for _, key := range []string{"id", "lua", "args"} {
		if _, present := sent[key]; !present {
			t.Errorf("the command omitted %q: %#v", key, sent)
		}
	}
	if sent["lua"] != "return 6 * 7" {
		t.Errorf("lua = %v", sent["lua"])
	}
}

// args must go as an array of typed entries. The bridge cannot iterate an
// object's keys -- obs_data_item_next takes a pointer-to-pointer that SWIG
// will not marshal from Lua -- so an object here would arrive empty.
func TestRunEncodesArgsAsATypedArray(t *testing.T) {
	var transport *Transport
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		transport.HandleEvent(reply(s["id"].(string), true, "ok", ""))
	}
	transport = New(caller)

	_, err := transport.Run(context.Background(), "return 1", map[string]interface{}{
		"name":  "Game",
		"count": 3,
		"on":    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries, ok := caller.writes[0]["args"].([]interface{})
	if !ok {
		t.Fatalf("args is %T, want an array", caller.writes[0]["args"])
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	// Sorted by name: count, name, on.
	first := entries[0].(map[string]interface{})
	if first["name"] != "count" || first["n"] != 3.0 {
		t.Errorf("entries[0] = %#v, want count tagged as a number", first)
	}
	second := entries[1].(map[string]interface{})
	if second["name"] != "name" || second["s"] != "Game" {
		t.Errorf("entries[1] = %#v, want name tagged as a string", second)
	}
	third := entries[2].(map[string]interface{})
	if third["name"] != "on" || third["b"] != true {
		t.Errorf("entries[2] = %#v, want on tagged as a bool", third)
	}
}

func TestRunIgnoresRepliesForOtherIDs(t *testing.T) {
	var transport *Transport
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		transport.HandleEvent(reply("a-stale-id", true, "wrong", ""))
		transport.HandleEvent(reply(s["id"].(string), true, "right", ""))
	}
	transport = New(caller)

	got, err := transport.Run(context.Background(), "return 1", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Value != "right" {
		t.Fatalf("a stale reply was accepted: %+v", got)
	}
}

func TestRunIgnoresEventsFromOtherSources(t *testing.T) {
	var transport *Transport
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		other := reply(s["id"].(string), true, "wrong", "")
		other.Payload["input_name"] = "some other source"
		transport.HandleEvent(other)
		transport.HandleEvent(reply(s["id"].(string), true, "right", ""))
	}
	transport = New(caller)

	got, _ := transport.Run(context.Background(), "return 1", nil)
	if got.Value != "right" {
		t.Fatalf("an event from another source was accepted: %+v", got)
	}
}

func TestRunTimesOutWhenNothingReplies(t *testing.T) {
	transport := New(&fakeCaller{})
	transport.Timeout = 30 * time.Millisecond

	_, err := transport.Run(context.Background(), "return 1", nil)
	if err == nil {
		t.Fatal("Run returned no error when the bridge never answered")
	}
}

// A failed write is the bridge being absent: the inbox source does not exist,
// so obs-websocket rejects the request. That must surface immediately rather
// than waiting out the timeout.
func TestRunFailsFastWhenTheInboxIsMissing(t *testing.T) {
	transport := New(&fakeCaller{err: errTestNoSuchInput})
	transport.Timeout = 10 * time.Second

	start := time.Now()
	if _, err := transport.Run(context.Background(), "return 1", nil); err == nil {
		t.Fatal("Run succeeded with a failing caller")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Run waited for the timeout instead of failing on the write")
	}
}

func TestRunSerialisesConcurrentCallers(t *testing.T) {
	var transport *Transport
	var inFlight, maxInFlight int
	var mu sync.Mutex

	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(5 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		transport.HandleEvent(reply(s["id"].(string), true, "ok", ""))
	}
	transport = New(caller)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = transport.Run(context.Background(), "return 1", nil)
		}()
	}
	wg.Wait()

	if maxInFlight != 1 {
		t.Fatalf("%d commands were in flight at once; settings writes merge, so only one is safe", maxInFlight)
	}
}
