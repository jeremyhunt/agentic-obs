package bridge

import (
	"context"
	"testing"
	"time"
)

func TestProbeReportsPresentWithVersion(t *testing.T) {
	var transport *Transport
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		transport.HandleEvent(reply(s["id"].(string), true, "1", ""))
	}
	transport = New(caller)

	got := transport.Probe(context.Background())
	if !got.Present {
		t.Fatalf("Present = false, want true: %+v", got)
	}
	if got.Version != "1" {
		t.Errorf("Version = %q, want \"1\"", got.Version)
	}
}

func TestProbeReportsAbsentWhenTheWriteFails(t *testing.T) {
	transport := New(&fakeCaller{err: errTestNoSuchInput})

	got := transport.Probe(context.Background())
	if got.Present {
		t.Fatal("Present = true when the inbox does not exist")
	}
	if got.Detail == "" {
		t.Error("Detail is empty; an absent bridge must say why")
	}
}

func TestProbeOnANilTransportReportsAbsentWithoutPanicking(t *testing.T) {
	// A bare-constructed *Server in a test can easily leave bridge nil; Probe
	// must report that as "not there" rather than panic on the caller.
	var transport *Transport

	got := transport.Probe(context.Background())
	if got.Present {
		t.Fatal("Present = true for a nil transport")
	}
	if got.Detail == "" {
		t.Error("Detail is empty; a nil transport must say why")
	}
}

func TestProbeIsCachedWithinTheTTL(t *testing.T) {
	var transport *Transport
	calls := 0
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		calls++
		transport.HandleEvent(reply(s["id"].(string), true, "1", ""))
	}
	transport = New(caller)

	transport.Probe(context.Background())
	transport.Probe(context.Background())

	if calls != 1 {
		t.Fatalf("probed %d times inside the TTL, want 1", calls)
	}
}

func TestProbeRefreshesAfterTheTTL(t *testing.T) {
	var transport *Transport
	calls := 0
	caller := &fakeCaller{}
	caller.onWrite = func(s map[string]interface{}) {
		calls++
		transport.HandleEvent(reply(s["id"].(string), true, "1", ""))
	}
	transport = New(caller)

	now := time.Now()
	transport.Now = func() time.Time { return now }

	transport.Probe(context.Background())
	now = now.Add(10 * time.Second)
	transport.Probe(context.Background())

	if calls != 2 {
		t.Fatalf("probed %d times across the TTL, want 2", calls)
	}
}
