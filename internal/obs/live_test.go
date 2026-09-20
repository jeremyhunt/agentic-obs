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
	obstest.RunContract(t, func(t *testing.T) (obstest.SceneItemClient, obstest.Fixture) {
		client := liveClient(t)

		// A scratch scene per row, so rows cannot interfere and a failure leaves
		// nothing behind in the user's OBS.
		scene := fmt.Sprintf("agentic-obs-contract-%d", time.Now().UnixNano())
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

		return client, obstest.Fixture{SceneName: scene, SceneItemID: itemID}
	})
}
