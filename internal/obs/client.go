package obs

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/events/subscriptions"
	"github.com/andreykaipov/goobs/api/requests/general"
)

// Client wraps the OBS WebSocket client with connection management and state tracking.
type Client struct {
	// Connection configuration
	host               string
	port               string
	password           string
	eventSubscriptions int

	// OBS client instance
	client *goobs.Client

	// Connection state
	mu             sync.RWMutex
	connected      bool
	reconnect      bool        // Whether to attempt auto-reconnect
	monitorStarted atomic.Bool // Guards the single monitorConnection goroutine

	// Event handlers
	eventSink EventSink

	// Context for managing lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// ConnectionConfig holds the parameters needed to connect to OBS.
type ConnectionConfig struct {
	Host     string
	Port     string
	Password string

	// EventSubscriptions is the obs-websocket event mask to connect with.
	// Zero means "unset" and takes DefaultEventSubscriptions(); to receive no
	// events at all, pass subscriptions.None explicitly via a non-zero mask
	// combination, or simply register no callback.
	EventSubscriptions int
}

// DefaultEventSubscriptions is the event mask the client connects with unless a
// ConnectionConfig overrides it.
//
// A category is excluded when it is high-volume and unused: obs-websocket will
// push SceneItemTransformChanged for every frame of a drag, and
// InputVolumeMeters tens of times a second, whether or not anyone is listening.
//
// Filters and Vendors are included ahead of any handler, which is deliberate and
// worth stating because it inverts the usual order. An unsubscribed category is
// not an error anywhere -- no log line, no failure, just an event that never
// arrives -- so a handler written for one looks correct forever while doing
// nothing. Both are low-frequency (a filter toggle, a plugin emitting), so the
// cost of subscribing early is nil and the cost of forgetting is a defect that
// takes a live OBS to find.
func DefaultEventSubscriptions() int {
	return subscriptions.Scenes | // Scene creation, removal, and switching
		subscriptions.Outputs | // Recording, Streaming, VirtualCam, ReplayBuffer
		subscriptions.Inputs | // Audio mute/volume changes
		subscriptions.SceneItems | // Source visibility changes
		subscriptions.Transitions | // Scene transition events
		subscriptions.Ui | // Studio mode changes
		subscriptions.Filters | // Filter enable/settings changes
		subscriptions.Vendors // VendorEvent: the only inbound channel from a plugin
}

// EventCallback is the interface for handling OBS events and triggering MCP notifications.
type EventCallback interface {
	// Scene events
	OnSceneCreated(sceneName string)
	OnSceneRemoved(sceneName string)
	OnCurrentProgramSceneChanged(sceneName string)

	// Recording events
	OnRecordingStarted()
	OnRecordingStopped(outputPath string)
	OnRecordingPaused()
	OnRecordingResumed()
	OnRecordingFileChanged(newOutputPath string)

	// Streaming events
	OnStreamingStarted()
	OnStreamingStopped()

	// Virtual camera events
	OnVirtualCamStarted()
	OnVirtualCamStopped()

	// Replay buffer events
	OnReplayBufferSaved(savedPath string)

	// Input events
	OnInputMuteChanged(inputName string, muted bool)

	// Scene item events
	OnSceneItemVisibilityChanged(sceneName string, sceneItemId int, visible bool)

	// Transition events
	OnTransitionStarted(transitionName string)

	// Studio mode events
	OnStudioModeChanged(enabled bool)
}

// NewClient creates a new OBS client with the specified connection configuration.
// The client is not connected until Connect() is called.
func NewClient(config ConnectionConfig) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	subs := config.EventSubscriptions
	if subs == 0 {
		subs = DefaultEventSubscriptions()
	}

	return &Client{
		host:               config.Host,
		port:               config.Port,
		password:           config.Password,
		eventSubscriptions: subs,
		connected:          false,
		reconnect:          true,
		ctx:                ctx,
		cancel:             cancel,
	}
}

