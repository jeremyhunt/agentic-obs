package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/ironystock/agentic-obs/internal/obs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// listeningPair is connectedPair plus the resources/list_changed notifications
// the client actually received. Asserting on what arrives at a real client
// session is the only way to tell a notification apart from a log line claiming
// one was sent -- which is what was there before. (FB-66)
func listeningPair(t *testing.T) (*Server, *mcpsdk.ClientSession, <-chan struct{}) {
	t.Helper()

	mock := testutil.NewMockOBSClient()
	mock.Connect()

	s := &Server{
		obsClient:  mock,
		ctx:        context.Background(),
		toolGroups: DefaultToolGroupConfig(),
	}
	s.mcpServer = mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "agentic-obs-test", Version: "test"},
		&mcpsdk.ServerOptions{
			SubscribeHandler:   s.handleSubscribe,
			UnsubscribeHandler: s.handleUnsubscribe,
		},
	)
	s.registerResourceHandlers()

	listChanged := make(chan struct{}, 32)
	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "test-client", Version: "test"},
		&mcpsdk.ClientOptions{
			ResourceListChangedHandler: func(context.Context, *mcpsdk.ResourceListChangedRequest) {
				select {
				case listChanged <- struct{}{}:
				default:
				}
			},
		},
	)

	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	serverSession, err := s.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { serverSession.Close() })

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { clientSession.Close() })

	return s, clientSession, listChanged
}

func listedSceneURIs(t *testing.T, cs *mcpsdk.ClientSession) map[string]bool {
	t.Helper()

	res, err := cs.ListResources(context.Background(), nil)
	require.NoError(t, err)

	uris := map[string]bool{}
	for _, r := range res.Resources {
		uris[r.URI] = true
	}
	return uris
}

// TestScenesAreListedAsConcreteResources: a scene has to be a real resource
// before anything about its lifecycle can be reported. Served only by a URI
// template, scenes never appear in resources/list at all -- a client has to know
// a scene's name before it can ask about it.
func TestScenesAreListedAsConcreteResources(t *testing.T) {
	s, cs, _ := listeningPair(t)
	s.syncSceneResources()

	uris := listedSceneURIs(t, cs)
	for _, scene := range []string{"Scene 1", "Gaming"} {
		want := obs.GetResourceURIForScene(scene)
		if !uris[want] {
			t.Errorf("%s is not in resources/list; listed: %v", want, uris)
		}
	}
}

// TestSceneCreatedAddsAResourceAndNotifies is the defect this replaces. The
// handler logged "Scene list changed" under a comment claiming the SDK handled
// the notification "automatically when resources are added/removed dynamically",
// but nothing ever added or removed one, so no client was ever told.
func TestSceneCreatedAddsAResourceAndNotifies(t *testing.T) {
	s, cs, listChanged := listeningPair(t)
	s.syncSceneResources()
	settle(listChanged)

	s.handleOBSEventNotification(obs.EventTypeSceneCreated, map[string]interface{}{
		"scene_name": "Brand New Scene",
	})

	requireNotified(t, listChanged, "creating a scene must tell clients the resource list changed")

	if uris := listedSceneURIs(t, cs); !uris[obs.GetResourceURIForScene("Brand New Scene")] {
		t.Errorf("the new scene is not in resources/list; listed: %v", uris)
	}
}

// TestSceneRemovedDropsTheResourceAndNotifies: the other half. A resource left
// behind for a deleted scene is worse than a missing one -- reading it fails
// against OBS while the listing insists it exists.
func TestSceneRemovedDropsTheResourceAndNotifies(t *testing.T) {
	s, cs, listChanged := listeningPair(t)
	s.syncSceneResources()
	settle(listChanged)

	s.handleOBSEventNotification(obs.EventTypeSceneRemoved, map[string]interface{}{
		"scene_name": "Gaming",
	})

	requireNotified(t, listChanged, "removing a scene must tell clients the resource list changed")

	if uris := listedSceneURIs(t, cs); uris[obs.GetResourceURIForScene("Gaming")] {
		t.Errorf("the removed scene is still in resources/list; listed: %v", uris)
	}
}

// TestSyncSceneResourcesIsIdempotent: it runs on every reconnect, and the SDK
// notifies on each add. Re-syncing an unchanged scene list should not make a
// client re-fetch for nothing.
func TestSyncSceneResourcesIsIdempotent(t *testing.T) {
	s, cs, listChanged := listeningPair(t)

	s.syncSceneResources()
	settle(listChanged)
	first := listedSceneURIs(t, cs)

	s.syncSceneResources()

	// The listing is the weaker half of this: AddResource replaces by URI, so
	// re-adding never changes what is listed and a count comparison would pass
	// no matter what. The notification is the real assertion -- it is what a
	// client acts on, and a reconnect that re-announced every scene would have
	// it re-fetch all of them for nothing.
	select {
	case <-listChanged:
		t.Error("re-syncing an unchanged scene list notified clients; " +
			"every reconnect would look like the scene list had changed")
	case <-time.After(300 * time.Millisecond):
	}

	second := listedSceneURIs(t, cs)
	if len(first) != len(second) {
		t.Errorf("re-syncing changed the listing from %d to %d resources", len(first), len(second))
	}
}

// settle consumes notifications until none has arrived for a short quiet
// period.
//
// A plain non-blocking drain is not enough and reading one as a pass is how a
// test lies: notifications cross the in-memory transport asynchronously, so the
// ones from syncSceneResources can still be in flight when the drain runs, and
// a later assertion then accepts a leftover as proof that the thing under test
// notified. Caught by mutation -- removing the registration entirely left these
// tests green up to the listing check.
func settle(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		case <-time.After(250 * time.Millisecond):
			return
		}
	}
}

func requireNotified(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(msg)
	}
}
