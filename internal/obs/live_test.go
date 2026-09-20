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
	"sort"
	"strings"
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
	// One connection for the whole run, not one per row.
	//
	// Row isolation is a property of the fixture -- a fresh scratch scene and
	// input per row -- and never needed a socket of its own. Connecting per row
	// bought nothing and cost reliability: goobs v1.8.3 panics on disconnect if
	// the server pushes a message at the wrong moment. client.go:351-368 checks
	// whether `Disconnected` is closed and then sends on `Opcodes`, but
	// markDisconnected closes both, so a message arriving between the check and
	// the send is a send on a closed channel. That is a panic in goobs' own
	// goroutine: it cannot be recovered from here, and it fails the run.
	//
	// Every row creates and removes scenes, inputs and filters, so OBS is
	// almost always emitting an event as a row tears down -- exactly the window
	// the race needs. Disconnecting once instead of once per row does not fix
	// goobs, but it removes the repetition that was making a rare race a
	// regular one.
	client := liveClient(t)

	obstest.RunContract(t, func(t *testing.T) (obstest.ContractClient, obstest.Fixture) {
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

		groupName, groupItem := borrowGroup(t, client, scene)

		return client, obstest.Fixture{
			SceneName:   scene,
			SourceName:  source,
			SourceKind:  kind,
			SceneItemID: itemID,
			GroupName:   groupName,
			GroupItemID: groupItem,
		}
	})
}

// borrowGroup places an existing group into the scratch scene, or returns ""
// when the collection has none.
//
// The live suite cannot build its own group: obs-websocket's Scenes category
// has CreateScene but no CreateGroup, and the group requests only read. So the
// fixture borrows one. DuplicateSceneItem is what makes that safe -- it adds a
// second *placement* of the same group rather than copying it, so the rows
// mutate a scene item inside the scratch scene and the operator's own scenes
// are never touched. Removing the scratch scene removes the borrowed placement
// with it, and the group itself survives because another placement still
// references it.
func borrowGroup(t *testing.T, client *obs.Client, scratchScene string) (string, int) {
	t.Helper()

	groups, err := client.GetGroupList()
	if err != nil {
		t.Fatalf("GetGroupList: %v", err)
	}
	if len(groups) == 0 {
		return "", 0
	}
	inGroups := make(map[string]bool, len(groups))
	for _, g := range groups {
		inGroups[g] = true
	}

	scenes, _, err := client.GetSceneList()
	if err != nil {
		t.Fatalf("GetSceneList: %v", err)
	}
	for _, name := range scenes {
		if name == scratchScene {
			continue
		}
		full, err := client.GetSceneByName(name)
		if err != nil {
			continue // GetSceneItemList refuses a group; skip anything that will not open
		}
		for _, item := range full.Sources {
			if !inGroups[item.Name] {
				continue
			}
			dup, err := client.DuplicateSceneItem(name, item.ID, scratchScene)
			if err != nil {
				t.Fatalf("duplicating group %q from scene %q into the scratch scene: %v",
					item.Name, name, err)
			}
			return item.Name, dup
		}
	}
	return "", 0
}

// TestLiveRequestCoverageIsMeasured compares what this build can issue against
// what the server says it offers.
//
// "Drive every OBS control surface" is only a real claim if the gap is a number
// someone checks, not a sentence in a design document. OBS reports its own
// request list in GetVersion.availableRequests, so the comparison needs no
// hand-maintained table and cannot drift: a request added by a future OBS shows
// up here as uncovered the first time this runs.
//
// The four private-settings requests are expected to be missing. obs-websocket
// offers them and goobs does not generate them, so they need a goobs
// contribution or the Lua bridge. They are listed rather than tolerated
// silently -- if the list ever changes, this test says so.
func TestLiveRequestCoverageIsMeasured(t *testing.T) {
	client := liveClient(t)

	// Read the server's own request list through the passthrough. Using the
	// mechanism under test to measure itself is deliberate: if CallRequest is
	// broken, this test cannot report a false clean bill of health.
	version, err := client.CallRequest("GetVersion", nil)
	if err != nil {
		t.Fatalf("CallRequest(GetVersion): %v", err)
	}
	offered := []string{}
	for _, name := range version["availableRequests"].([]interface{}) {
		offered = append(offered, name.(string))
	}

	reachable := map[string]bool{}
	for _, name := range client.AvailableRequests() {
		reachable[name] = true
	}

	knownUnreachable := map[string]string{
		"GetSourcePrivateSettings":    "goobs does not generate it",
		"SetSourcePrivateSettings":    "goobs does not generate it",
		"GetSceneItemPrivateSettings": "goobs does not generate it",
		"SetSceneItemPrivateSettings": "goobs does not generate it",
	}

	var unexpected []string
	for _, offered := range offered {
		if reachable[offered] {
			continue
		}
		if _, known := knownUnreachable[offered]; known {
			continue
		}
		unexpected = append(unexpected, offered)
	}
	sort.Strings(unexpected)

	t.Logf("obs-websocket %v offers %d requests; %d reachable through call_obs_request",
		version["obsWebSocketVersion"], len(offered), len(reachable))

	if len(unexpected) > 0 {
		t.Errorf("%d requests this build cannot issue and has not accounted for: %v\n"+
			"Either the registry stopped recognising goobs' generated shape, or OBS "+
			"gained requests goobs has not caught up with.",
			len(unexpected), unexpected)
	}

	// The converse: a name in knownUnreachable that has become reachable means
	// goobs caught up and the exception should go, along with whatever work was
	// deferred behind it.
	for name := range knownUnreachable {
		if reachable[name] {
			t.Errorf("%s is now reachable; remove it from knownUnreachable and from "+
				"whatever was waiting on the Lua bridge for it", name)
		}
	}
}