// SetEventCallback registers a callback handler for OBS events.
// This should be called before Connect() to ensure no events are missed.
//
// Deprecated: prefer SetEventSink. EventCallback has one method per event kind,
// so every new kind widens it and breaks every implementer -- which is why
// categories this client subscribes to still have no handler. Callbacks
// registered here are adapted to a sink and keep working.
func (c *Client) SetEventCallback(callback EventCallback) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if callback == nil {
		c.eventSink = nil
		return
	}
	c.eventSink = callbackSink{cb: callback}
}

// SetEventSink registers a sink for OBS events. Call it before Connect so no
// events are missed.
func (c *Client) SetEventSink(sink EventSink) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eventSink = sink
}

// Connect establishes a connection to OBS WebSocket server.
// Returns an error if the connection fails.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil // Already connected
	}

	// Arm the reconnect monitor on the FIRST attempt, before it can fail.
	//
	// It used to be started further down, only after a connection succeeded,
	// so a first attempt against an OBS that wasn't running yet left nothing
	// watching: the client stayed dead until the process restarted, even once
	// OBS came up seconds later. The compare-and-swap keeps repeated Connect
	// calls (the caller's retries, and the monitor's own) from spawning more
	// goroutines.
	if c.reconnect && c.monitorStarted.CompareAndSwap(false, true) {
		go c.monitorConnection()
	}

	address := fmt.Sprintf("%s:%s", c.host, c.port)

	var client *goobs.Client
	var err error

	// Build connection options. The mask is decided once, at construction, so
	// a reconnect cannot quietly come back with a different subscription set
	// than the one the caller asked for.
	opts := []goobs.Option{
		goobs.WithEventSubscriptions(c.eventSubscriptions),
	}

	if c.password != "" {
		opts = append(opts, goobs.WithPassword(c.password))
	}

	client, err = goobs.New(address, opts...)
	if err != nil {
		return fmt.Errorf("OBS connection failed. Is OBS Studio running with WebSocket server enabled? Error: %w", err)
	}

	c.client = client
	c.connected = true

	// Set up event subscriptions
	if err := c.setupEventHandlers(); err != nil {
		c.client.Disconnect()
		c.client = nil
		c.connected = false
		return fmt.Errorf("failed to set up OBS event handlers: %w", err)
	}

	// The reconnect monitor is armed at the top of this method, not here --
	// see the comment there for why.

	return nil
}

// Disconnect closes the connection to OBS WebSocket server.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Stand the monitor down FIRST, before the already-disconnected shortcut.
	//
	// Disconnect means "stop, and stay stopped". Clearing this only on the
	// connected path meant that a client which had never managed to connect
	// ignored Disconnect entirely and kept retrying in the background -- which
	// went unnoticed while the monitor was armed only after a SUCCESSFUL
	// connect, and became reachable the moment it was armed on the first
	// attempt instead.
	c.reconnect = false

	if !c.connected {
		return nil // Already disconnected
	}

	if c.client != nil {
		if err := c.client.Disconnect(); err != nil {
			return fmt.Errorf("failed to disconnect from OBS: %w", err)
		}
		c.client = nil
	}

	c.connected = false
	return nil
}

// Close performs a graceful shutdown of the client, disconnecting and cleaning up resources.
func (c *Client) Close() error {
	c.cancel() // Cancel context to stop background goroutines
	return c.Disconnect()
}

// IsConnected returns true if the client is currently connected to OBS.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// GetConnectionStatus returns detailed connection status information.
func (c *Client) GetConnectionStatus() (ConnectionStatus, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := ConnectionStatus{
		Connected: c.connected,
		Host:      c.host,
		Port:      c.port,
	}

	if !c.connected {
		return status, nil
	}

	// Get OBS version and additional status info
	versionResp, err := c.client.General.GetVersion()
	if err != nil {
		// Connection might be broken
		return status, fmt.Errorf("failed to get OBS version (connection may be broken): %w", err)
	}

	status.OBSVersion = versionResp.ObsVersion
	status.WebSocketVersion = versionResp.ObsWebSocketVersion
	status.Platform = versionResp.Platform

	return status, nil
}

