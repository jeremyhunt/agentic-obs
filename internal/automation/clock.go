package automation

import (
	"sort"
	"sync"
	"time"
)

// clock is the engine's single source of "what time is it".
//
// Every cooldown decision goes through this rather than calling time.Now
// directly, so tests can advance time instead of sleeping through it.
// TestEngineCooldown used to sleep exactly as long as the cooldown it was
// waiting out, which races the boundary and made it fail intermittently --
// FB-37 on the roadmap. Sleeping is also slow: that one test spent 1.1s doing
// nothing. (FB-56)
type clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration

	// AfterFunc schedules f to run after d, and returns a handle for cancelling
	// it. Debounce is built on this, and a debounce test that waited out real
	// durations would be as slow and as flaky as the cooldown test this seam was
	// created to fix.
	AfterFunc(d time.Duration, f func()) timer
}

// timer is the part of time.Timer that anything here needs: the ability to give
// up on a callback that has not run yet.
type timer interface {
	// Stop cancels the timer, reporting false if it had already fired.
	Stop() bool
}

// realClock is the production implementation.
type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

func (realClock) AfterFunc(d time.Duration, f func()) timer { return time.AfterFunc(d, f) }

// fakeClock is a manually advanced clock for tests. Safe for concurrent use,
// because the engine reads the time from goroutines the test does not control.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	pending []*fakeTimer
}

// fakeTimer is one scheduled callback on a fake clock.
type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

// Stop cancels the timer, reporting whether it was still pending.
//
// The clock is not locked here: a timer holds no state the clock needs, and
// taking the clock's lock would deadlock a callback that stops another timer,
// which is exactly what resetting a debounce does.
func (t *fakeTimer) Stop() bool {
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

// newFakeClock starts at a fixed, arbitrary instant. The absolute value does not
// matter; only the differences do.
func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// AfterFunc schedules f to run once the clock has been advanced past d.
func (c *fakeClock) AfterFunc(d time.Duration, f func()) timer {
	c.mu.Lock()
	defer c.mu.Unlock()

	t := &fakeTimer{at: c.now.Add(d), f: f}
	c.pending = append(c.pending, t)
	return t
}

// Advance moves the clock forward, firing any timer whose deadline it passes.
//
// Timers fire in deadline order, and they fire *after* the lock is released.
// Holding it would deadlock the first callback that asks the clock what time it
// is -- which a debounced rule does, to make its cooldown decision -- and that
// is the normal path rather than an edge case.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now

	var due []*fakeTimer
	kept := c.pending[:0]
	for _, t := range c.pending {
		switch {
		case t.stopped:
			// dropped
		case !t.at.After(now):
			t.fired = true
			due = append(due, t)
		default:
			kept = append(kept, t)
		}
	}
	c.pending = kept
	c.mu.Unlock()

	sort.SliceStable(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}
