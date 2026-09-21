package automation

import (
	"sync/atomic"
	"testing"
	"time"
)

// Debounce needs a timer, and a timer in a test must be something the test
// drives rather than something it waits for. These cover the seam itself; the
// debounce tests cover what is built on it.

func TestFakeClockFiresATimerWhenTimePasses(t *testing.T) {
	clk := newFakeClock()

	var fired atomic.Int32
	clk.AfterFunc(100*time.Millisecond, func() { fired.Add(1) })

	clk.Advance(99 * time.Millisecond)
	if n := fired.Load(); n != 0 {
		t.Fatalf("the timer fired %d times before its deadline", n)
	}

	clk.Advance(2 * time.Millisecond)
	if n := fired.Load(); n != 1 {
		t.Errorf("the timer fired %d times after its deadline passed, want 1", n)
	}

	// A one-shot timer stays fired. Re-firing on every later advance would make
	// a debounced rule run once per tick of whatever moved the clock.
	clk.Advance(time.Second)
	if n := fired.Load(); n != 1 {
		t.Errorf("the timer fired %d times in total; it should fire once", n)
	}
}

func TestFakeClockTimerCanBeStopped(t *testing.T) {
	clk := newFakeClock()

	var fired atomic.Int32
	timer := clk.AfterFunc(100*time.Millisecond, func() { fired.Add(1) })

	if !timer.Stop() {
		t.Error("Stop reported the timer had already fired")
	}
	clk.Advance(time.Second)

	if n := fired.Load(); n != 0 {
		t.Errorf("a stopped timer fired %d times", n)
	}
	if timer.Stop() {
		t.Error("Stop reported success on a timer that was already stopped")
	}
}

func TestFakeClockTimerCallbackSeesTheNewTime(t *testing.T) {
	clk := newFakeClock()
	start := clk.Now()

	var seen time.Time
	done := make(chan struct{})
	clk.AfterFunc(100*time.Millisecond, func() {
		// Calling back into the clock from inside a callback must not deadlock,
		// which it would if the callback ran while Advance held the lock. A
		// debounce callback reads the time to make its cooldown decision, so
		// this is the normal path, not an edge case.
		seen = clk.Now()
		close(done)
	})

	clk.Advance(150 * time.Millisecond)
	<-done

	if !seen.After(start) {
		t.Errorf("the callback saw %v, which is not after the start time %v", seen, start)
	}
}

func TestFakeClockFiresTimersInDeadlineOrder(t *testing.T) {
	clk := newFakeClock()

	var order []string
	done := make(chan struct{}, 3)
	clk.AfterFunc(300*time.Millisecond, func() { order = append(order, "third"); done <- struct{}{} })
	clk.AfterFunc(100*time.Millisecond, func() { order = append(order, "first"); done <- struct{}{} })
	clk.AfterFunc(200*time.Millisecond, func() { order = append(order, "second"); done <- struct{}{} })

	clk.Advance(500 * time.Millisecond)
	for i := 0; i < 3; i++ {
		<-done
	}

	want := []string{"first", "second", "third"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("timers fired in order %v, want %v", order, want)
		}
	}
}
