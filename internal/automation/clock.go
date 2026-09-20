package automation

import (
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
}

// realClock is the production implementation.
type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }

// fakeClock is a manually advanced clock for tests. Safe for concurrent use,
// because the engine reads the time from goroutines the test does not control.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
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

// Advance moves the clock forward. Tests use this in place of time.Sleep when
// what they are waiting for is a deadline rather than another goroutine.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
