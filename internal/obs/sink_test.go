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