// TestLiveCallRequestReachesAnUnwrappedSurface proves the passthrough is not
// merely a registry.
//
// GetStats has no typed wrapper in this package and is the telemetry the design
// plan wants for a "variables" surface. If it answers here, so does every other
// unwrapped request, because they all travel the same path.
func TestLiveCallRequestReachesAnUnwrappedSurface(t *testing.T) {
	client := liveClient(t)

	got, err := client.CallRequest("GetStats", nil)
	if err != nil {
		t.Fatalf("CallRequest(GetStats): %v", err)
	}
	for _, field := range []string{"cpuUsage", "memoryUsage", "availableDiskSpace", "activeFps"} {
		if _, ok := got[field]; !ok {
			t.Errorf("GetStats response has no %q; got keys %v", field, keysOf(got))
		}
	}

	// A request that takes required parameters, reached generically.
	scenes, _, err := client.GetSceneList()
	if err != nil || len(scenes) == 0 {
		t.Fatalf("GetSceneList: %v", err)
	}
	items, err := client.CallRequest("GetSceneItemList", map[string]interface{}{
		"sceneName": scenes[0],
	})
	if err != nil {
		t.Fatalf("CallRequest(GetSceneItemList): %v", err)
	}
	if _, ok := items["sceneItems"]; !ok {
		t.Errorf("GetSceneItemList response has no sceneItems; got %v", keysOf(items))
	}

	// A server-side refusal must arrive as an error carrying OBS's own words,
	// because the agent's next move depends on which refusal it was.
	_, err = client.CallRequest("GetSceneItemList", map[string]interface{}{
		"sceneName": "agentic-obs-no-such-scene",
	})
	if err == nil {
		t.Fatal("expected an error for a scene that does not exist")
	}
	if !strings.Contains(err.Error(), "ResourceNotFound") {
		t.Errorf("error lost the server's status: %v", err)
	}
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

// TestLivePreviewSceneEventReachesTheSink proves the preview path end to end.
//
// CurrentPreviewSceneChanged is in obs-websocket's Scenes category rather than
// Ui, which is easy to get wrong and fails silently if wrong -- the event simply
// never arrives. Confirming that from the protocol is not the same as seeing one.
//
// It skips rather than enabling studio mode, because that changes the operator's
// OBS layout and is outside what a test should do to someone's live setup.
func TestLivePreviewSceneEventReachesTheSink(t *testing.T) {
	client := liveClient(t)

	studio, err := client.GetStudioModeEnabled()
	if err != nil {
		t.Fatalf("GetStudioModeEnabled: %v", err)
	}
	if !studio {
		t.Skip("studio mode is off; enabling it would rearrange the operator's OBS window")
	}

	original, err := client.GetCurrentPreviewScene()
	if err != nil {
		t.Fatalf("GetCurrentPreviewScene: %v", err)
	}
	t.Cleanup(func() {
		if err := client.SetCurrentPreviewScene(original); err != nil {
			t.Logf("warning: could not restore preview scene %q: %v", original, err)
		}
	})

	scene := fmt.Sprintf("agentic-obs-preview-%d", time.Now().UnixNano())
	if err := client.CreateScene(scene); err != nil {
		t.Fatalf("CreateScene: %v", err)
	}
	t.Cleanup(func() {
		if err := client.RemoveScene(scene); err != nil {
			t.Logf("warning: could not remove scratch scene %q: %v", scene, err)
		}
	})

	sink := recordingSink{events: make(chan obs.Event, 32)}
	client.SetEventSink(sink)

	if err := client.SetCurrentPreviewScene(scene); err != nil {
		t.Fatalf("SetCurrentPreviewScene: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-sink.events:
			if e.Type != obs.EventTypePreviewSceneChanged {
				continue
			}
			if got := e.Payload["scene_name"]; got != scene {
				continue
			}
			return
		case <-deadline:
			t.Fatal("no preview scene event arrived within 5s; CurrentPreviewSceneChanged " +
				"is in the Scenes subscription category -- check the mask includes it")
		}
	}
}
