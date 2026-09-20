package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/ironystock/agentic-obs/internal/obs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectedPair wires a server and an in-memory client, returning the client
// session and a channel of resource-updated URIs the client actually received.
func connectedPair(t *testing.T) (*Server, *mcpsdk.ClientSession, <-chan string) {
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

	updates := make(chan string, 16)
	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "test-client", Version: "test"},
		&mcpsdk.ClientOptions{
			ResourceUpdatedHandler: func(_ context.Context, req *mcpsdk.ResourceUpdatedNotificationRequest) {
				updates <- req.Params.URI
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

	return s, clientSession, updates
}

// TestResourceUpdatesReachSubscribers is the end-to-end proof for FB-57.
//
// Before this, no SubscribeHandler was set. The SDK therefore advertised
// resources.subscribe=false and, because ResourceUpdated only delivers to
// sessions in the server's subscription set, every notification went nowhere --
// while the fan-out logged "Sent resource updated notification" as though it had
// worked. This test fails if that regresses, because it asserts on what the
// client received rather than on what the server believed it sent.
func TestResourceUpdatesReachSubscribers(t *testing.T) {
	ctx := context.Background()
	s, session, updates := connectedPair(t)

	uri := obs.GetResourceURIForScene("Scene 1")

	require.NoError(t, session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: uri}),
		"subscribe must be supported; without a SubscribeHandler the SDK returns method-not-found")

	require.NoError(t, s.SendResourceUpdated(ctx, uri))

	select {
	case got := <-updates:
		assert.Equal(t, uri, got)
	case <-time.After(2 * time.Second):
		t.Fatal("subscribed client never received the resource update")
	}
}

// TestResourceUpdatesOnlyReachSubscribedURIs guards against notifying everything
// to everyone, which would be the lazy way to make the test above pass.
func TestResourceUpdatesOnlyReachSubscribedURIs(t *testing.T) {
	ctx := context.Background()
	s, session, updates := connectedPair(t)

	subscribed := obs.GetResourceURIForScene("Scene 1")
	other := obs.GetResourceURIForScene("Gaming")

	require.NoError(t, session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: subscribed}))

	require.NoError(t, s.SendResourceUpdated(ctx, other))

	select {
	case got := <-updates:
		t.Fatalf("received an update for %q while only subscribed to %q", got, subscribed)
	case <-time.After(300 * time.Millisecond):
		// nothing arrived, which is correct
	}
}

// TestUnsubscribeStopsUpdates completes the lifecycle.
func TestUnsubscribeStopsUpdates(t *testing.T) {
	ctx := context.Background()
	s, session, updates := connectedPair(t)

	uri := obs.GetResourceURIForScene("Scene 1")
	require.NoError(t, session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: uri}))
	require.NoError(t, s.SendResourceUpdated(ctx, uri))

	select {
	case <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the first update while subscribed")
	}

	require.NoError(t, session.Unsubscribe(ctx, &mcpsdk.UnsubscribeParams{URI: uri}))
	require.NoError(t, s.SendResourceUpdated(ctx, uri))

	select {
	case got := <-updates:
		t.Fatalf("received %q after unsubscribing", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestVisibilityChangeMarksSceneResourceUpdated covers the mapping half: a
// scene's resource includes per-item visibility, so showing or hiding a source
// must count as a change to that scene. Only scene_changed was mapped. (FB-57)
func TestVisibilityChangeMarksSceneResourceUpdated(t *testing.T) {
	assert.True(t, obs.ShouldTriggerResourceUpdated(obs.EventTypeSceneChanged))
	assert.True(t, obs.ShouldTriggerResourceUpdated(obs.EventTypeSourceVisibilityChanged),
		"hiding a source changes the scene resource's contents")
	assert.False(t, obs.ShouldTriggerResourceUpdated(obs.EventTypeRecordingStarted),
		"output state is not part of any scene resource")
}

// TestVisibilityEventNotifiesSubscribedScene drives the real fan-out rather than
// calling SendResourceUpdated directly, so it covers the whole path: an OBS event
// arrives, the fan-out decides the scene resource changed, and a subscribed
// client is told about it.
func TestVisibilityEventNotifiesSubscribedScene(t *testing.T) {
	ctx := context.Background()
	s, session, updates := connectedPair(t)

	uri := obs.GetResourceURIForScene("Scene 1")
	require.NoError(t, session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: uri}))

	// Exactly what internal/obs emits when a source is shown or hidden.
	s.handleOBSEventNotification(obs.EventTypeSourceVisibilityChanged, map[string]interface{}{
		"scene_name":    "Scene 1",
		"scene_item_id": 1,
		"visible":       false,
	})

	select {
	case got := <-updates:
		assert.Equal(t, uri, got,
			"hiding a source must notify subscribers of that source's scene")
	case <-time.After(2 * time.Second):
		t.Fatal("no notification for a visibility change in a subscribed scene")
	}
}

// TestSubscribeRejectsURIsThatNeverEmit covers the handleSubscribe guard, which
// was written in FB-57 with no test at all.
//
// Writing the test first would have caught that the guard did not do what its
// own comment claimed. It said it rejected "URIs we will never notify about",
// but it only checked the obs:// prefix -- so obs://screenshot/x and
// obs://preset/x were accepted, and a client subscribing to either waits
// forever. Only obs://scene/{name} ever emits, because ShouldTriggerResourceUpdated
// maps scene and visibility events and nothing else. (FB-59)
func TestSubscribeRejectsURIsThatNeverEmit(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		uri       string
		wantError bool
	}{
		{"scene resource emits, so it is accepted", "obs://scene/Scene 1", false},
		{"a non-obs scheme is rejected", "file:///etc/passwd", true},
		{"http is rejected", "https://example.com", true},
		{"screenshot never emits an update, so subscribing is a silent wait", "obs://screenshot/Webcam", true},
		{"preset never emits an update either", "obs://preset/my-preset", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, session, _ := connectedPair(t)

			err := session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: tt.uri})

			if tt.wantError {
				require.Error(t, err, "subscribing to %s should be refused rather than silently never delivering", tt.uri)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
