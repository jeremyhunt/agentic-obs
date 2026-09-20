package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRegisteredServer builds a Server with the given tool groups enabled and
// registers its handlers on a real SDK server, without touching storage, OBS or
// HTTP. This is the only place in the tests that exercises registerToolHandlers;
// everything else calls handlers directly.
func newRegisteredServerWith(t *testing.T, configure func(*Server)) *Server {
	t.Helper()

	mock := testutil.NewMockOBSClient()
	mock.Connect()

	s := &Server{
		obsClient:  mock,
		ctx:        context.Background(),
		toolGroups: DefaultToolGroupConfig(),
	}
	if configure != nil {
		configure(s)
	}
	s.mcpServer = mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "agentic-obs-test", Version: "test"},
		nil,
	)
	s.registerToolHandlers()

	return s
}

func newRegisteredServer(t *testing.T, groups ToolGroupConfig) *Server {
	t.Helper()
	return newRegisteredServerWith(t, func(s *Server) { s.toolGroups = groups })
}

// servedToolNames connects an in-memory client and asks the server what tools it
// actually serves. This is the ground truth: not what a constant claims, but what
// registerToolHandlers put on the wire.
func servedToolNames(t *testing.T, s *Server) []string {
	t.Helper()

	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	serverSession, err := s.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	var names []string
	for tool, err := range clientSession.Tools(ctx, nil) {
		require.NoError(t, err)
		names = append(names, tool.Name)
	}
	return names
}

// declaredToolNames returns every tool name the metadata claims, for the groups
// the server currently has enabled, plus the always-on meta tools.
//
// Group enablement is read through the server's own getGroupEnabled rather than a
// switch here: a switch would be one more hand-maintained list of group names,
// which is the failure mode this whole test file exists to prevent. (FB-52)
func declaredToolNames(s *Server) []string {
	var names []string
	for _, group := range ToolGroupOrder {
		meta, ok := toolGroupMetadata[group]
		if !ok {
			continue
		}
		if s.getGroupEnabled(group) {
			names = append(names, meta.ToolNames...)
		}
	}
	return append(names, MetaToolNames...)
}

// TestRegisteredToolsMatchMetadata is the guard that makes every hand-maintained
// tool count redundant: it compares the tools the server actually serves against
// what toolGroupMetadata declares. Adding an AddTool call without a ToolNames
// entry (or the reverse) fails here. (FB-52)
func TestRegisteredToolsMatchMetadata(t *testing.T) {
	s := newRegisteredServer(t, DefaultToolGroupConfig())

	served := servedToolNames(t, s)
	declared := declaredToolNames(s)

	assertSameToolSets(t, declared, served)
}

// assertSameToolSets reports the symmetric difference between two tool lists.
// testify's ElementsMatch dumps both lists in full, which for an 83-tool surface
// buries the two names that actually differ. (FB-52)
func assertSameToolSets(t *testing.T, declared, served []string) {
	t.Helper()

	inDeclared := make(map[string]bool, len(declared))
	for _, n := range declared {
		inDeclared[n] = true
	}
	inServed := make(map[string]bool, len(served))
	for _, n := range served {
		inServed[n] = true
	}

	var registeredNotDeclared, declaredNotRegistered []string
	for _, n := range served {
		if !inDeclared[n] {
			registeredNotDeclared = append(registeredNotDeclared, n)
		}
	}
	for _, n := range declared {
		if !inServed[n] {
			declaredNotRegistered = append(declaredNotRegistered, n)
		}
	}
	sort.Strings(registeredNotDeclared)
	sort.Strings(declaredNotRegistered)

	assert.Empty(t, registeredNotDeclared,
		"registered with mcpsdk.AddTool but missing from toolGroupMetadata ToolNames "+
			"(add them in internal/mcp/tool_config.go): %v", registeredNotDeclared)
	assert.Empty(t, declaredNotRegistered,
		"declared in toolGroupMetadata but never registered "+
			"(add the mcpsdk.AddTool call in internal/mcp/tools.go, or remove the name): %v",
		declaredNotRegistered)

	// Duplicate registrations would otherwise hide inside a set comparison.
	assert.Equal(t, len(inServed), len(served), "a tool was registered more than once")
}

// TestHelpToolCountMatchesRegisteredTools ties the documented total to reality.
func TestHelpToolCountMatchesRegisteredTools(t *testing.T) {
	s := newRegisteredServer(t, DefaultToolGroupConfig())

	served := servedToolNames(t, s)

	assert.Equal(t, len(served), HelpToolCount,
		"HelpToolCount must equal the number of tools actually registered; "+
			"update internal/mcp/help_content.go")
}

// TestToolGroupGatingIsReal disables one group at a time and asserts that
// exactly that group's tools disappear. ADR-004 promised group gating; until now
// nothing verified it end to end. (FB-52)
func TestToolGroupGatingIsReal(t *testing.T) {
	for _, group := range ToolGroupOrder {
		t.Run(group+" disabled", func(t *testing.T) {
			s := newRegisteredServerWith(t, func(s *Server) {
				s.setGroupEnabled(group, false)
			})

			served := servedToolNames(t, s)

			assertSameToolSets(t, declaredToolNames(s), served)

			for _, tool := range toolGroupMetadata[group].ToolNames {
				assert.NotContains(t, served, tool,
					"%s is disabled, so %s must not be served", group, tool)
			}

			// Meta tools can never be disabled.
			for _, meta := range MetaToolNames {
				assert.Contains(t, served, meta, "meta tool %s must always be served", meta)
			}
		})
	}
}
