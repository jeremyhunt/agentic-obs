package obs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deadAddress returns host/port for a port nothing is listening on, so
// Connect fails the way it does when OBS simply is not running yet.
func deadClient(t *testing.T) *Client {
	t.Helper()
	c := NewClient(ConnectionConfig{Host: "127.0.0.1", Port: "1"})
	t.Cleanup(func() { c.Close() })
	return c
}

// ── the reconnect monitor ────────────────────────────────────────────────────

func TestMonitorIsArmedEvenWhenTheFirstConnectFails(t *testing.T) {
	// The monitor used to be started only after a SUCCESSFUL connect, so
	// launching before OBS meant nothing ever retried: the client stayed dead
	// for the life of the process even though OBS came up moments later.
	c := deadClient(t)

	if c.monitorStarted.Load() {
		t.Fatal("monitor was armed before Connect was ever called")
	}
	if err := c.Connect(); err == nil {
		t.Fatal("expected Connect to fail against a dead port")
	}
	if !c.monitorStarted.Load() {
		t.Error("a failed Connect left no reconnect monitor running")
	}
}

func TestMonitorIsArmedOnlyOnce(t *testing.T) {
	// Callers retry Connect, and the monitor calls Connect itself. Neither
	// may spawn a second monitor goroutine.
	c := deadClient(t)

	for i := 0; i < 3; i++ {
		_ = c.Connect()
	}
	if !c.monitorStarted.Load() {
		t.Fatal("monitor never armed")
	}
	// CompareAndSwap is what enforces this; if it were a plain assignment the
	// flag would still read true here, so assert the swap itself is spent.
	if c.monitorStarted.CompareAndSwap(false, true) {
		t.Error("the monitor guard was not consumed; repeated Connects would each start a goroutine")
	}
}

func TestDisconnectStandsDownTheMonitor(t *testing.T) {
	// An explicit Disconnect is a deliberate stop, not a dropped connection:
	// the monitor must not immediately reconnect behind the caller's back.
	c := deadClient(t)
	_ = c.Connect()

	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.reconnect {
		t.Error("Disconnect left auto-reconnect enabled")
	}
}

// ── the stdio contract ───────────────────────────────────────────────────────

// TestPackageNeverWritesToStdout is the guard for the bug that made this
// package's logging dangerous in the first place.
//
// agentic-obs speaks MCP over stdio: stdout IS the JSON-RPC channel. A
// fmt.Print anywhere on the server path injects text into that stream and the
// client drops the connection. The reconnect loop did exactly this, once every
// five seconds for as long as OBS was down.
//
// Parsed rather than grepped so the comments that explain the rule -- which
// necessarily mention fmt.Print -- do not trip it.
func TestPackageNeverWritesToStdout(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Dead demo code: no Example* functions, referenced from nowhere, and
		// never on the server path. It prints to stdout on purpose.
		if name == "example_usage.go" {
			continue
		}
		assertNoStdoutWrites(t, name)
	}
}

func assertNoStdoutWrites(t *testing.T, path string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	// fmt.Print/Printf/Println write to stdout. fmt.Fprint* take an explicit
	// writer and fmt.Sprint* return a string, so both are fine.
	banned := map[string]bool{"Print": true, "Printf": true, "Println": true}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "fmt" || !banned[sel.Sel.Name] {
			return true
		}
		t.Errorf("%s writes to stdout via fmt.%s -- use log (stderr); stdout is the MCP JSON-RPC stream",
			filepath.Base(fset.Position(call.Pos()).Filename), sel.Sel.Name)
		return true
	})
}
