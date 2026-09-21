package automation

import (
	"testing"
	"time"
)

// The engine reacts to OBS events and writes to OBS. Its own writes come back
// as events, so without something in between, a rule that reacts to visibility
// by setting visibility feeds itself. Cooldown does not prevent that -- it is
// off by default, and when it is on it throttles a genuine stream of events
// just as hard as it throttles an echo.

func TestSuppressorEatsOurOwnEchoExactlyOnce(t *testing.T) {
	clock := newFakeClock()
	s := newWriteSuppressor(clock, 500*time.Millisecond)

	key := visibilityKey("Game", 12, true)
	s.Expect(key)

	// The echo of our own write.
	if !s.Consume(key) {
		t.Fatal("the echo of a write we just made was not suppressed")
	}

	// A second event with the same shape is not ours. An operator flicking the
	// same item back and forth produces exactly this, and eating it would make
	// the engine ignore the user.
	if s.Consume(key) {
		t.Error("a second identical event was also suppressed; the entry must be " +
			"consumed on match, not left to swallow everything that looks like it")
	}
}

func TestSuppressorLeavesSomebodyElsesEventAlone(t *testing.T) {
	clock := newFakeClock()
	s := newWriteSuppressor(clock, 500*time.Millisecond)

	s.Expect(visibilityKey("Game", 12, true))

	// Same scene and item, different value: the operator hid what we showed.
	if s.Consume(visibilityKey("Game", 12, false)) {
		t.Error("an event with a different value was suppressed")
	}
	// Same value, different item.
	if s.Consume(visibilityKey("Game", 99, true)) {
		t.Error("an event about another item was suppressed")
	}
	// Same everything, different scene.
	if s.Consume(visibilityKey("Intermission", 12, true)) {
		t.Error("an event in another scene was suppressed")
	}

	// And our own echo still arrives unharmed after all that.
	if !s.Consume(visibilityKey("Game", 12, true)) {
		t.Error("the real echo was lost while rejecting the others")
	}
}

func TestSuppressorForgetsAnEchoThatNeverArrived(t *testing.T) {
	clock := newFakeClock()
	s := newWriteSuppressor(clock, 500*time.Millisecond)

	key := visibilityKey("Game", 12, true)
	s.Expect(key)

	// A write can fail, or OBS can coalesce, so an expected echo may never come.
	// An entry that waited forever would eat a genuine event minutes later --
	// which is worse than a missed suppression, because it is silent.
	clock.Advance(501 * time.Millisecond)

	if s.Consume(key) {
		t.Error("an expired entry still suppressed an event")
	}
	if n := s.Pending(); n != 0 {
		t.Errorf("%d expired entries are still held", n)
	}
}

func TestSuppressorHandlesRepeatedWritesOfTheSameThing(t *testing.T) {
	clock := newFakeClock()
	s := newWriteSuppressor(clock, 500*time.Millisecond)

	key := visibilityKey("Game", 12, true)
	s.Expect(key)
	s.Expect(key)

	// Two writes, two echoes. Collapsing them to one entry would let the second
	// echo through and re-trigger the rule.
	if !s.Consume(key) || !s.Consume(key) {
		t.Error("two writes produced two echoes and only one was suppressed")
	}
	if s.Consume(key) {
		t.Error("a third event was suppressed with only two writes outstanding")
	}
}

func TestSuppressorIsAnUnsetFieldAwayFromDoingNothing(t *testing.T) {
	// A nil suppressor must be safe to call. The engine consults it on every
	// event, and a nil-check at each call site is a nil-check someone forgets.
	var s *writeSuppressor
	if s.Consume(visibilityKey("Game", 1, true)) {
		t.Error("a nil suppressor suppressed something")
	}
	s.Expect(visibilityKey("Game", 1, true)) // must not panic
	if n := s.Pending(); n != 0 {
		t.Errorf("a nil suppressor reports %d pending entries", n)
	}
}

func TestSuppressorFollowsTheEnginesClock(t *testing.T) {
	db, cleanup := testAutomationDB(t)
	defer cleanup()

	engine := NewAutomationEngine(db, NewMockOBSClient())

	// Swapping the engine's clock has to swap the suppressor's too. An engine
	// on a fake clock whose suppressor is still on the real one has two notions
	// of now, which is the condition the clock seam exists to prevent -- and it
	// would come back one field at a time without something saying so.
	clk := newFakeClock()
	engine.setClock(clk)

	engine.suppressor.Expect(visibilityKey("Game", 7, true))
	if n := engine.suppressor.Pending(); n != 1 {
		t.Fatalf("the write was not recorded: %d pending", n)
	}

	clk.Advance(suppressTTL + time.Millisecond)

	if n := engine.suppressor.Pending(); n != 0 {
		t.Errorf("%d entries survived the TTL after the engine's clock advanced; "+
			"the suppressor is running on a different clock", n)
	}
}
