package automation

import (
	"fmt"
	"sync"
	"time"
)

// The engine both reacts to OBS events and writes to OBS, and OBS announces
// every write as an event. A rule that reacts to visibility by setting
// visibility therefore feeds itself: the write lands, the event comes back,
// the rule matches again.
//
// Cooldown does not solve this. It is off by default, so the loop is the
// out-of-the-box behaviour rather than an edge case; and when it is on it
// throttles a genuine stream of events exactly as hard as it throttles an echo,
// which is the wrong trade -- cooldown is a rate limit, not an identity check.
//
// A suppressor is an identity check. Before each state-setting write the
// executor records what OBS is about to announce, and the dispatcher drops the
// first event that matches. The rejected alternative was rse/obs-scripts'
// approach of detaching the event callback for the duration of a write: that is
// racy under concurrency and blinds every other consumer on the same fan-out,
// including the thumbnail cache and the resource notifications.

// suppressTTL is how long an expected echo is waited for.
//
// Long enough to cover the round trip -- the write goes out, OBS applies it on
// the graphics thread, the event comes back -- and short enough that an echo
// that never arrives cannot eat a genuine event. A write can fail, and OBS can
// coalesce two changes into one announcement, so entries must expire: one that
// waited indefinitely would swallow a real user action minutes later, silently.
const suppressTTL = 500 * time.Millisecond

// writeKey identifies a state change precisely enough that our own echo is
// distinguishable from an identical-looking change somebody else made.
//
// It is built from the typed payload rather than the raw event map, so a key is
// wrong at compile time rather than at three in the morning.
type writeKey string

// visibilityKey identifies a scene item's visibility landing on a value.
//
// The value is part of the key on purpose. If it were not, hiding something we
// had just shown would be read as our own echo and ignored -- the engine would
// stop responding to the operator precisely when they were correcting it.
func visibilityKey(sceneName string, sceneItemID int, visible bool) writeKey {
	return writeKey(fmt.Sprintf("visibility|%s|%d|%t", sceneName, sceneItemID, visible))
}

// muteKey identifies an input's mute state landing on a value.
func muteKey(inputName string, muted bool) writeKey {
	return writeKey(fmt.Sprintf("mute|%s|%t", inputName, muted))
}

// sceneKey identifies the program scene becoming a particular scene.
func sceneKey(sceneName string) writeKey {
	return writeKey(fmt.Sprintf("scene|%s", sceneName))
}

// writeSuppressor remembers writes the engine has just made, so their echoes
// can be told apart from events somebody else caused.
type writeSuppressor struct {
	mu      sync.Mutex
	clock   clock
	ttl     time.Duration
	pending map[writeKey][]time.Time
}

func newWriteSuppressor(c clock, ttl time.Duration) *writeSuppressor {
	return &writeSuppressor{
		clock:   c,
		ttl:     ttl,
		pending: map[writeKey][]time.Time{},
	}
}

// setClock replaces the suppressor's source of time. See
// AutomationEngine.setClock for why the two must move together.
func (s *writeSuppressor) setClock(c clock) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = c
}

// Expect records that a write has been made and its echo is coming.
//
// Counted rather than flagged: two writes of the same thing produce two events,
// and collapsing them to one entry would let the second through to re-trigger
// the rule that caused them.
func (s *writeSuppressor) Expect(key writeKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expireLocked()
	s.pending[key] = append(s.pending[key], s.clock.Now())
}

// Consume reports whether this event is the echo of one of our own writes, and
// if so takes the entry.
//
// Consume-on-match is the whole design. Leaving the entry in place would make
// the suppressor a filter that swallows every event of that shape for its
// lifetime, so an operator flicking a source back and forth would be ignored.
func (s *writeSuppressor) Consume(key writeKey) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expireLocked()

	waiting := s.pending[key]
	if len(waiting) == 0 {
		return false
	}
	if len(waiting) == 1 {
		delete(s.pending, key)
	} else {
		s.pending[key] = waiting[1:]
	}
	return true
}

// Pending reports how many echoes are still expected. It exists for tests and
// for the engine's own diagnostics: a number that only grows means writes are
// being recorded whose events never arrive.
func (s *writeSuppressor) Pending() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.expireLocked()

	n := 0
	for _, waiting := range s.pending {
		n += len(waiting)
	}
	return n
}

// expireLocked drops entries older than the TTL. Caller holds s.mu.
//
// Sweeping on access rather than on a timer keeps the suppressor free of
// goroutines and of any lifecycle of its own: it cannot outlive the engine or
// leak a ticker, and the cost is paid only when something is happening anyway.
func (s *writeSuppressor) expireLocked() {
	cutoff := s.clock.Now().Add(-s.ttl)
	for key, waiting := range s.pending {
		kept := waiting[:0]
		for _, at := range waiting {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		if len(kept) == 0 {
			delete(s.pending, key)
			continue
		}
		s.pending[key] = kept
	}
}

// keyForEvent builds the suppression key an incoming event would match, or
// false when the event is not one the engine ever causes.
//
// Only the events the executor can produce are worth keying. Anything else --
// a stream starting, a transition ending -- cannot be our own echo, so giving
// it a key would be inventing a way to swallow it.
func keyForEvent(payload EventPayload) (writeKey, bool) {
	switch payload.EventType {
	case EventSourceVisibilityChanged:
		scene, ok1 := payload.Data["scene_name"].(string)
		visible, ok2 := payload.Data["visible"].(bool)
		id, ok3 := asInt(payload.Data["scene_item_id"])
		if ok1 && ok2 && ok3 {
			return visibilityKey(scene, id, visible), true
		}

	case EventInputMuteChanged:
		name, ok1 := payload.Data["input_name"].(string)
		muted, ok2 := payload.Data["muted"].(bool)
		if ok1 && ok2 {
			return muteKey(name, muted), true
		}

	case EventSceneChanged:
		name, ok := payload.Data["scene_name"].(string)
		if ok {
			return sceneKey(name), true
		}
	}
	return "", false
}

// asInt accepts the numeric types an event payload can carry.
//
// The payload is map[string]interface{}, and a value that has been through JSON
// arrives as float64 whatever it started as. Type-asserting to int alone works
// in process and fails the moment the same event arrives over the wire.
func asInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
