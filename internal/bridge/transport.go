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
	//
	// The literals live in internal/obs, which this package imports, because
	// the passthrough guard in internal/obs/dispatch.go needs them and cannot
	// import this package back. Aliased rather than repeated so there is one
	// spelling of each name in the binary.
	InboxSource   = obs.BridgeInboxSource
	MailboxSource = obs.BridgeMailboxSource

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

	// PresenceTTL bounds how long a Probe result is reused. Now is a clock
	// seam so tests can move time rather than sleep through it, matching the
	// automation engine.
	PresenceTTL time.Duration
	Now         func() time.Time

	presence presenceCache
}

type pending struct {
	id   string
	done chan Result
}

func New(caller Caller) *Transport {
	return &Transport{caller: caller, Timeout: DefaultTimeout, PresenceTTL: DefaultPresenceTTL}
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
