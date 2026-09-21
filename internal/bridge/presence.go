package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultPresenceTTL keeps a probe result briefly so that a tool call does not
// pay for a round trip it just made.
const DefaultPresenceTTL = 5 * time.Second

// Status describes whether the bridge is reachable right now.
//
// "Right now" is the operative part: scripts are stored per scene collection,
// so switching collections unloads the bridge with no warning, and a user can
// remove it through the Scripts dialog at any time.
type Status struct {
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// probeLua asks the bridge to name its protocol version. bridge_version is set
// by the prelude inside the sandbox, so a reply proves the whole path works --
// the write landed, the signal fired, a chunk ran, and the answer came back.
const probeLua = "return bridge_version"

type presenceCache struct {
	mu     sync.Mutex
	status Status
	at     time.Time
	valid  bool
}

// Probe reports the bridge's presence, re-running at most once per
// PresenceTTL.
//
// A TTL rather than an event subscription: a collection switch is not the only
// way the bridge disappears, and removing it from the Scripts dialog announces
// nothing at all.
func (t *Transport) Probe(ctx context.Context) Status {
	if t == nil {
		return Status{Detail: "bridge not configured"}
	}

	ttl := t.PresenceTTL
	if ttl <= 0 {
		ttl = DefaultPresenceTTL
	}
	now := t.now()

	t.presence.mu.Lock()
	if t.presence.valid && now.Sub(t.presence.at) < ttl {
		cached := t.presence.status
		t.presence.mu.Unlock()
		return cached
	}
	t.presence.mu.Unlock()

	status := t.probeNow(ctx)

	t.presence.mu.Lock()
	t.presence.status = status
	t.presence.at = now
	t.presence.valid = true
	t.presence.mu.Unlock()

	return status
}

// Invalidate forces the next Probe to ask OBS again. Callers use it after a
// failed command, because a failure is evidence the cached answer is stale.
func (t *Transport) Invalidate() {
	t.presence.mu.Lock()
	t.presence.valid = false
	t.presence.mu.Unlock()
}

func (t *Transport) probeNow(ctx context.Context) Status {
	result, err := t.Run(ctx, probeLua, nil)
	if err != nil {
		return Status{Present: false, Detail: err.Error()}
	}
	if !result.OK {
		return Status{Present: false, Detail: fmt.Sprintf("the bridge answered but failed: %s", result.Err)}
	}
	return Status{Present: true, Version: fmt.Sprintf("%v", result.Value)}
}

func (t *Transport) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}
