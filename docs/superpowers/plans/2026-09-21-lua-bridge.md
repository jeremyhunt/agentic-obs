# Lua Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give agentic-obs a way to run code inside the OBS process, over a source-settings transport, with the scripting channel compiled out of the default build.

**Architecture:** A fixed Lua script loaded in OBS owns a sandbox, an instruction bound and result marshalling. agentic-obs writes `{id, lua, args}` to an inbox source; OBS raises `update`; the script runs the chunk and writes `{id, ok, result}` to a mailbox source, which returns as `InputSettingsChanged`. Both directions are push. The eval-facing MCP tool sits behind a build tag *and* an env var.

**Tech Stack:** Go 1.25.5 (no CGO), goobs 1.8.3, obslua (Lua 5.1), modernc.org/sqlite.

**Spec:** [docs/superpowers/specs/2026-09-21-lua-bridge-design.md](../specs/2026-09-21-lua-bridge-design.md)

## Global Constraints

- **No CGO** (ADR-001). Go cannot call into Lua; the sandbox is tested over the live transport.
- **Source names:** `agentic-obs-inbox`, `agentic-obs-mailbox`.
- **Build tag:** `scripting`. **Env var:** `AGENTIC_OBS_SCRIPTING=1`. Both required for `run_lua_in_obs` to exist.
- **`go test ./...` must pass untagged, and `go test -tags scripting ./...` must also pass.**
- **Never concatenate `args` into `lua`.** Parameters travel as data; the snippet is a constant.
- **Instruction bound:** 2,000,000. **Marshalling caps:** 8 levels deep, 64 KB encoded.
- **Transport timeout:** 2s. Exactly one command outstanding at a time.
- `gofmt` and `go vet` clean. Follow existing file style in each package.

**Deviation from the spec, deliberate:** the spec says re-probe presence on `CurrentSceneCollectionChanged`. This plan uses a 5s TTL cache plus invalidate-on-failure instead. It needs no extra event translation and it also covers the case the event misses entirely — a user removing the script through the Scripts dialog, which emits nothing. Task 4 notes this.

---

### Task 1: Translate `InputSettingsChanged`

Currently untranslated, so it reaches no sink and the bridge could never hear a reply.

