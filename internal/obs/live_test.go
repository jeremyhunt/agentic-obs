//go:build obslive

// Package obs's live contract suite.
//
// This runs the same behavioural contract as internal/obs/obstest's fake test,
// but against a real obs-websocket connection. That parity is the entire point:
// a fake is only useful while it behaves like the thing it replaces, and nothing
// else in this repo forces that. FB-54 -- transform writes silently re-anchoring
// scene items -- lived in the goobs boundary that the fake replaces wholesale, so
// no amount of mock-based testing could have found it.
//
// It is build-tagged because it needs OBS running, which a CI runner does not
// have. Run it locally before a release:
//
//	make test-live
//
// It creates a scratch scene, works only inside it, and removes it afterwards.
package obs_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/obs/obstest"
)

// liveClient connects to a real OBS, or skips the test if one is not configured.
func liveClient(t *testing.T) *obs.Client {
	t.Helper()

	host := envOr("OBS_HOST", "localhost")
	port := envOr("OBS_PORT", "4455")
	password := os.Getenv("OBS_PASSWORD")

	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1 (and OBS_HOST/OBS_PORT/OBS_PASSWORD if not default)")
	}

	client := obs.NewClient(obs.ConnectionConfig{Host: host, Port: port, Password: password})
	if err := client.Connect(); err != nil {
		t.Fatalf("could not reach OBS at %s:%s -- is it running with obs-websocket enabled? %v", host, port, err)
	}
	t.Cleanup(func() { _ = client.Disconnect() })

	return client
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestLiveClientSatisfiesContract is the parity check. If this passes and the
// fake's copy passes, the fake is telling the truth about the behaviour the
// contract covers.
func TestLiveClientSatisfiesContract(t *testing.T) {
	obstest.RunContract(t, func(t *testing.T) (obstest.ContractClient, obstest.Fixture) {
		client := liveClient(t)

		// A scratch scene per row, so rows cannot interfere and a failure leaves
		// nothing behind in the user's OBS. Removing the scene also frees the
		// input, which nothing else references.
		scene := fmt.Sprintf("agentic-obs-contract-%d", time.Now().UnixNano())
		if err := client.CreateScene(scene); err != nil {
			t.Fatalf("CreateScene: %v", err)
		}
		t.Cleanup(func() {
			if err := client.RemoveScene(scene); err != nil {
				t.Logf("warning: could not remove scratch scene %q: %v", scene, err)
			}
		})

		const kind = "color_source_v3"
		source := scene + "-item"
		itemID, err := client.CreateInput(scene, source, kind, nil)
		if err != nil {
			t.Fatalf("CreateInput: %v", err)
		}

		return client, obstest.Fixture{
			SceneName:   scene,
			SourceName:  source,
			SourceKind:  kind,
			SceneItemID: itemID,
		}
	})
}

// recordingSink collects translated events for the live event test.
type recordingSink struct {
	events chan obs.Event
}

func (s recordingSink) HandleEvent(e obs.Event) {
	select {
	case s.events <- e:
	default: // never block the client's event goroutine
	}
}

// TestLiveEventsReachTheSink is the end-to-end proof that events flow at all.
//
// Nothing else covers this. The translation table is unit-tested and the
// subscription mask is unit-tested, but whether OBS actually pushes an event we
// asked for, and whether it survives goobs and the sink, is only answerable
// against a real server. It is also the test that would have caught FB-62: a
// category missing from the mask produces no error, just silence.
func TestLiveEventsReachTheSink(t *testing.T) {
	client := liveClient(t)

	scene := fmt.Sprintf("agentic-obs-events-%d", time.Now().UnixNano())
	if err := client.CreateScene(scene); err != nil {
		t.Fatalf("CreateScene: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveScene(scene); err != nil {
			t.Logf("warning: could not remove scratch scene %q: %v", scene, err)
		}
	})

	itemID, err := client.CreateInput(scene, scene+"-item", "color_source_v3", nil)
	if err != nil {
		t.Fatalf("CreateInput: %v", err)
	}

	sink := recordingSink{events: make(chan obs.Event, 32)}
	client.SetEventSink(sink)

	// Hiding the item is a state change OBS announces via
	// SceneItemEnableStateChanged, which needs the SceneItems subscription.
	if err := client.SetSceneItemEnabled(scene, itemID, false); err != nil {
		t.Fatalf("SetSceneItemEnabled: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-sink.events:
			if e.Type != obs.EventTypeSourceVisibilityChanged {
				continue // other events on the same connection are fine
			}
			if got := e.Payload["scene_name"]; got != scene {
				continue // a visibility change elsewhere in the collection
			}
			if got := e.Payload["visible"]; got != false {
				t.Errorf("event reports visible=%v, want false", got)
			}
			if got := e.Payload["scene_item_id"]; got != itemID {
				t.Errorf("event reports scene_item_id=%v, want %d", got, itemID)
			}
			if e.At.IsZero() {
				t.Error("event carries no timestamp")
			}
			return
		case <-deadline:
			t.Fatal("no visibility event arrived within 5s; either OBS did not send it " +
				"or the subscription mask does not include SceneItems")
		}
	}
}
