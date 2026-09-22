//go:build obslive

// The bridge's contract, against a real OBS with the script loaded.
//
// The sandbox tests are the reason this file exists. CGO is out (ADR-001), so
// Go cannot reach the Lua directly -- and a sandbox asserted against a mock
// proves nothing about the interpreter that enforces it. Shipping
// "return os == nil" over the real transport tests the real one.
//
// The real one is LuaJIT 2.1, not vanilla Lua 5.1: OBS ships it as lua51.dll
// because it is Lua 5.1 ABI-compatible. That difference is not cosmetic --
// TestLiveBridgeBoundsRunawayChunks below failed the first time this file was
// run against a real OBS, because a LuaJIT trace does not check the count hook
// the budget is built from, and reviewing the bridge against the Lua 5.1
// manual could not have caught it.
package bridge_test

import (
	"context"
	"fmt"
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

	// _G would hand back the real global table and undo the whole sandbox;
	// coroutine.wrap is a way to run code the count hook cannot interrupt;
	// rawequal/newproxy reach the metatable machinery the raw* family is kept
	// out for; collectgarbage can stall the render thread on its own.
	//
	// ffi, jit and bit are LuaJIT's, and the interpreter is LuaJIT. ffi is the
	// one that matters: ffi.cdef plus ffi.load reach any DLL on the machine,
	// which is worse than the os.execute this list already withholds.
	for _, name := range []string{"os", "io", "package", "require", "dofile", "loadfile", "loadstring", "load", "debug", "setfenv", "getfenv", "getmetatable", "setmetatable", "rawset", "rawget", "_G", "coroutine", "rawequal", "collectgarbage", "newproxy", "ffi", "jit", "bit"} {
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

// set_value is the most intricate code in the bridge and the least reachable
// from Go: no CGO (ADR-001) means no unit test can execute a line of it. The
// tests from here down are the only verification it will ever get, so they go
// after the encoder's edges rather than its happy path.

// A table is the shape any non-trivial chunk returns, and every level of it
// goes through a different branch of set_value: obs_data_set_obj for the
// table, and the scalar setters for what it holds.
func TestLiveBridgeRoundTripsANestedTable(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), `
		return {
			name = "outer",
			count = 3,
			inner = { flag = true, leaf = { text = "bottom" } },
		}
	`, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.OK {
		t.Fatalf("the chunk failed: %s", got.Err)
	}

	top, ok := got.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("result = %#v, want an object", got.Value)
	}
	if top["name"] != "outer" {
		t.Errorf("name = %#v, want \"outer\"", top["name"])
	}
	// Numbers cross as doubles: obs_data_set_double on the way out, JSON on
	// the way back.
	if top["count"] != 3.0 {
		t.Errorf("count = %#v, want 3", top["count"])
	}

	inner, ok := top["inner"].(map[string]interface{})
	if !ok {
		t.Fatalf("inner = %#v, want an object", top["inner"])
	}
	if inner["flag"] != true {
		t.Errorf("inner.flag = %#v, want true", inner["flag"])
	}

	leaf, ok := inner["leaf"].(map[string]interface{})
	if !ok {
		t.Fatalf("inner.leaf = %#v, want an object", inner["leaf"])
	}
	if leaf["text"] != "bottom" {
		t.Errorf("inner.leaf.text = %#v, want \"bottom\"", leaf["text"])
	}
}

// MAX_DEPTH is 8 and the top-level value sits at depth 0, so eight nested
// tables are the last that fit and the ninth is refused. A cap that silently
// truncated instead would hand back a result that reads like success.
func TestLiveBridgeEnforcesMaxDepth(t *testing.T) {
	transport := liveTransport(t)

	// nest(n) builds n tables, one inside the next.
	nest := func(n int) string {
		return fmt.Sprintf(`
			local top = {}
			local cur = top
			for _ = 1, %d do cur.next = {}; cur = cur.next end
			return top
		`, n-1)
	}

	t.Run("eight levels fit", func(t *testing.T) {
		got, err := transport.Run(context.Background(), nest(8), nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !got.OK {
			t.Fatalf("eight levels was refused: %s", got.Err)
		}
	})

	t.Run("nine levels are refused", func(t *testing.T) {
		got, err := transport.Run(context.Background(), nest(9), nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got.OK {
			t.Fatalf("nine levels reported success: %#v", got.Value)
		}
		if !strings.Contains(got.Err, "nested") {
			t.Errorf("error = %q, want it to name the depth cap", got.Err)
		}
	})
}

// MAX_BYTES is 64 KB, counted across values AND keys. The key case is the
// point: counting only values let a chunk return one table with an enormous
// key and sail past the cap while still reading as success.
func TestLiveBridgeEnforcesMaxBytes(t *testing.T) {
	transport := liveTransport(t)

	cases := []struct {
		name string
		lua  string
	}{
		{"a huge value", `return string.rep("x", 70000)`},
		{"a huge key", `return { [string.rep("k", 70000)] = 1 }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := transport.Run(context.Background(), tc.lua, nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got.OK {
				t.Fatal("a result over the byte cap reported success")
			}
			if !strings.Contains(got.Err, "bytes") {
				t.Errorf("error = %q, want it to name the byte cap", got.Err)
			}
		})
	}
}

// A value obs_data has no setter for is refused rather than stringified.
// Returning "function: 0x...", which is what tostring would give, would be a
// result that looks like data and is not.
//
// A function stands in for the whole class. Userdata would test the same
// branch of set_value, and every userdata reachable from the sandbox is an OBS
// object that would have to be released -- leaking one per test run is the
// worse trade for the same coverage.
func TestLiveBridgeRefusesValuesItCannotSend(t *testing.T) {
	transport := liveTransport(t)

	got, err := transport.Run(context.Background(), "return function() end", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.OK {
		t.Fatalf("a function came back as a result: %#v", got.Value)
	}
	if !strings.Contains(got.Err, "function") {
		t.Errorf("error = %q, want it to name what could not be sent", got.Err)
	}
}

// The mailbox is a source, and obs_source_update MERGES settings, so a reply
// that does not write a key leaves the previous reply's value under it. A
// failing call after a successful one therefore used to carry the earlier
// call's result, which the Go side reads on every reply -- a failure that
// arrives holding somebody else's data.
func TestLiveBridgeFailureDoesNotCarryThePreviousResult(t *testing.T) {
	transport := liveTransport(t)

	first, err := transport.Run(context.Background(), `return { token = "from-the-first-call" }`, nil)
	if err != nil {
		t.Fatalf("Run (first): %v", err)
	}
	if !first.OK {
		t.Fatalf("the first chunk failed: %s", first.Err)
	}
	if _, ok := first.Value.(map[string]interface{}); !ok {
		t.Fatalf("the first result = %#v, want an object to be left behind", first.Value)
	}

	second, err := transport.Run(context.Background(), `error("the second call fails")`, nil)
	if err != nil {
		t.Fatalf("Run (second): %v", err)
	}
	if second.OK {
		t.Fatal("a chunk that raised reported success")
	}
	if carried, ok := second.Value.(map[string]interface{}); ok {
		t.Fatalf("a failed call carried a result payload: %#v", carried)
	}
	if text, ok := second.Value.(string); ok && text != "" {
		t.Errorf("a failed call carried the stale result %q", text)
	}
}
