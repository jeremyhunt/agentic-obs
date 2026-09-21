package automation

import (
	"testing"
	"time"
)

// Suppression removes the loop it can identify. It cannot identify every one:
// a toggle's landing value is not known before the write, so a rule that
// toggles in response to its own toggle still oscillates, and a rule can reach
// the same state by a path with no key at all.
//
// The breaker is the backstop. It does not need to understand why a rule is
// running away -- only that it is, and that stopping it beats melting OBS.

func TestBreakerTripsOnARunawayRule(t *testing.T) {
	clock := newFakeClock()
	b := newBreaker(clock, 20, time.Second)

	for i := 0; i < 19; i++ {
		if tripped, _ := b.Record(7); tripped {
			t.Fatalf("tripped after %d executions, before the threshold of 20", i+1)
		}
	}
	tripped, count := b.Record(7)
	if !tripped {
		t.Fatal("20 executions inside a second did not trip the breaker")
	}
	if count != 20 {
		t.Errorf("the breaker reports %d executions, want 20; the number is what "+
			"tells an operator how fast it was going", count)
	}
}

func TestBreakerIgnoresABurstThatIsMerelyBusy(t *testing.T) {
	clock := newFakeClock()
	b := newBreaker(clock, 20, time.Second)

	// Nineteen per second, forever. A scene with a stack of layers being shown
	// in sequence looks like this, and disabling that rule would be the tool
	// breaking a working setup -- which is worse than the loop, because the
	// operator did nothing wrong.
	for round := 0; round < 10; round++ {
		for i := 0; i < 19; i++ {
			if tripped, _ := b.Record(7); tripped {
				t.Fatalf("round %d: tripped on a sustained rate below the threshold", round)
			}
		}
		clock.Advance(time.Second)
	}
}

func TestBreakerCountsEachRuleSeparately(t *testing.T) {
	clock := newFakeClock()
	b := newBreaker(clock, 3, time.Second)

	// One rule running away must not take out the others. They are separate
	// pieces of the operator's configuration and only one of them is broken.
	//
	// Run rule 1 past the threshold while rule 2 stays well under it. Recording
	// both up to the line instead would pass even against a single shared
	// counter, because by then everything has tripped -- the assertion that
	// separates the two designs is that rule 2 is *not* tripped.
	for i := 0; i < 3; i++ {
		if _, count := b.Record(1); count != i+1 {
			t.Errorf("rule 1's count is %d on execution %d", count, i+1)
		}
	}

	tripped, count := b.Record(2)
	if tripped {
		t.Error("rule 2 tripped on its first execution; one runaway rule is taking " +
			"out the others, so the counter is shared")
	}
	if count != 1 {
		t.Errorf("rule 2's first execution is counted as number %d", count)
	}
}

func TestBreakerForgetsARuleOnceItIsDisabled(t *testing.T) {
	clock := newFakeClock()
	b := newBreaker(clock, 3, time.Second)

	for i := 0; i < 3; i++ {
		b.Record(1)
	}
	b.Forget(1)

	// A rule the operator re-enables starts with a clean history. Otherwise it
	// would trip again on its first execution and look permanently broken.
	if tripped, _ := b.Record(1); tripped {
		t.Error("a re-enabled rule tripped immediately on stale history")
	}
}

func TestBreakerIsSafeWhenUnset(t *testing.T) {
	var b *breaker
	if tripped, _ := b.Record(1); tripped {
		t.Error("a nil breaker tripped")
	}
	b.Forget(1) // must not panic
}