// HealthCheck performs a lightweight health check by pinging OBS.
// Returns nil if healthy, error if unhealthy.
func (c *Client) HealthCheck() error {
	c.mu.RLock()
	client := c.client
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return fmt.Errorf("OBS client is not connected")
	}

	// Use GetVersion as a lightweight health check
	_, err := client.General.GetVersion()
	if err != nil {
		return fmt.Errorf("OBS health check failed: %w", err)
	}

	return nil
}

// monitorConnection runs in the background and attempts to reconnect if the connection is lost.
func (c *Client) monitorConnection() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return // Client is shutting down
		case <-ticker.C:
			// Check if we need to reconnect
			c.mu.RLock()
			shouldReconnect := c.reconnect && !c.connected
			c.mu.RUnlock()

			if shouldReconnect {
				// Attempt reconnection.
				//
				// log (stderr), never fmt.Print (stdout): this process speaks
				// MCP over stdio, so anything written to stdout lands in the
				// JSON-RPC stream. With OBS down this branch runs every 5s,
				// which made a dropped connection corrupt the protocol
				// continuously rather than just once.
				if err := c.Connect(); err != nil {
					log.Printf("Auto-reconnect failed: %v", err)
				} else {
					log.Println("Successfully reconnected to OBS")
				}
			} else {
				// Perform health check on connected client
				c.mu.RLock()
				isConnected := c.connected
				c.mu.RUnlock()

				if isConnected {
					if err := c.HealthCheck(); err != nil {
						// Mark as disconnected so we can try to reconnect
						c.mu.Lock()
						c.connected = false
						if c.client != nil {
							c.client.Disconnect()
							c.client = nil
						}
						c.mu.Unlock()
						log.Printf("OBS connection lost: %v", err)
					}
				}
			}
		}
	}
}

// setupEventHandlers subscribes to relevant OBS events and sets up handlers.
func (c *Client) setupEventHandlers() error {
	// Events are subscribed to via WithEventSubscriptions option during client creation
	// We just need to start listening for events
	go c.handleEvents()

	return nil
}

// handleEvents translates incoming OBS events and hands them to the sink.
//
// The translation itself lives in eventFrom, which is a pure function and
// therefore testable; this loop is only plumbing. It used to be a ~70-line
// switch inline here, unreachable by any test that did not have OBS running.
func (c *Client) handleEvents() {
	for raw := range c.client.IncomingEvents {
		c.mu.RLock()
		sink := c.eventSink
		c.mu.RUnlock()

		if sink == nil {
			continue // Nothing registered
		}

		// Events with no handler are dropped here rather than at the sink. The
		// subscription mask is deliberately wider than the set translated below
		// (FB-62), so this is the normal path for filter and vendor events, not
		// an error.
		event, ok := eventFrom(raw, time.Now())
		if !ok {
			continue
		}

		sink.HandleEvent(event)
	}
}

// CallVendorRequest issues a generic obs-websocket CallVendorRequest. Vendor
// requests are how third-party OBS plugins (e.g. Advanced Scene Switcher) expose
// their own request types over the obs-websocket protocol. `data` may be nil for
// requests with no parameters; the returned map mirrors the vendor's response
// payload (which may be empty for fire-and-forget vendors).
func (c *Client) CallVendorRequest(vendorName, requestType string, data map[string]any) (map[string]any, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	params := general.NewCallVendorRequestParams().
		WithVendorName(vendorName).
		WithRequestType(requestType)
	if data != nil {
		params = params.WithRequestData(data)
	}

	resp, err := client.General.CallVendorRequest(params)
	if err != nil {
		return nil, fmt.Errorf("CallVendorRequest(%s/%s) failed: %w", vendorName, requestType, err)
	}
	return resp.ResponseData, nil
}

// getClient is a helper that returns the client if connected, or an error if not.
func (c *Client) getClient() (*goobs.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("not connected to OBS. Call Connect() first")
	}

	return c.client, nil
}

// ConnectionStatus holds detailed information about the OBS connection.
type ConnectionStatus struct {
	Connected        bool   `json:"connected"`
	Host             string `json:"host"`
	Port             string `json:"port"`
	OBSVersion       string `json:"obs_version,omitempty"`
	WebSocketVersion string `json:"websocket_version,omitempty"`
	Platform         string `json:"platform,omitempty"`
}
