package automation

import (
	"sync"
	"testing"
	"time"
)

// TestFakeClockAdvances covers the test helper FB-56 introduced and never tested.
//
// A clock that advances incorrectly would make every cooldown test lie in a way
// that looks like a passing suite, which is worse than no test at all.
func TestFakeClockAdvances(t *testing.T) {
	c := newFakeClock()
	start := c.Now()

	c.Advance(500 * time.Millisecond)

	if got := c.Now().Sub(start); got != 500*time.Millisecond {
		t.Errorf("after advancing 500ms the clock moved %v", got)
	}
}

// TestFakeClockSinceMatchesAdvance is the property the cooldown check relies on:
// Since(t) must reflect advanced time, not wall-clock time.
func TestFakeClockSinceMatchesAdvance(t *testing.T) {
	c := newFakeClock()
	mark := c.Now()

	if got := c.Since(mark); got != 0 {
		t.Errorf("Since on an unadvanced clock = %v, want 0", got)
	}

	c.Advance(750 * time.Millisecond)

	if got := c.Since(mark); got != 750*time.Millisecond {
		t.Errorf("Since = %v, want 750ms; the cooldown check compares this against the rule's cooldown", got)
	}
}

// TestFakeClockDoesNotMoveOnItsOwn is what makes the cooldown test deterministic:
// real time passing must not affect it.
func TestFakeClockDoesNotMoveOnItsOwn(t *testing.T) {
	c := newFakeClock()
	before := c.Now()

	time.Sleep(20 * time.Millisecond)

	if !c.Now().Equal(before) {
		t.Error("the fake clock moved without Advance; tests using it would be timing-dependent again")
	}
}

// TestFakeClockIsSafeForConcurrentUse matters because the engine reads the time
// from goroutines the test does not control. Under -race this would fail loudly;
// without cgo it at least exercises the locking.
func TestFakeClockIsSafeForConcurrentUse(t *testing.T) {
	c := newFakeClock()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.Advance(time.Millisecond) }()
		go func() { defer wg.Done(); _ = c.Now() }()
	}
	wg.Wait()

	if got := c.Since(newFakeClock().Now()); got != 50*time.Millisecond {
		t.Errorf("after 50 concurrent 1ms advances the clock moved %v, want 50ms", got)
	}
}