**Files:**
- Modify: `internal/obs/events.go` (add the event-type constant near the others)
- Modify: `internal/obs/event_translation.go` (add a case in `eventFrom`)
- Test: `internal/obs/event_translation_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `obs.EventTypeInputSettingsChanged` (`EventType = "input_settings_changed"`), payload keys `input_name` (string), `input_uuid` (string), `settings` (`map[string]interface{}`).

- [ ] **Step 1: Write the failing test**

Add to `internal/obs/event_translation_test.go`:

```go
func TestEventFromInputSettingsChanged(t *testing.T) {
	at := time.Now()
	raw := &events.InputSettingsChanged{
		InputName:     "agentic-obs-mailbox",
		InputUuid:     "uuid-1",
		InputSettings: map[string]any{"id": "abc", "ok": true},
	}

	got, ok := eventFrom(raw, at)
	if !ok {
		t.Fatal("eventFrom did not recognise InputSettingsChanged")
	}
	if got.Type != EventTypeInputSettingsChanged {
		t.Errorf("Type = %q, want %q", got.Type, EventTypeInputSettingsChanged)
	}
	if got.Payload["input_name"] != "agentic-obs-mailbox" {
		t.Errorf("input_name = %v", got.Payload["input_name"])
	}
	if got.Payload["input_uuid"] != "uuid-1" {
		t.Errorf("input_uuid = %v", got.Payload["input_uuid"])
	}

	settings, isMap := got.Payload["settings"].(map[string]interface{})
	if !isMap {
		t.Fatalf("settings is %T, want a map", got.Payload["settings"])
	}
	if settings["id"] != "abc" {
		t.Errorf("settings[id] = %v, want abc", settings["id"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/ -run TestEventFromInputSettingsChanged -v`
Expected: FAIL — `undefined: EventTypeInputSettingsChanged`.

- [ ] **Step 3: Add the constant**

In `internal/obs/events.go`, beside the other `EventType` constants:

```go
	// EventTypeInputSettingsChanged reports an input's settings being replaced,
	// including by a write made inside OBS. The Lua bridge's replies arrive
	// this way.
	EventTypeInputSettingsChanged EventType = "input_settings_changed"
```

- [ ] **Step 4: Add the translation case**

In `internal/obs/event_translation.go`, in the `switch` inside `eventFrom`, next to the other input cases:

```go
	case *events.InputSettingsChanged:
		return ev(EventTypeInputSettingsChanged, map[string]interface{}{
			"input_name": e.InputName,
			"input_uuid": e.InputUuid,
			"settings":   map[string]interface{}(e.InputSettings),
		})
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/obs/ -v -run "TestEventFrom"`
Expected: PASS, including the pre-existing translation tests.

- [ ] **Step 6: Check nothing else broke**

Run: `go test ./...`
Expected: PASS. If a test asserts an exhaustive list of translated events, update it to include the new type.

- [ ] **Step 7: Commit**

```bash
git add internal/obs/events.go internal/obs/event_translation.go internal/obs/event_translation_test.go
git commit -m "feat(bridge): translate InputSettingsChanged"
```

---

### Task 2: Composable and filtering event sinks

`Client.eventSink` is a single field. The bridge must listen alongside the automation engine, and the bridge's own traffic must not reach that engine — otherwise every reply looks like an OBS state change.

**The filter belongs here, not in `eventFrom`.** The bridge *needs* mailbox events; only the downstream consumers must not see them.

**Files:**
- Create: `internal/obs/sink.go`
- Test: `internal/obs/sink_test.go`

**Interfaces:**
- Consumes: `obs.Event`, `obs.EventSink` (Task 1's event type in tests).
- Produces:
  - `obs.MultiSink []EventSink` with `HandleEvent(Event)` — fans out in order.
  - `obs.FilterSink{Sink EventSink; Keep func(Event) bool}` with `HandleEvent(Event)`.

- [ ] **Step 1: Write the failing test**

Create `internal/obs/sink_test.go`:

```go
package obs

import "testing"

type recordingSink struct{ got []Event }

func (r *recordingSink) HandleEvent(e Event) { r.got = append(r.got, e) }

func TestMultiSinkFansOutToEverySink(t *testing.T) {
	a, b := &recordingSink{}, &recordingSink{}
	sink := MultiSink{a, b}

	sink.HandleEvent(Event{Type: EventTypeSceneCreated})

	if len(a.got) != 1 || len(b.got) != 1 {
		t.Fatalf("fan-out reached %d and %d sinks, want 1 each", len(a.got), len(b.got))
	}
}

// A nil member must not panic: sinks are wired at startup and one may be
// absent (the bridge, when it is not configured).
func TestMultiSinkSkipsNilMembers(t *testing.T) {
	a := &recordingSink{}
	sink := MultiSink{nil, a}

	sink.HandleEvent(Event{Type: EventTypeSceneCreated})

	if len(a.got) != 1 {
		t.Fatalf("a nil member stopped the fan-out: got %d", len(a.got))
	}
}

func TestFilterSinkDropsWhatKeepRejects(t *testing.T) {
	inner := &recordingSink{}
	sink := FilterSink{
		Sink: inner,
		Keep: func(e Event) bool { return e.Type != EventTypeInputSettingsChanged },
	}

	sink.HandleEvent(Event{Type: EventTypeInputSettingsChanged})
	sink.HandleEvent(Event{Type: EventTypeSceneCreated})

	if len(inner.got) != 1 {
		t.Fatalf("inner sink saw %d events, want 1", len(inner.got))
	}
	if inner.got[0].Type != EventTypeSceneCreated {
		t.Errorf("the wrong event survived: %q", inner.got[0].Type)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/ -run "TestMultiSink|TestFilterSink" -v`
Expected: FAIL — `undefined: MultiSink`.

- [ ] **Step 3: Write the implementation**

Create `internal/obs/sink.go`:

```go
package obs

// MultiSink delivers one event to several sinks in order.
//
// Client holds a single sink, and more than one thing needs the stream: the
// MCP notification path, and the Lua bridge waiting for its replies. Nil
// members are skipped so a caller can wire an optional sink without branching.
type MultiSink []EventSink

func (m MultiSink) HandleEvent(e Event) {
	for _, sink := range m {
		if sink != nil {
			sink.HandleEvent(e)
		}
	}
}

// FilterSink forwards only the events Keep accepts.
//
// It exists because the bridge's transport sources produce a settings event on
// every reply. Those are plumbing, not OBS state changes, and letting them
// reach the automation engine would feed it traffic it caused -- the problem
// ADR-010 solved for rule writes, arriving by a different road.
type FilterSink struct {
	Sink EventSink
	Keep func(Event) bool
}

func (f FilterSink) HandleEvent(e Event) {
	if f.Sink == nil || (f.Keep != nil && !f.Keep(e)) {
		return
	}
	f.Sink.HandleEvent(e)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/obs/ -run "TestMultiSink|TestFilterSink" -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/obs/sink.go internal/obs/sink_test.go
git commit -m "feat(bridge): composable and filtering event sinks"
```

---

### Task 3: The bridge transport

Framing, correlation, serialisation and timeout. No OBS required — tested against a fake caller.

**Files:**
- Create: `internal/bridge/transport.go`
- Test: `internal/bridge/transport_test.go`

**Interfaces:**
- Consumes: `obs.Event`, `obs.EventTypeInputSettingsChanged` (Task 1).
- Produces:
  - `bridge.InboxSource`, `bridge.MailboxSource` (string constants)
  - `bridge.Caller` interface: `SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error` — satisfied by `*obs.Client`
  - `bridge.Result{OK bool; Value interface{}; Err string}`
  - `bridge.New(caller Caller) *Transport`
  - `(*Transport).Run(ctx context.Context, lua string, args map[string]interface{}) (Result, error)`
  - `(*Transport).HandleEvent(obs.Event)` — implements `obs.EventSink`
  - `(*Transport).Timeout` field (`time.Duration`, default 2s) so tests can shorten it

- [ ] **Step 1: Write the failing test**

Create `internal/bridge/transport_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -v`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/bridge/transport.go`:

```go
// Package bridge talks to the Lua script loaded inside OBS.
//
// The transport is a pair of source-settings mailboxes rather than
// obs-websocket's vendor API, which ADR-013 records as closed to Lua: obslua
// cannot pass obs-websocket's own proc handler back in, because calldata_ptr
// hands it back as a bare void* that SWIG will not retype.
package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
)

const (
	// InboxSource carries commands in. MailboxSource carries answers out.
	// Two sources rather than one: sharing a single source would echo every
	// write back to whoever made it, so agentic-obs would read its own
	// commands and the script would re-run its own replies.
	InboxSource   = "agentic-obs-inbox"
	MailboxSource = "agentic-obs-mailbox"

	// DefaultTimeout is generous against a round trip measured at 30ms. It is
	// the bound on a chunk that wedges, not on normal latency.
	DefaultTimeout = 2 * time.Second
)

// Caller is the slice of the OBS client the transport needs. *obs.Client
// satisfies it.
type Caller interface {
	SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error
}

// Result is what one chunk produced. Err is the bridge's message, not a
// transport failure: a chunk that fails to compile still round-trips
// successfully and reports OK false.
type Result struct {
	OK    bool        `json:"ok"`
	Value interface{} `json:"result,omitempty"`
	Err   string      `json:"error,omitempty"`
}

// Transport runs one command at a time against the bridge.
//
// Serialising is correctness rather than caution. obs_source_update merges
// settings, so two writes landing between video ticks coalesce into a single
// "update" signal and the first command is lost with no error anywhere.
type Transport struct {
	caller  Caller
	Timeout time.Duration

	send sync.Mutex // held for a whole round trip: one command outstanding

	mu      sync.Mutex
	pending *pending
}

type pending struct {
	id   string
	done chan Result
}

func New(caller Caller) *Transport {
	return &Transport{caller: caller, Timeout: DefaultTimeout}
}

// Run ships lua with args and waits for the bridge's answer.
//
// args is data, never text spliced into lua. That separation is what stops a
// value like a scene name from becoming code.
func (t *Transport) Run(ctx context.Context, lua string, args map[string]interface{}) (Result, error) {
	t.send.Lock()
	defer t.send.Unlock()

	if args == nil {
		args = map[string]interface{}{}
	}

	id, err := newID()
	if err != nil {
		return Result{}, err
	}

	done := make(chan Result, 1)
	t.mu.Lock()
	t.pending = &pending{id: id, done: done}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.pending = nil
		t.mu.Unlock()
	}()

	// Every field goes on every write. Settings merge, so an omitted args
	// would silently inherit the previous command's.
	command := map[string]interface{}{"id": id, "lua": lua, "args": encodeArgs(args)}
	if err := t.caller.SetSourceSettings(InboxSource, command, true); err != nil {
		return Result{}, fmt.Errorf("could not reach the bridge on %q (is it loaded? run 'agentic-obs install-bridge'): %w", InboxSource, err)
	}

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-done:
		return result, nil
	case <-timer.C:
		return Result{}, fmt.Errorf("the bridge did not answer within %s", timeout)
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// HandleEvent implements obs.EventSink, picking the bridge's replies out of
// the event stream and dropping everything else.
func (t *Transport) HandleEvent(e obs.Event) {
	if e.Type != obs.EventTypeInputSettingsChanged {
		return
	}
	if name, _ := e.Payload["input_name"].(string); name != MailboxSource {
		return
	}
	settings, ok := e.Payload["settings"].(map[string]interface{})
	if !ok {
		return
	}

	t.mu.Lock()
	waiting := t.pending
	t.mu.Unlock()

	// A reply for another id is a leftover: the mailbox keeps keys from
	// earlier replies because updates merge, so an id we did not send is
	// stale rather than an error.
	if waiting == nil {
		return
	}
	if id, _ := settings["id"].(string); id != waiting.id {
		return
	}

	okFlag, _ := settings["ok"].(bool)
	errMsg, _ := settings["error"].(string)

	select {
	case waiting.done <- Result{OK: okFlag, Value: settings["result"], Err: errMsg}:
	default: // already answered
	}
}

// encodeArgs turns the caller's map into an ordered array of typed entries.
//
// It is an array because the bridge has to read it. obs_data_item_next takes
// obs_data_item_t** in C, which SWIG will not marshal from Lua, so a script
// cannot walk an object's keys at all -- but obs_data_array_count and
// obs_data_array_item are plain calls. Each entry names its value and tags the
// type, so the bridge rebuilds a Lua table without guessing.
//
// Sorted by name so a command is deterministic for a given input.
func encodeArgs(args map[string]interface{}) []interface{} {
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]interface{}, 0, len(names))
	for _, name := range names {
		entry := map[string]interface{}{"name": name}
		switch value := args[name].(type) {
		case bool:
			entry["b"] = value
		case string:
			entry["s"] = value
		case float64:
			entry["n"] = value
		case int:
			entry["n"] = float64(value)
		case int64:
			entry["n"] = float64(value)
		default:
			// Anything richer is flattened rather than dropped, so a caller
			// sees what arrived instead of a silently missing argument.
			entry["s"] = fmt.Sprintf("%v", value)
		}
		entries = append(entries, entry)
	}
	return entries
}

func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not generate a command id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/bridge/ -v -race`
Expected: PASS (7 tests). `-race` matters here: `HandleEvent` runs on the client's event goroutine while `Run` waits.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/transport.go internal/bridge/transport_test.go
git commit -m "feat(bridge): the source-settings transport"
```

---

### Task 4: Presence and version

**Files:**
- Create: `internal/bridge/presence.go`
- Test: `internal/bridge/presence_test.go`

**Interfaces:**
- Consumes: `Transport.Run` (Task 3).
- Produces:
  - `bridge.Status{Present bool; Version string; Detail string}` with JSON tags `present`, `version`, `detail`
  - `(*Transport).Probe(ctx context.Context) Status` — cached for `PresenceTTL`
  - `(*Transport).PresenceTTL` field (`time.Duration`, default 5s)
  - `(*Transport).Now` field (`func() time.Time`, default `time.Now`) — clock seam, matching the automation engine's pattern

**Note on the spec:** the spec re-probes on `CurrentSceneCollectionChanged`. A TTL cache is used instead — it needs no extra event translation and it also catches a user removing the script through the Scripts dialog, which emits no event at all.

- [ ] **Step 1: Write the failing test**

Create `internal/bridge/presence_test.go`:

```go
package bridge

import (
	"context"
	"testing"
	"time"
)

func TestProbeReportsPresentWithVersion(t *testing.T) {
	var transport *Transport
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		transport.HandleEvent(reply(s["id"].(string), true, "1", ""))
	}
	transport = New(caller)

	got := transport.Probe(context.Background())
	if !got.Present {
		t.Fatalf("Present = false, want true: %+v", got)
	}
	if got.Version != "1" {
		t.Errorf("Version = %q, want \"1\"", got.Version)
	}
}

func TestProbeReportsAbsentWhenTheWriteFails(t *testing.T) {
	transport := New(&fakeCaller{err: errTestNoSuchInput})

	got := transport.Probe(context.Background())
	if got.Present {
		t.Fatal("Present = true when the inbox does not exist")
	}
	if got.Detail == "" {
		t.Error("Detail is empty; an absent bridge must say why")
	}
}

func TestProbeIsCachedWithinTheTTL(t *testing.T) {
	var transport *Transport
	calls := 0
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		calls++
		transport.HandleEvent(reply(s["id"].(string), true, "1", ""))
	}
	transport = New(caller)

	transport.Probe(context.Background())
	transport.Probe(context.Background())

	if calls != 1 {
		t.Fatalf("probed %d times inside the TTL, want 1", calls)
	}
}

func TestProbeRefreshesAfterTheTTL(t *testing.T) {
	var transport *Transport
	calls := 0
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		calls++
		transport.HandleEvent(reply(s["id"].(string), true, "1", ""))
	}
	transport = New(caller)

	now := time.Now()
	transport.Now = func() time.Time { return now }

	transport.Probe(context.Background())
	now = now.Add(10 * time.Second)
	transport.Probe(context.Background())

	if calls != 2 {
		t.Fatalf("probed %d times across the TTL, want 2", calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestProbe -v`
Expected: FAIL — `transport.Probe undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/bridge/presence.go`:

```go
package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultPresenceTTL keeps a probe result briefly so that a tool call does not
// pay for a round trip it just made.
const DefaultPresenceTTL = 5 * time.Second

// Status describes whether the bridge is reachable right now.
//
// "Right now" is the operative part: scripts are stored per scene collection,
// so switching collections unloads the bridge with no warning, and a user can
// remove it through the Scripts dialog at any time.
type Status struct {
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// probeLua asks the bridge to name its protocol version. bridge_version is set
// by the prelude inside the sandbox, so a reply proves the whole path works --
// the write landed, the signal fired, a chunk ran, and the answer came back.
const probeLua = "return bridge_version"

type presenceCache struct {
	mu     sync.Mutex
	status Status
	at     time.Time
	valid  bool
}

// Probe reports the bridge's presence, re-running at most once per
// PresenceTTL.
//
// A TTL rather than an event subscription: a collection switch is not the only
// way the bridge disappears, and removing it from the Scripts dialog announces
// nothing at all.
func (t *Transport) Probe(ctx context.Context) Status {
	ttl := t.PresenceTTL
	if ttl <= 0 {
		ttl = DefaultPresenceTTL
	}
	now := t.now()

	t.presence.mu.Lock()
	if t.presence.valid && now.Sub(t.presence.at) < ttl {
		cached := t.presence.status
		t.presence.mu.Unlock()
		return cached
	}
	t.presence.mu.Unlock()

	status := t.probeNow(ctx)

	t.presence.mu.Lock()
	t.presence.status = status
	t.presence.at = now
	t.presence.valid = true
	t.presence.mu.Unlock()

	return status
}

// Invalidate forces the next Probe to ask OBS again. Callers use it after a
// failed command, because a failure is evidence the cached answer is stale.
func (t *Transport) Invalidate() {
	t.presence.mu.Lock()
	t.presence.valid = false
	t.presence.mu.Unlock()
}

func (t *Transport) probeNow(ctx context.Context) Status {
	result, err := t.Run(ctx, probeLua, nil)
	if err != nil {
		return Status{Present: false, Detail: err.Error()}
	}
	if !result.OK {
		return Status{Present: false, Detail: fmt.Sprintf("the bridge answered but failed: %s", result.Err)}
	}
	return Status{Present: true, Version: fmt.Sprintf("%v", result.Value)}
}

func (t *Transport) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}
```

- [ ] **Step 4: Add the fields to Transport**

In `internal/bridge/transport.go`, add to the `Transport` struct:

```go
	// PresenceTTL bounds how long a Probe result is reused. Now is a clock
	// seam so tests can move time rather than sleep through it, matching the
	// automation engine.
	PresenceTTL time.Duration
	Now         func() time.Time

	presence presenceCache
```

and set the default in `New`:

```go
func New(caller Caller) *Transport {
	return &Transport{caller: caller, Timeout: DefaultTimeout, PresenceTTL: DefaultPresenceTTL}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/bridge/ -v -race`
Expected: PASS (11 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/bridge/presence.go internal/bridge/presence_test.go internal/bridge/transport.go
git commit -m "feat(bridge): presence and version probing"
```

---

### Task 5: The bridge Lua script

The fixed asset. It changes only when the protocol changes, never to add a capability.

Go cannot test this directly — no CGO. It is verified by the live suite in Task 6, and syntax-checked here.

**Files:**
- Create: `bridge/agentic-obs-bridge.lua`

**Interfaces:**
- Consumes: the protocol from Task 3 — reads `id`, `lua`, `args` from the inbox; writes `id`, `ok`, `result`, `error` to the mailbox.
- Produces: `bridge_version` (string `"1"`) inside the sandbox, read by Task 4's probe.

- [ ] **Step 1: Write the script**

Create `bridge/agentic-obs-bridge.lua`:

```lua
--[[
agentic-obs bridge.

agentic-obs writes {id, lua, args} to the inbox source; OBS raises "update" on
it; this script runs the chunk and writes {id, ok, result} to the mailbox
source, which leaves as InputSettingsChanged. Both directions are push, and the
round trip measures about 30ms.

The vendor API would have been the obvious transport and is closed to Lua:
calldata_ptr hands obs-websocket's proc handler back as a bare void* that SWIG
will not retype. ADR-013 has the measurements.

EVERYTHING HERE RUNS ON THE VIDEO THREAD. obs_source_update defers on a video
source and the signal is raised from obs_source_video_tick, so a chunk that
blocks drops frames. That is what the instruction budget is for, and why this
script has no timers.
]]

obs = obslua

local INBOX = "agentic-obs-inbox"
local MAILBOX = "agentic-obs-mailbox"
local VERSION = "1"

-- A frame's worth of work, near enough. Not a security control: it stops a
-- runaway loop from hanging OBS, the way FB-86 stops a runaway rule.
local INSTRUCTION_BUDGET = 2000000

-- Caps on what a chunk may hand back. Exceeding one is a failure, never a
-- truncated result that reads like success.
local MAX_DEPTH = 8
local MAX_BYTES = 64 * 1024

local inbox, mailbox = nil, nil
local last_id = nil

local function log(fmt, ...)
	obs.script_log(obs.LOG_INFO, "[agentic-obs] " .. string.format(fmt, ...))
end

-- The sandbox. obslua goes in whole, because reaching OBS internals is the
-- entire point. What stays out is everything that reaches the machine or the
-- interpreter itself:
--
--   os, io            -- files and os.execute
--   package, require,
--   dofile, loadfile  -- loading more code from disk
--   load, loadstring  -- nested eval, which would undo this table
--   debug             -- debug.getregistry walks straight out of any sandbox
--   setfenv, getfenv  -- swapping the environment back
--   the raw*/…metatable family -- getmetatable("") reaches the shared string
--                     metatable, so a chunk could poison every string in the
--                     process for every other script OBS has loaded
--
-- This shrinks what a mistake can reach. It is not a boundary against an
-- attacker: Lua sandbox escapes are a known class, and anyone holding the
-- obs-websocket password could load their own script anyway.
local function make_env(args)
	return {
		obslua = obs,
		obs = obs,
		args = args,
		bridge_version = VERSION,

		string = string,
		table = table,
		math = math,

		ipairs = ipairs,
		pairs = pairs,
		next = next,
		select = select,
		tonumber = tonumber,
		tostring = tostring,
		type = type,
		unpack = unpack,
		error = error,
		assert = assert,
		pcall = pcall,
		xpcall = xpcall,
	}
end

-- to_data walks a returned Lua value into an obs_data_t.
--
-- Everything is nested under one key because obs_data has no "set arbitrary
-- value" call -- the type has to be chosen per field.
local function set_value(data, key, value, depth, budget)
	local kind = type(value)

	if kind == "nil" then
		obs.obs_data_set_string(data, key, "")
		return true
	elseif kind == "boolean" then
		obs.obs_data_set_bool(data, key, value)
		return true
	elseif kind == "number" then
		obs.obs_data_set_double(data, key, value)
		return true
	elseif kind == "string" then
		budget.bytes = budget.bytes + #value
		if budget.bytes > MAX_BYTES then
			return false, "the result exceeded " .. MAX_BYTES .. " bytes"
		end
		obs.obs_data_set_string(data, key, value)
		return true
	elseif kind == "table" then
		if depth >= MAX_DEPTH then
			return false, "the result nested deeper than " .. MAX_DEPTH .. " levels"
		end
		local child = obs.obs_data_create()
		for k, v in pairs(value) do
			local ok, err = set_value(child, tostring(k), v, depth + 1, budget)
			if not ok then
				obs.obs_data_release(child)
				return false, err
			end
		end
		obs.obs_data_set_obj(data, key, child)
		obs.obs_data_release(child)
		return true
	end

	-- functions, userdata, threads
	return false, "a " .. kind .. " cannot be sent back"
end

local function answer(id, ok, value, err)
	if mailbox == nil then
		return
	end

	local settings = obs.obs_data_create()
	obs.obs_data_set_string(settings, "id", id)
	obs.obs_data_set_bool(settings, "ok", ok)
	obs.obs_data_set_string(settings, "error", err or "")

	if ok then
		local written, cap_err = set_value(settings, "result", value, 0, { bytes = 0 })
		if not written then
			obs.obs_data_set_bool(settings, "ok", false)
			obs.obs_data_set_string(settings, "error", cap_err)
		end
	end

	obs.obs_source_update(mailbox, settings)
	obs.obs_data_release(settings)
end

-- run compiles and executes one chunk. It never raises: a bridge that dies on
-- a bad payload needs a human to reload it, which is the one repair it cannot
-- perform on itself.
local function run(source, args)
	local chunk, compile_err = loadstring(source, "agentic-obs-chunk")
	if chunk == nil then
		return false, nil, tostring(compile_err)
	end

	setfenv(chunk, make_env(args))

	-- The hook is installed from out here, where debug is still reachable.
	-- The chunk itself never sees it.
	local tripped = false
	debug.sethook(function()
		tripped = true
		error("the chunk exceeded its instruction budget", 2)
	end, "", INSTRUCTION_BUDGET)

	local ok, result = pcall(chunk)
	debug.sethook()

	if not ok then
		if tripped then
			return false, nil, "the chunk exceeded its instruction budget of " .. INSTRUCTION_BUDGET
		end
		return false, nil, tostring(result)
	end
	return true, result, nil
end

-- read_args rebuilds the args table from the command.
--
-- The wire carries an ARRAY of {name, s|n|b} rather than an object, because a
-- Lua script cannot walk an obs_data object at all: obs_data_item_next takes
-- obs_data_item_t** in C and SWIG will not marshal that. Arrays are plain --
-- obs_data_array_count and obs_data_array_item are ordinary calls.
--
-- This is also where code and data stay apart. Values arrive as values; none
-- of this is ever concatenated into the chunk's source.
local function read_args(settings)
	local out = {}
	local list = obs.obs_data_get_array(settings, "args")
	if list == nil then
		return out
	end

	local count = obs.obs_data_array_count(list)
	for i = 0, count - 1 do
		local entry = obs.obs_data_array_item(list, i)
		if entry ~= nil then
			local name = obs.obs_data_get_string(entry, "name")
			if name ~= "" then
				if obs.obs_data_has_user_value(entry, "s") then
					out[name] = obs.obs_data_get_string(entry, "s")
				elseif obs.obs_data_has_user_value(entry, "n") then
					out[name] = obs.obs_data_get_double(entry, "n")
				elseif obs.obs_data_has_user_value(entry, "b") then
					out[name] = obs.obs_data_get_bool(entry, "b")
				end
			end
			obs.obs_data_release(entry)
		end
	end

	obs.obs_data_array_release(list)
	return out
end

local function on_inbox_update(cd)
	local src = obs.calldata_source(cd, "source")
	if src == nil then
		return
	end

	local settings = obs.obs_source_get_settings(src)
	local id = obs.obs_data_get_string(settings, "id")
	local source_text = obs.obs_data_get_string(settings, "lua")
	local args = read_args(settings)
	obs.obs_data_release(settings)

	-- Creating the source raises this signal too, and OBS coalesces writes, so
	-- the id is what makes a command a command.
	if id == nil or id == "" or id == last_id then
		return
	end
	last_id = id

	local ok, value, err = run(source_text, args)
	answer(id, ok, value, err)
end

local function open_sources()
	local blank = obs.obs_data_create()
	mailbox = obs.obs_source_create("color_source_v3", MAILBOX, blank, nil)
	inbox = obs.obs_source_create("color_source_v3", INBOX, blank, nil)
	obs.obs_data_release(blank)

	if inbox == nil or mailbox == nil then
		log("FAILED to create the transport sources; the bridge is not running")
		return false
	end

	obs.signal_handler_connect(obs.obs_source_get_signal_handler(inbox), "update", on_inbox_update)
	return true
end

function script_description()
	return [[<h2>agentic-obs bridge</h2>
<p>Lets agentic-obs run code inside OBS. It creates two sources,
<code>agentic-obs-inbox</code> and <code>agentic-obs-mailbox</code>, which
belong to no scene and render nowhere. <b>Deleting them stops the bridge
working.</b></p>
<p>Installed and updated with <code>agentic-obs install-bridge</code>.</p>]]
end

function script_load(settings)
	if open_sources() then
		log("bridge %s up: commands on %q, answers on %q", VERSION, INBOX, MAILBOX)
	end
end

function script_unload()
	if inbox ~= nil then
		obs.obs_source_release(inbox)
		inbox = nil
	end
	if mailbox ~= nil then
		obs.obs_source_release(mailbox)
		mailbox = nil
	end
end
```

- [ ] **Step 2: Syntax-check it against OBS's own Lua 5.1**

No Lua CLI ships with OBS, but `lua51.dll` does. Create `scripts/check-lua.py`:

```python
"""Syntax-check Lua files against the Lua 5.1 that OBS itself runs.

No Lua interpreter is installed, and CGO is out (ADR-001), so a Go test cannot
parse these. OBS ships lua51.dll, which is the exact parser that matters.
"""
import ctypes
import glob
import sys

DLL_CANDIDATES = [
    "C:/Program Files/obs-studio/bin/64bit/lua51.dll",
    "/usr/lib/x86_64-linux-gnu/liblua5.1.so.0",
]


def load_lua():
    for path in DLL_CANDIDATES:
        try:
            return ctypes.CDLL(path)
        except OSError:
            continue
    print("SKIP: no Lua 5.1 library found; install OBS to enable this check")
    sys.exit(0)


lua = load_lua()
lua.luaL_newstate.restype = ctypes.c_void_p
lua.luaL_loadbuffer.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_size_t, ctypes.c_char_p]
lua.luaL_loadbuffer.restype = ctypes.c_int
lua.lua_tolstring.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_void_p]
lua.lua_tolstring.restype = ctypes.c_char_p

state = lua.luaL_newstate()
failed = False

for path in sorted(glob.glob("bridge/**/*.lua", recursive=True) + glob.glob("internal/bridge/snippets/*.lua")):
    with open(path, "rb") as handle:
        source = handle.read()
    if lua.luaL_loadbuffer(state, source, len(source), path.encode()) == 0:
        print(f"ok   {path}")
    else:
        failed = True
        print(f"FAIL {path}: {lua.lua_tolstring(state, -1, None).decode(errors='replace')}")

sys.exit(1 if failed else 0)
```

Run: `python scripts/check-lua.py`
Expected: `ok   bridge/agentic-obs-bridge.lua`

- [ ] **Step 3: Commit**

```bash
git add bridge/agentic-obs-bridge.lua scripts/check-lua.py
git commit -m "feat(bridge): the in-OBS bridge script"
```

---

### Task 6: Wire the bridge into the server, and prove it live

**Files:**
- Modify: `internal/mcp/server.go` (compose sinks, build the transport)
- Modify: `internal/mcp/tools.go:1425-1437` (`handleGetOBSStatus`)
- Create: `internal/bridge/live_test.go` (`//go:build obslive`)
- Modify: `Makefile` (add `./internal/bridge/...` to `test-live`)

**Interfaces:**
- Consumes: `bridge.New`, `(*Transport).HandleEvent`, `(*Transport).Probe`, `obs.MultiSink`, `obs.FilterSink`.
- Produces: `Server.bridge *bridge.Transport`; `get_obs_status` result gains `bridge` (a `bridge.Status`).

- [ ] **Step 1: Write the failing live test**

Create `internal/bridge/live_test.go`:

```go
//go:build obslive

// The bridge's contract, against a real OBS with the script loaded.
//
// The sandbox tests are the reason this file exists. CGO is out (ADR-001), so
// Go cannot reach the Lua directly -- and a sandbox asserted against a mock
// proves nothing about Lua 5.1. Shipping "return os == nil" over the real
// transport tests the real interpreter.
package bridge_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/bridge"
	"github.com/ironystock/agentic-obs/internal/obs"
)

func liveTransport(t *testing.T) *bridge.Transport {
	t.Helper()
	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1")
	}

	client := obs.NewClient(obs.ConnectionConfig{
		Host:     envOr("OBS_HOST", "localhost"),
		Port:     envOr("OBS_PORT", "4455"),
		Password: os.Getenv("OBS_PASSWORD"),
	})

	transport := bridge.New(client)
	client.SetEventSink(transport)

	if err := client.Connect(); err != nil {
		t.Fatalf("could not reach OBS: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect() })

	if status := transport.Probe(context.Background()); !status.Present {
		t.Skipf("the bridge is not loaded (%s). Run 'agentic-obs install-bridge' and restart OBS.", status.Detail)
	}
	return transport
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestLiveBridgeRunsCode(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "return 6 * 7", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.OK {
		t.Fatalf("the chunk failed: %s", got.Err)
	}
	if got.Value != 42.0 {
		t.Errorf("result = %#v, want 42", got.Value)
	}
}

func TestLiveBridgeReceivesArgsAsDataNotCode(t *testing.T) {
	transport := liveTransport(t)

	// A value that would be code if it were spliced into the source. It must
	// come back as the string it is.
	hostile := `]] .. os.execute("echo pwned") .. [[`
	got, err := transport.Run(context.Background(),
		"return args.value", map[string]interface{}{"value": hostile})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.OK {
		t.Fatalf("the chunk failed: %s", got.Err)
	}
	if got.Value != hostile {
		t.Errorf("args round-tripped as %#v, want the literal string", got.Value)
	}
}

// The sandbox, asserted in the interpreter that enforces it.
func TestLiveBridgeSandboxWithholdsTheMachine(t *testing.T) {
	transport := liveTransport(t)

	for _, name := range []string{"os", "io", "package", "require", "dofile", "loadfile", "loadstring", "load", "debug", "setfenv", "getfenv", "getmetatable", "setmetatable", "rawset", "rawget"} {
		t.Run(name, func(t *testing.T) {
			got, err := transport.Run(context.Background(), "return "+name+" == nil", nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !got.OK {
				t.Fatalf("the chunk failed: %s", got.Err)
			}
			if got.Value != true {
				t.Errorf("%s is reachable from inside the sandbox", name)
			}
		})
	}
}

func TestLiveBridgeStillReachesOBS(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "return type(obslua.obs_get_version_string)", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Value != "function" {
		t.Fatalf("obslua is not reachable: %#v (%s)", got.Value, got.Err)
	}
}

// A runaway loop must come back as a failure rather than hanging OBS. If this
// test times out instead of failing, the budget is not being enforced and the
// render thread is wedged.
func TestLiveBridgeBoundsRunawayChunks(t *testing.T) {
	transport := liveTransport(t)
	transport.Timeout = 20 * time.Second

	got, err := transport.Run(context.Background(), "while true do end", nil)
	if err != nil {
		t.Fatalf("Run: %v -- the budget did not stop the loop", err)
	}
	if got.OK {
		t.Fatal("an infinite loop reported success")
	}
	if !strings.Contains(got.Err, "budget") {
		t.Errorf("error = %q, want it to name the budget", got.Err)
	}
}

func TestLiveBridgeReportsCompileErrors(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "this is not lua", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.OK {
		t.Fatal("invalid Lua reported success")
	}
	if got.Err == "" {
		t.Error("a compile failure came back with no message")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `OBS_LIVE_TEST=1 OBS_PASSWORD=... go test -tags obslive ./internal/bridge/ -v`
Expected: SKIP with "the bridge is not loaded" — the script is not installed yet. That skip is the correct failure for now; Task 8 installs it.

To run these before Task 8 exists, add the script manually through **Tools → Scripts** once, then re-run. Expected then: PASS for all six.

- [ ] **Step 3: Compose the sinks in the server**

In `internal/mcp/server.go`, replace the single-sink wiring at line 213:

```go
	// The bridge needs the event stream for its replies, and so does the MCP
	// notification path. The bridge's own traffic is filtered out of the
	// latter: a reply is plumbing, not an OBS state change, and feeding it to
	// the automation engine would be ADR-010's echo problem by another road.
	eventHandler := obs.NewEventHandler(s.handleOBSEventNotification)
	s.bridge = bridge.New(obsClient)

	obsClient.SetEventSink(obs.MultiSink{
		s.bridge,
		obs.FilterSink{Sink: eventHandler, Keep: notBridgeTraffic},
	})
```

and add, near the other helpers in the same file:

```go
// notBridgeTraffic rejects settings events for the bridge's two transport
// sources.
func notBridgeTraffic(e obs.Event) bool {
	if e.Type != obs.EventTypeInputSettingsChanged {
		return true
	}
	name, _ := e.Payload["input_name"].(string)
	return name != bridge.InboxSource && name != bridge.MailboxSource
}
```

Add the `bridge *bridge.Transport` field to the `Server` struct and the import.

- [ ] **Step 4: Add the bridge field to get_obs_status**

In `internal/mcp/tools.go`, replace the body of `handleGetOBSStatus`:

```go
func (s *Server) handleGetOBSStatus(ctx context.Context, request *mcpsdk.CallToolRequest, input struct{}) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()
	log.Println("Getting OBS status")

	status, err := s.obsClient.GetOBSStatus()
	if err != nil {
		s.recordAction("get_obs_status", "Get OBS status", nil, nil, false, time.Since(start))
		return nil, nil, fmt.Errorf("failed to get OBS status: %w", err)
	}

	// The bridge is reported rather than assumed: scripts live per scene
	// collection, so switching collections unloads it with no warning.
	result := struct {
		*obs.OBSStatus
		Bridge bridge.Status `json:"bridge"`
	}{OBSStatus: status, Bridge: s.bridge.Probe(ctx)}

	s.recordAction("get_obs_status", "Get OBS status", nil, result, true, time.Since(start))
	return nil, result, nil
}
```

- [ ] **Step 5: Add the bridge package to the live make target**

In `Makefile`, change the `test-live` recipe:

```make
test-live:
	$(GOTEST) -tags obslive -v -p 1 ./internal/obs/... ./internal/scenespec/... ./internal/bridge/...
```

- [ ] **Step 6: Run everything**

Run: `go test ./... && go vet ./...`
Expected: PASS.

Run: `OBS_LIVE_TEST=1 OBS_PASSWORD=... go test -tags obslive ./internal/bridge/ -v`
Expected: PASS (6 tests) with the script loaded manually.

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools.go internal/bridge/live_test.go Makefile
git commit -m "feat(bridge): wire the transport into the server and prove it live"
```

---

### Task 7: Scene collection JSON editing

The riskiest code in the plan — it rewrites the file holding the user's entire scene setup.

**Files:**
- Create: `internal/install/collections.go`
- Test: `internal/install/collections_test.go`
- Create: `internal/install/testdata/collection.json`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `install.Backup(path string) (backupPath string, err error)`
  - `install.AddScript(collectionPath, scriptPath string) (changed bool, err error)`
  - `install.RemoveScript(collectionPath, scriptPath string) (changed bool, err error)`

- [ ] **Step 1: Create the fixture**

Create `internal/install/testdata/collection.json`. The large integer is the point: scene-item IDs are int64, and a decode into float64 mangles anything past 2^53.

```json
{
  "name": "Fixture",
  "current_scene": "Game",
  "sources": [
    { "name": "cam", "id": 9007199254740993, "settings": { "width": 1920 } }
  ],
  "modules": {
    "scripts-tool": [
      { "path": "C:/existing/other.lua", "settings": {} }
    ]
  }
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/install/collections_test.go`:

```go
package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixture gives each test its own file to mangle.
func copyFixture(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "collection.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestAddScriptAppendsWithoutDisturbingExistingEntries(t *testing.T) {
	path := copyFixture(t)

	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("AddScript: %v", err)
	}
	if !changed {
		t.Fatal("changed = false on a collection without the script")
	}

	scripts := readScripts(t, path)
	if len(scripts) != 2 {
		t.Fatalf("got %d scripts, want 2", len(scripts))
	}
	if scripts[0] != "C:/existing/other.lua" {
		t.Errorf("the existing script moved or changed: %q", scripts[0])
	}
	if scripts[1] != "C:/agentic-obs/bridge.lua" {
		t.Errorf("scripts[1] = %q", scripts[1])
	}
}

func TestAddScriptIsIdempotent(t *testing.T) {
	path := copyFixture(t)

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("first AddScript: %v", err)
	}
	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("second AddScript: %v", err)
	}
	if changed {
		t.Error("changed = true on the second add; upgrades re-run this")
	}
	if got := len(readScripts(t, path)); got != 2 {
		t.Errorf("got %d scripts after adding twice, want 2", got)
	}
}

// The test this file exists for. A naive decode into float64 turns
// 9007199254740993 into 9007199254740992 and silently corrupts a scene.
func TestAddScriptPreservesLargeIntegers(t *testing.T) {
	path := copyFixture(t)

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(written), "9007199254740993") {
		t.Fatalf("the scene-item id was mangled; file now reads:\n%s", written)
	}
}

func TestAddScriptLeavesUnrelatedKeysAlone(t *testing.T) {
	path := copyFixture(t)

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}

	var got map[string]interface{}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the file is no longer valid JSON: %v", err)
	}
	if got["current_scene"] != "Game" {
		t.Errorf("current_scene = %v", got["current_scene"])
	}
	if got["name"] != "Fixture" {
		t.Errorf("name = %v", got["name"])
	}
}

func TestAddScriptCreatesModulesWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bare.json")
	if err := os.WriteFile(path, []byte(`{"name":"Bare"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}
	if got := readScripts(t, path); len(got) != 1 {
		t.Fatalf("got %d scripts, want 1", len(got))
	}
}

func TestRemoveScriptTakesOnlyOurs(t *testing.T) {
	path := copyFixture(t)
	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}

	changed, err := RemoveScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("RemoveScript: %v", err)
	}
	if !changed {
		t.Error("changed = false when the script was present")
	}

	scripts := readScripts(t, path)
	if len(scripts) != 1 || scripts[0] != "C:/existing/other.lua" {
		t.Fatalf("remove took the wrong entry: %v", scripts)
	}
}

func TestBackupCopiesTheFile(t *testing.T) {
	path := copyFixture(t)

	backup, err := Backup(path)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	original, _ := os.ReadFile(path)
	saved, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(original) != string(saved) {
		t.Error("the backup does not match the original")
	}
}

func readScripts(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Modules struct {
			Scripts []struct {
				Path string `json:"path"`
			} `json:"scripts-tool"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := make([]string, 0, len(doc.Modules.Scripts))
	for _, s := range doc.Modules.Scripts {
		out = append(out, s.Path)
	}
	return out
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/install/ -v`
Expected: FAIL — the package does not exist.

- [ ] **Step 4: Write the implementation**

Create `internal/install/collections.go`:

```go
// Package install puts the bridge script where OBS will load it.
//
// OBS stores the script list per scene collection, in the same JSON file that
// holds every scene and source the user owns. That is why this package backs
// up before it writes and refuses to run while OBS is open: OBS rewrites the
// collection on save and would discard the edit, or worse, race it.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// scriptsKey is where the frontend-tools plugin keeps the list.
const scriptsKey = "scripts-tool"

// readCollection parses a collection with numbers left as json.Number.
//
// Decoding into interface{} would turn every number into a float64, and
// scene-item IDs are int64: anything past 2^53 would come back a different
// number and be written back corrupted. UseNumber keeps the original text.
func readCollection(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var doc map[string]interface{}
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return doc, nil
}

func writeCollection(path string, doc map[string]interface{}) error {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "    ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("could not encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}

// scriptList returns the existing entries, and whether modules existed.
func scriptList(doc map[string]interface{}) []interface{} {
	modules, _ := doc["modules"].(map[string]interface{})
	if modules == nil {
		return nil
	}
	list, _ := modules[scriptsKey].([]interface{})
	return list
}

func setScriptList(doc map[string]interface{}, list []interface{}) {
	modules, _ := doc["modules"].(map[string]interface{})
	if modules == nil {
		modules = map[string]interface{}{}
		doc["modules"] = modules
	}
	modules[scriptsKey] = list
}

func entryPath(entry interface{}) string {
	obj, _ := entry.(map[string]interface{})
	if obj == nil {
		return ""
	}
	path, _ := obj["path"].(string)
	return path
}

// Backup copies path beside itself with a timestamp, and returns the copy's
// name.
func Backup(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not read %s to back it up: %w", path, err)
	}

	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return "", fmt.Errorf("could not write the backup %s: %w", backup, err)
	}
	return backup, nil
}

// AddScript registers scriptPath with the collection, reporting whether
// anything changed. Adding a script already present is a no-op, so upgrades
// can re-run it.
func AddScript(collectionPath, scriptPath string) (bool, error) {
	doc, err := readCollection(collectionPath)
	if err != nil {
		return false, err
	}

	list := scriptList(doc)
	for _, entry := range list {
		if entryPath(entry) == scriptPath {
			return false, nil
		}
	}

	list = append(list, map[string]interface{}{
		"path":     scriptPath,
		"settings": map[string]interface{}{},
	})
	setScriptList(doc, list)

	if err := writeCollection(collectionPath, doc); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveScript drops scriptPath from the collection, leaving every other entry
// alone.
func RemoveScript(collectionPath, scriptPath string) (bool, error) {
	doc, err := readCollection(collectionPath)
	if err != nil {
		return false, err
	}

	list := scriptList(doc)
	kept := make([]interface{}, 0, len(list))
	removed := false
	for _, entry := range list {
		if entryPath(entry) == scriptPath {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return false, nil
	}

	setScriptList(doc, kept)
	if err := writeCollection(collectionPath, doc); err != nil {
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/install/ -v`
Expected: PASS (7 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/install/
git commit -m "feat(bridge): scene collection script registration"
```

---

### Task 8: The install-bridge CLI

**Files:**
- Create: `internal/install/install.go`
- Test: `internal/install/install_test.go`
- Modify: `main.go` (subcommand dispatch)
- Modify: `README.md` (an Install the bridge section)

**Interfaces:**
- Consumes: `install.AddScript`, `install.RemoveScript`, `install.Backup` (Task 7); the embedded script from Task 5.
- Produces:
  - `install.CollectionsDir() (string, error)` — OBS's scenes directory for this OS
  - `install.ScriptDir() (string, error)` — where the bridge `.lua` is written
  - `install.Run(opts Options) (Report, error)` with `Options{Collection string; All bool; DryRun bool; Remove bool}` and `Report{ScriptPath string; Changed []string; Skipped []string; Backups []string}`
  - `install.OBSIsRunning() bool`

- [ ] **Step 1: Write the failing test**

Create `internal/install/install_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteScriptPutsTheAssetOnDisk(t *testing.T) {
	dir := t.TempDir()

	path, err := writeScript(dir)
	if err != nil {
		t.Fatalf("writeScript: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, want a file in %s", path, dir)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), "agentic-obs-inbox") {
		t.Error("the written script is not the bridge")
	}
}

func TestWriteScriptOverwritesAnOlderCopy(t *testing.T) {
	dir := t.TempDir()

	path, err := writeScript(dir)
	if err != nil {
		t.Fatalf("first writeScript: %v", err)
	}
	if err := os.WriteFile(path, []byte("-- stale"), 0o644); err != nil {
		t.Fatalf("stale write: %v", err)
	}

	if _, err := writeScript(dir); err != nil {
		t.Fatalf("second writeScript: %v", err)
	}

	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "stale") {
		t.Error("an upgrade left the old script in place")
	}
}

// A dry run must not touch anything -- it is what a cautious user reaches for
// before letting this near their scene collections.
func TestDryRunChangesNothing(t *testing.T) {
	scenes := t.TempDir()
	collection := filepath.Join(scenes, "Test.json")
	original := `{"name":"Test"}`
	if err := os.WriteFile(collection, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report, err := installInto(scenes, t.TempDir(), Options{All: true, DryRun: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Changed) != 1 {
		t.Fatalf("report listed %d changes, want 1", len(report.Changed))
	}

	after, _ := os.ReadFile(collection)
	if string(after) != original {
		t.Errorf("a dry run modified the collection:\n%s", after)
	}
	if len(report.Backups) != 0 {
		t.Errorf("a dry run wrote %d backups", len(report.Backups))
	}
}

func TestInstallBacksUpEveryCollectionItChanges(t *testing.T) {
	scenes := t.TempDir()
	for _, name := range []string{"One.json", "Two.json"} {
		if err := os.WriteFile(filepath.Join(scenes, name), []byte(`{"name":"x"}`), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	report, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Backups) != 2 {
		t.Fatalf("made %d backups for 2 collections", len(report.Backups))
	}
	for _, backup := range report.Backups {
		if _, err := os.Stat(backup); err != nil {
			t.Errorf("backup %s is missing: %v", backup, err)
		}
	}
}

func TestInstallSkipsNonCollectionFiles(t *testing.T) {
	scenes := t.TempDir()
	if err := os.WriteFile(filepath.Join(scenes, "Real.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenes, "Real.json.bak-20260101-000000"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	report, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Changed) != 1 {
		t.Fatalf("changed %d files, want 1 -- backups must not be treated as collections", len(report.Changed))
	}
}

func TestRemoveTakesTheScriptBackOut(t *testing.T) {
	scenes := t.TempDir()
	scriptDir := t.TempDir()
	collection := filepath.Join(scenes, "One.json")
	if err := os.WriteFile(collection, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := installInto(scenes, scriptDir, Options{All: true}); err != nil {
		t.Fatalf("install: %v", err)
	}
	report, err := installInto(scenes, scriptDir, Options{All: true, Remove: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(report.Changed) != 1 {
		t.Fatalf("removed from %d collections, want 1", len(report.Changed))
	}

	body, _ := os.ReadFile(collection)
	if strings.Contains(string(body), "agentic-obs-bridge.lua") {
		t.Error("the script is still registered after a remove")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/install/ -run "TestWriteScript|TestDryRun|TestInstall|TestRemoveTakes" -v`
Expected: FAIL — `undefined: writeScript`.

- [ ] **Step 3: Write the implementation**

Create `internal/install/install.go`:

```go
package install

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

//go:embed all:assets
var assets embed.FS

// scriptName is the file written to disk and registered with OBS.
const scriptName = "agentic-obs-bridge.lua"

// Options controls one install or uninstall run.
type Options struct {
	// Collection names a single scene collection (without .json). Empty with
	// All false means the caller must choose.
	Collection string
	All        bool
	DryRun     bool
	Remove     bool
}

// Report says what happened, so the command can print it and a test can assert
// it.
type Report struct {
	ScriptPath string
	Changed    []string
	Skipped    []string
	Backups    []string
}

// CollectionsDir is where OBS keeps scene collections.
func CollectionsDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set, so OBS's config cannot be located")
		}
		return filepath.Join(appData, "obs-studio", "basic", "scenes"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "obs-studio", "basic", "scenes"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return filepath.Join(config, "obs-studio", "basic", "scenes"), nil
	}
}

// ScriptDir is where the bridge script itself is written. It is deliberately
// not inside OBS's config: OBS rewrites that tree, and this file belongs to
// agentic-obs.
func ScriptDir() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "agentic-obs", "bridge"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "agentic-obs", "bridge"), nil
}

// Run performs an install or uninstall against the real OBS config.
func Run(opts Options) (Report, error) {
	scenes, err := CollectionsDir()
	if err != nil {
		return Report{}, err
	}
	scriptDir, err := ScriptDir()
	if err != nil {
		return Report{}, err
	}
	return installInto(scenes, scriptDir, opts)
}

// writeScript drops the embedded bridge into dir, replacing any older copy so
// that an upgrade is just a re-run.
func writeScript(dir string) (string, error) {
	body, err := assets.ReadFile("assets/" + scriptName)
	if err != nil {
		return "", fmt.Errorf("the bridge script is missing from this build: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create %s: %w", dir, err)
	}

	path := filepath.Join(dir, scriptName)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	return path, nil
}

// installInto is Run with its directories injected, which is what the tests
// drive.
func installInto(scenesDir, scriptDir string, opts Options) (Report, error) {
	report := Report{}

	scriptPath := filepath.Join(scriptDir, scriptName)
	if !opts.Remove {
		written, err := writeScript(scriptDir)
		if err != nil {
			return report, err
		}
		scriptPath = written
	}
	report.ScriptPath = scriptPath

	collections, err := findCollections(scenesDir, opts)
	if err != nil {
		return report, err
	}

	for _, path := range collections {
		// A dry run still reports what it would do, by asking on a copy.
		if opts.DryRun {
			would, err := wouldChange(path, scriptPath, opts.Remove)
			if err != nil {
				return report, err
			}
			if would {
				report.Changed = append(report.Changed, path)
			} else {
				report.Skipped = append(report.Skipped, path)
			}
			continue
		}

		backup, err := Backup(path)
		if err != nil {
			return report, err
		}

		var changed bool
		if opts.Remove {
			changed, err = RemoveScript(path, scriptPath)
		} else {
			changed, err = AddScript(path, scriptPath)
		}
		if err != nil {
			return report, err
		}

		if changed {
			report.Changed = append(report.Changed, path)
			report.Backups = append(report.Backups, backup)
		} else {
			report.Skipped = append(report.Skipped, path)
			_ = os.Remove(backup) // nothing changed, so the backup is litter
		}
	}

	return report, nil
}

// wouldChange answers the dry run by doing the edit on a temporary copy.
// Predicting it separately would mean two implementations that can disagree.
func wouldChange(collectionPath, scriptPath string, remove bool) (bool, error) {
	raw, err := os.ReadFile(collectionPath)
	if err != nil {
		return false, err
	}
	temp, err := os.CreateTemp("", "agentic-obs-dryrun-*.json")
	if err != nil {
		return false, err
	}
	defer os.Remove(temp.Name())

	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return false, err
	}
	temp.Close()

	if remove {
		return RemoveScript(temp.Name(), scriptPath)
	}
	return AddScript(temp.Name(), scriptPath)
}

// findCollections lists the scene collection files to operate on. Backups this
// tool made end in .bak-<timestamp>, so matching *.json exactly keeps them out.
func findCollections(scenesDir string, opts Options) ([]string, error) {
	if opts.Collection != "" {
		path := filepath.Join(scenesDir, opts.Collection+".json")
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("no scene collection named %q in %s", opts.Collection, scenesDir)
		}
		return []string{path}, nil
	}
	if !opts.All {
		return nil, fmt.Errorf("choose a collection with --collection NAME, or pass --all")
	}

	entries, err := os.ReadDir(scenesDir)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", scenesDir, err)
	}

	var found []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		found = append(found, filepath.Join(scenesDir, entry.Name()))
	}
	sort.Strings(found)

	if len(found) == 0 {
		return nil, fmt.Errorf("no scene collections found in %s", scenesDir)
	}
	return found, nil
}
```

- [ ] **Step 4: Put the script where go:embed can reach it**

`go:embed` cannot climb out of its own directory, so the asset is copied into the package at build time by the Makefile rather than duplicated by hand.

Create `internal/install/assets/.gitignore`:

```gitignore
*.lua
```

Add to `Makefile`, and make `build` and `test` depend on it:

```make
## sync-bridge: copy the bridge script into the install package for go:embed
.PHONY: sync-bridge
sync-bridge:
	@mkdir -p internal/install/assets
	@cp bridge/agentic-obs-bridge.lua internal/install/assets/agentic-obs-bridge.lua
```

Run it now: `make sync-bridge`

- [ ] **Step 5: Run tests to verify they pass**

Run: `make sync-bridge && go test ./internal/install/ -v`
Expected: PASS (13 tests).

- [ ] **Step 6: Add the OBS-running guard**

Append to `internal/install/install.go`:

```go
// OBSIsRunning reports whether an OBS process is up.
//
// Installing while OBS runs is pointless and dangerous: OBS holds the
// collection in memory and rewrites it on save, so it would either discard the
// edit or race the write.
func OBSIsRunning() bool {
	names := map[string]bool{"obs64.exe": true, "obs.exe": true, "obs": true}

	entries, err := os.ReadDir("/proc")
	if err == nil { // Linux
		for _, entry := range entries {
			comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
			if err == nil && names[strings.TrimSpace(string(comm))] {
				return true
			}
		}
		return false
	}

	return obsIsRunningFallback(names)
}
```

Create `internal/install/process_windows.go`:

```go
//go:build windows

package install

import (
	"os/exec"
	"strings"
)

// obsIsRunningFallback asks Windows for the task list. tasklist is present on
// every supported Windows and needs no elevation.
func obsIsRunningFallback(names map[string]bool) bool {
	out, err := exec.Command("tasklist", "/fo", "csv", "/nh").Output()
	if err != nil {
		return false // cannot tell; the command warns instead of blocking
	}
	lower := strings.ToLower(string(out))
	for name := range names {
		if strings.Contains(lower, strings.ToLower(name)) {
			return true
		}
	}
	return false
}
```

Create `internal/install/process_other.go`:

```go
//go:build !windows

package install

import (
	"os/exec"
	"strings"
)

func obsIsRunningFallback(names map[string]bool) bool {
	out, err := exec.Command("ps", "-A", "-o", "comm=").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if names[strings.TrimSpace(line)] {
			return true
		}
	}
	return false
}
```

- [ ] **Step 7: Wire the subcommands into main.go**

In `main.go`, before the existing flag parsing, dispatch on `os.Args[1]`:

```go
	// Subcommands are checked before flags so that `agentic-obs install-bridge`
	// does not fall through to the server's flag set.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install-bridge":
			os.Exit(runBridgeInstall(os.Args[2:], false))
		case "uninstall-bridge":
			os.Exit(runBridgeInstall(os.Args[2:], true))
		}
	}
```

and add:

```go
// runBridgeInstall implements install-bridge and uninstall-bridge, returning a
// process exit code.
func runBridgeInstall(args []string, remove bool) int {
	name := "install-bridge"
	if remove {
		name = "uninstall-bridge"
	}

	flags := flag.NewFlagSet(name, flag.ExitOnError)
	collection := flags.String("collection", "", "a single scene collection by name; omit with --all")
	all := flags.Bool("all", false, "every scene collection")
	dryRun := flags.Bool("dry-run", false, "report what would change, write nothing")
	force := flags.Bool("force", false, "proceed even if OBS appears to be running")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// OBS rewrites the scene collection when it saves, so an edit made now
	// would be discarded or raced.
	if install.OBSIsRunning() && !*force && !*dryRun {
		fmt.Fprintln(os.Stderr, "OBS is running. Close it first, or pass --force if you are certain it is not.")
		return 1
	}

	report, err := install.Run(install.Options{
		Collection: *collection,
		All:        *all,
		DryRun:     *dryRun,
		Remove:     remove,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}

	if *dryRun {
		fmt.Println("Dry run. Nothing was written.")
	}
	if !remove {
		fmt.Printf("Bridge script: %s\n", report.ScriptPath)
	}
	for _, path := range report.Changed {
		fmt.Printf("  changed  %s\n", path)
	}
	for _, path := range report.Skipped {
		fmt.Printf("  no change %s\n", path)
	}
	if len(report.Changed) > 0 && !*dryRun {
		fmt.Println("\nStart OBS (or switch scene collections) to load the bridge.")
	}
	return 0
}
```

Add `"flag"`, `"fmt"`, `"os"` and the `install` package to the imports if not already present.

- [ ] **Step 8: Verify end to end**

Run: `go build -o agentic-obs . && ./agentic-obs install-bridge --all --dry-run`
Expected: it lists your scene collections and writes nothing.

Then close OBS, run `./agentic-obs install-bridge --all`, start OBS, and run:

`OBS_LIVE_TEST=1 OBS_PASSWORD=... go test -tags obslive ./internal/bridge/ -v`
Expected: PASS (6 tests), now without a manual script install.

- [ ] **Step 9: Document it**

Add to `README.md`, after the installation section:

```markdown
### Install the bridge

Some capabilities need code running inside OBS. Close OBS, then:

    agentic-obs install-bridge --all

This writes the bridge script and registers it with every scene collection,
backing each one up first. Scene collections store their own script list, so a
collection installed later needs another run. `--dry-run` shows what would
change; `uninstall-bridge` takes it back out.

`get_obs_status` reports whether the bridge is currently loaded.
```

- [ ] **Step 10: Commit**

```bash
git add internal/install/ main.go Makefile README.md
git commit -m "feat(bridge): install-bridge and uninstall-bridge"
```

---

### Task 9: The scripting channel tool

Build-tagged and env-gated, so the default build cannot contain it.

**Files:**
- Create: `internal/mcp/scripting_channel.go` (`//go:build scripting`)
- Create: `internal/mcp/scripting_channel_stub.go` (`//go:build !scripting`)
- Create: `internal/mcp/scripting_channel_test.go` (`//go:build scripting`)
- Modify: `internal/mcp/server.go` (call `registerScriptingChannel`)
- Modify: `docs/TOOLS.md`

**Interfaces:**
- Consumes: `Server.bridge` (Task 6), `ElicitConfirmation` (`internal/mcp/elicitation.go:22`).
- Produces: `registerScriptingChannel(s *Server)`, defined once per build tag.

- [ ] **Step 1: Write the stub**

Create `internal/mcp/scripting_channel_stub.go`:

```go
//go:build !scripting

package mcp

// registerScriptingChannel does nothing in the default build.
//
// run_lua_in_obs runs arbitrary code inside the OBS process, so it is compiled
// out rather than merely switched off. A tool group would not do: set_tool_config
// is a Meta tool that is always enabled and can turn any group on, so a group
// the model can see is a group the model can enable. Build with
// `-tags scripting` and set AGENTIC_OBS_SCRIPTING=1 to include it.
func registerScriptingChannel(s *Server) {}

// scriptingToolNames is empty here: the default build serves no scripting
// tools.
func scriptingToolNames() []string { return nil }
```

- [ ] **Step 2: Write the failing test**

Create `internal/mcp/scripting_channel_test.go`:

```go
//go:build scripting

package mcp

import (
	"testing"
)

func TestScriptingChannelStaysOffWithoutTheEnvVar(t *testing.T) {
	t.Setenv("AGENTIC_OBS_SCRIPTING", "")

	if scriptingEnabled() {
		t.Fatal("the scripting channel reported enabled with the variable unset")
	}
}

func TestScriptingChannelTurnsOnWithTheEnvVar(t *testing.T) {
	t.Setenv("AGENTIC_OBS_SCRIPTING", "1")

	if !scriptingEnabled() {
		t.Fatal("the scripting channel stayed off with AGENTIC_OBS_SCRIPTING=1")
	}
}

// Anything other than the documented value is treated as off. A half-set
// variable must not open this.
func TestScriptingChannelIgnoresOtherValues(t *testing.T) {
	for _, value := range []string{"0", "false", "yes", "true", " "} {
		t.Setenv("AGENTIC_OBS_SCRIPTING", value)
		if scriptingEnabled() {
			t.Errorf("AGENTIC_OBS_SCRIPTING=%q enabled the channel", value)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test -tags scripting ./internal/mcp/ -run TestScriptingChannel -v`
Expected: FAIL — `undefined: scriptingEnabled`.

- [ ] **Step 4: Write the implementation**

Create `internal/mcp/scripting_channel.go`:

```go
//go:build scripting

package mcp

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// scriptingEnabled reports whether the operator opted in.
//
// The value must be exactly "1". Anything else is off, because a variable left
// half-set should not open arbitrary code execution. It is an environment
// variable rather than stored configuration on purpose: tool configuration
// lives in SQLite and set_tool_config can write it, so a stored flag would be
// a gate the model could open for itself.
func scriptingEnabled() bool {
	return os.Getenv("AGENTIC_OBS_SCRIPTING") == "1"
}

// RunLuaInput is the scripting channel's input.
type RunLuaInput struct {
	Lua  string         `json:"lua" jsonschema:"Lua source to run inside OBS. It is compiled and run in a sandbox with obslua available; os, io and debug are not. Return a value to get it back."`
	Args map[string]any `json:"args,omitempty" jsonschema:"Values the chunk reads as the args table. Pass data here rather than building it into the source."`
}

// init publishes the Scripting group, but only when the channel is actually
// on. The group and the tool are gated on the same check so they cannot
// disagree -- a group advertising a tool the server does not serve is exactly
// what TestRegisteredToolsMatchMetadata exists to catch.
//
// The default build has no such group at all, so list_tool_groups does not
// hint at a capability that is not there.
func init() {
	if !scriptingEnabled() {
		return
	}

	toolGroupMetadata["Scripting"] = &ToolGroupMetadata{
		Name: "Scripting",
		Description: "Runs Lua inside the OBS process through the bridge. Present only in builds made with " +
			"-tags scripting and started with AGENTIC_OBS_SCRIPTING=1.",
		ToolNames: []string{"run_lua_in_obs"},
	}
	ToolGroupOrder = append(ToolGroupOrder, "Scripting")

	// TestHelpContentCompleteness fails for any registered tool with no help
	// entry, so the help travels with the tool rather than in help_tools.go
	// where the default build would also carry it.
	toolHelpContent["run_lua_in_obs"] = runLuaHelp
}

const runLuaHelp = `run_lua_in_obs — run Lua inside the OBS process

Compiles and runs a chunk through the agentic-obs bridge. Returns whatever the
chunk returns.

  lua   Source to run. ` + "`return`" + ` a value to receive it.
  args  Data the chunk reads as the args table. Put parameters here; building
        them into the source is how a scene name becomes code.

The chunk runs sandboxed: obslua is available; os, io, package, debug and the
loadstring/setfenv family are not. It is capped at 2,000,000 instructions.

It runs on OBS's render thread, so a slow chunk drops frames.`

func registerScriptingChannel(s *Server) {
	if !scriptingEnabled() {
		return
	}

	log.Println("scripting channel ENABLED: run_lua_in_obs runs arbitrary code inside OBS")

	mcpsdk.AddTool(s.mcpServer,
		&mcpsdk.Tool{
			Name: "run_lua_in_obs",
			Description: "Run Lua inside the OBS process through the agentic-obs bridge. " +
				"This executes arbitrary code in OBS on its render thread: a slow chunk drops frames. " +
				"Pass parameters in args rather than building them into the source.",
		},
		s.handleRunLuaInOBS,
	)
}

func (s *Server) handleRunLuaInOBS(ctx context.Context, request *mcpsdk.CallToolRequest, input RunLuaInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	if input.Lua == "" {
		return nil, nil, fmt.Errorf("lua is required")
	}

	// Per call, not once per session. The environment variable authorises the
	// capability; this authorises the specific chunk, and the chunk is the
	// part that varies.
	confirmed, err := ElicitConfirmation(ctx, getSession(request),
		fmt.Sprintf("Run this Lua inside OBS?\n\n%s", input.Lua))
	if err != nil {
		return nil, nil, err
	}
	if !confirmed {
		return nil, CancelledResult("run_lua_in_obs"), nil
	}

	result, err := s.bridge.Run(ctx, input.Lua, input.Args)
	if err != nil {
		// A transport failure is evidence the cached presence is stale.
		s.bridge.Invalidate()
		s.recordAction("run_lua_in_obs", "Run Lua in OBS", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	// The whole chunk goes into the history. "Ran Lua" is already harder to
	// audit than a named request; recording only the outcome would make it
	// impossible.
	s.recordAction("run_lua_in_obs", "Run Lua in OBS", input, result, result.OK, time.Since(start))
	return nil, result, nil
}
```

- [ ] **Step 5: Call it from the server**

In `internal/mcp/server.go`, at the end of `registerToolHandlers()`:

```go
	// Present only in a build made with -tags scripting; a no-op otherwise.
	registerScriptingChannel(s)
```

- [ ] **Step 6: Run both builds**

Run: `go test ./... && go build -o /dev/null .`
Expected: PASS. The default build has no `run_lua_in_obs`.

Run: `go test -tags scripting ./... && go vet -tags scripting ./...`
Expected: PASS (3 new tests).

`TestRegisteredToolsMatchMetadata` and `TestHelpContentCompleteness` should pass unchanged: the `init` above registers the group and the help entry under the same condition that registers the tool, so the two never disagree.

`HelpToolCount` in `internal/mcp/help_content.go` is written by hand and counts the default build. **Leave it alone** — it describes the shipped surface, and the tagged build adds one tool on top. If `TestHelpToolCountMatchesRegisteredTools` fails under `-tags scripting`, make the test add `len(scriptingToolNames())` rather than changing the constant, and add to `scripting_channel.go`:

```go
// scriptingToolNames lets the count test account for a tool that exists only
// in this build.
func scriptingToolNames() []string { return []string{"run_lua_in_obs"} }
```

with the matching stub returning `nil`.

- [ ] **Step 7: Document it**

Add to `docs/TOOLS.md`, in a new section at the end:

```markdown
## Scripting channel (not in default builds)

`run_lua_in_obs` is absent unless agentic-obs was built with `-tags scripting`
**and** started with `AGENTIC_OBS_SCRIPTING=1`. It runs arbitrary Lua inside the
OBS process, on the render thread, through the bridge.

| Parameter | Type | Description |
|---|---|---|
| `lua` | string | Source to compile and run. Return a value to receive it. |
| `args` | object | Data the chunk reads as `args`. Pass parameters here; never build them into `lua`. |

The chunk runs in a sandbox: `obslua` is available, `os`, `io`, `package`,
`debug` and the `loadstring`/`setfenv` family are not. It is bounded at
2,000,000 instructions so a runaway loop cannot hang OBS. Every call asks for
confirmation and the full source is written to action history.
```

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/scripting_channel.go internal/mcp/scripting_channel_stub.go internal/mcp/scripting_channel_test.go internal/mcp/server.go docs/TOOLS.md
git commit -m "feat(bridge): the scripting channel, behind a build tag and an env var"
```

---

### Task 10: Remove the spike and record the work

**Files:**
- Delete: `bridge/spike/agentic-obs-spike.lua`
- Delete: `internal/obs/live_spike_test.go`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Confirm the real bridge covers what the spike proved**

Run: `OBS_LIVE_TEST=1 OBS_PASSWORD=... go test -tags obslive ./internal/bridge/ -v`
Expected: PASS. The round trip, the sandbox and the budget are all covered by `live_test.go`, so the spike's tests are redundant rather than merely old.

- [ ] **Step 2: Uninstall the spike from OBS**

The spike was added through Tools → Scripts by hand, so remove it the same way, or it will keep creating `agentic-obs-spike-inbox` and `agentic-obs-spike-mailbox` alongside the real bridge.

- [ ] **Step 3: Delete the files**

```bash
git rm bridge/spike/agentic-obs-spike.lua internal/obs/live_spike_test.go
```

- [ ] **Step 4: Add the changelog entry**

Under `## [Unreleased]` in `CHANGELOG.md`, in `### Added`:

```markdown
- **The Lua bridge** — agentic-obs can run code inside the OBS process, over a
  pair of source-settings mailboxes. Both directions are push and the round
  trip measures about 30 ms.

  obs-websocket's vendor API would have been the obvious transport and is
  closed to Lua: `calldata_ptr` hands its proc handler back as a bare `void*`
  that SWIG will not retype. Script reload is closed to everyone outside the
  frontend-tools plugin, so the bridge ships code rather than reloading files.
  See [ADR-013](design/decisions/013-the-lua-bridge.md).

  Install with `agentic-obs install-bridge --all` while OBS is closed. Scene
  collections keep their own script lists, so a new collection needs another
  run. `get_obs_status` reports whether the bridge is loaded.

- **`run_lua_in_obs`, absent from default builds** — the scripting channel needs
  both `-tags scripting` at build time and `AGENTIC_OBS_SCRIPTING=1` at startup.
  It is compiled out rather than switched off because `set_tool_config` is a
  Meta tool that can enable any group, so a group the model can see is one it
  can turn on for itself.
```

- [ ] **Step 5: Run everything one last time**

```bash
go test ./... && go vet ./... && gofmt -l . && python scripts/check-lua.py
go test -tags scripting ./...
```

Expected: all pass, `gofmt -l` silent.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore(bridge): retire the spike, record the bridge"
```

---

## Notes for the executor

**The transport's `args` encoding is load-bearing and non-obvious.** It is an array of `{name, s|n|b}` entries rather than a JSON object, because `obs_data_item_next` takes `obs_data_item_t **` in C and SWIG will not marshal that from Lua — a script cannot iterate an object's keys at all. Both ends must agree, so if you change one, change the other and re-run `TestLiveBridgeReceivesArgsAsDataNotCode`.

**Do not skip the live tests.** Tasks 1–4 and 7–8 are covered without OBS, but the sandbox, the instruction budget and the args separation exist only in Lua. CGO is out, so `go test ./...` can never see them. A green offline suite says nothing about whether `os` is reachable from inside a chunk.
