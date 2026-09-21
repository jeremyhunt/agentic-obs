//go:build obslive

// The bridge's contract, against a real OBS with the script loaded.
//
// The sandbox tests are the reason this file exists. CGO is out (ADR-001), so
// Go cannot reach the Lua directly -- and a sandbox asserted against a mock
// proves nothing about Lua 5.1. Shipping "return os == nil" over the real
// transport tests the real interpreter.
package bridge_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ironystock/agentic-obs/internal/bridge"
	"github.com/ironystock/agentic-obs/internal/obs"
)

func liveTransport(t *testing.T) *bridge.Transport {
	t.Helper()
	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1")
	}

	client := obs.NewClient(obs.ConnectionConfig{
		Host:     envOr("OBS_HOST", "localhost"),
		Port:     envOr("OBS_PORT", "4455"),
		Password: os.Getenv("OBS_PASSWORD"),
	})

	transport := bridge.New(client)
	client.SetEventSink(transport)

	if err := client.Connect(); err != nil {
		t.Fatalf("could not reach OBS: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect() })

	if status := transport.Probe(context.Background()); !status.Present {
		t.Skipf("the bridge is not loaded (%s). Run 'agentic-obs install-bridge' and restart OBS.", status.Detail)
	}
	return transport
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestLiveBridgeRunsCode(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "return 6 * 7", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.OK {
		t.Fatalf("the chunk failed: %s", got.Err)
	}
	if got.Value != 42.0 {
		t.Errorf("result = %#v, want 42", got.Value)
	}
}

func TestLiveBridgeReceivesArgsAsDataNotCode(t *testing.T) {
	transport := liveTransport(t)

	// A value that would be code if it were spliced into the source. It must
	// come back as the string it is.
	hostile := `]] .. os.execute("echo pwned") .. [[`
	got, err := transport.Run(context.Background(),
		"return args.value", map[string]interface{}{"value": hostile})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.OK {
		t.Fatalf("the chunk failed: %s", got.Err)
	}
	if got.Value != hostile {
		t.Errorf("args round-tripped as %#v, want the literal string", got.Value)
	}
}

// The sandbox, asserted in the interpreter that enforces it.
func TestLiveBridgeSandboxWithholdsTheMachine(t *testing.T) {
	transport := liveTransport(t)

	for _, name := range []string{"os", "io", "package", "require", "dofile", "loadfile", "loadstring", "load", "debug", "setfenv", "getfenv", "getmetatable", "setmetatable", "rawset", "rawget"} {
		t.Run(name, func(t *testing.T) {
			got, err := transport.Run(context.Background(), "return "+name+" == nil", nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !got.OK {
				t.Fatalf("the chunk failed: %s", got.Err)
			}
			if got.Value != true {
				t.Errorf("%s is reachable from inside the sandbox", name)
			}
		})
	}
}

func TestLiveBridgeStillReachesOBS(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "return type(obslua.obs_get_version_string)", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Value != "function" {
		t.Fatalf("obslua is not reachable: %#v (%s)", got.Value, got.Err)
	}
}

// A runaway loop must come back as a failure rather than hanging OBS. If this
// test times out instead of failing, the budget is not being enforced and the
// render thread is wedged.
func TestLiveBridgeBoundsRunawayChunks(t *testing.T) {
	transport := liveTransport(t)
	transport.Timeout = 20 * time.Second

	got, err := transport.Run(context.Background(), "while true do end", nil)
	if err != nil {
		t.Fatalf("Run: %v -- the budget did not stop the loop", err)
	}
	if got.OK {
		t.Fatal("an infinite loop reported success")
	}
	if !strings.Contains(got.Err, "budget") {
		t.Errorf("error = %q, want it to name the budget", got.Err)
	}
}

func TestLiveBridgeReportsCompileErrors(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "this is not lua", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.OK {
		t.Fatal("invalid Lua reported success")
	}
	if got.Err == "" {
		t.Error("a compile failure came back with no message")
	}
}
