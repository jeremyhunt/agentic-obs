package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
)

// A missing OBS must be distinguishable from a broken server, because the two
// call for opposite responses: carry on serving vs. give up. main.go decides
// which by testing for ErrOBSUnavailable, so the wrapping is a contract, not a
// detail. Before this existed main.go could only treat every startup failure
// as fatal, and an MCP client started before OBS got a dead server for its
// whole session.

func TestConnectOBSWrapsFailureAsUnavailable(t *testing.T) {
	mock := testutil.NewMockOBSClient()
	mock.ErrorOnConnect = errors.New("dial tcp 127.0.0.1:4455: connection refused")

	s := &Server{obsClient: mock}

	err := s.ConnectOBS()
	if err == nil {
		t.Fatal("expected ConnectOBS to fail when the client cannot connect")
	}
	if !errors.Is(err, ErrOBSUnavailable) {
		t.Errorf("error does not identify itself as ErrOBSUnavailable: %v", err)
	}
	// The underlying cause has to survive: "OBS is not reachable" alone does
	// not tell an operator whether it is the wrong port or the wrong password.
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("wrapped error lost the underlying cause: %q", err)
	}
}

func TestConnectOBSSucceedsQuietly(t *testing.T) {
	s := &Server{obsClient: testutil.NewMockOBSClient()}

	if err := s.ConnectOBS(); err != nil {
		t.Fatalf("expected a clean connect, got %v", err)
	}
}

func TestOBSUnavailableIsNotMistakenForOtherFailures(t *testing.T) {
	// Guards the other direction: a local subsystem failure must NOT match, or
	// main.go would keep serving through a genuinely broken startup.
	if errors.Is(errors.New("failed to start HTTP server: port in use"), ErrOBSUnavailable) {
		t.Error("an unrelated startup error matched ErrOBSUnavailable")
	}
}
