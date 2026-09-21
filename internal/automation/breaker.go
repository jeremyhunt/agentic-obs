package automation

import (
	"sync"
	"time"
)

// A circuit breaker for rules that will not stop.
//
// Suppression (ADR-010) removes the loop it can identify, and it identifies
// most of them. It cannot identify all: a toggle's landing value is not known
// before the write, so a rule that toggles in response to its own toggle still
// oscillates, and a rule can reach the same state by a path with no key at all
// -- through a vendor request, or through a second rule.
//
// The breaker does not need to understand why a rule is running away. It only
// needs to notice that it is, and stopping a rule beats melting OBS. The cost
// of being wrong is one disabled rule and a log line saying so, which an
// operator can undo; the cost of not acting is unbounded writes to a live
// broadcast.

const (
	// oscillationThreshold is how many executions inside the window are treated
	// as a runaway.
	//
	// Deliberately well above what a busy rule reaches. The measured loop this
	// was built for managed 2,300 writes in 300ms, so anything in this region
	// separates "broken" from "busy" by three orders of magnitude, and erring
	// high costs only a slightly later trip.
	oscillationThreshold = 20

	// oscillationWindow is the period the threshold is counted over.
	oscillationWindow = 1 * time.Second
)

// breaker counts how often each rule has run recently.
type breaker struct {
	mu        sync.Mutex
	clock     clock
	threshold int
	window    time.Duration
	recent    map[int64][]time.Time
}

func newBreaker(c clock, threshold int, window time.Duration) *breaker {
	return &breaker{
		clock:     c,
		threshold: threshold,
		window:    window,
		recent:    map[int64][]time.Time{},
	}
}

// Record notes an execution and reports whether the rule has tripped.
//
// The count is returned alongside so the caller can say how fast it was going.
// A number in the log is the difference between "the tool disabled my rule" and
// "my rule ran 20 times in a second".
func (b *breaker) Record(ruleID int64) (tripped bool, count int) {
	if b == nil {
		return false, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()
	cutoff := now.Add(-b.window)

	// A sliding window, not a counter reset on a tick. A fixed bucket lets a
	// rule run at twice the threshold indefinitely by straddling the boundary,
	// which is precisely the behaviour this exists to catch.
	kept := b.recent[ruleID][:0]
	for _, at := range b.recent[ruleID] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	b.recent[ruleID] = kept

	return len(kept) >= b.threshold, len(kept)
}

// Forget drops a rule's history.
//
// Called when a rule is disabled, so that re-enabling it starts clean. Without
// this a rule the operator switched back on would trip on its first execution
// and look permanently broken.
func (b *breaker) Forget(ruleID int64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.recent, ruleID)
}

// setClock replaces the breaker's source of time, alongside the engine's.
func (b *breaker) setClock(c clock) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clock = c
}
