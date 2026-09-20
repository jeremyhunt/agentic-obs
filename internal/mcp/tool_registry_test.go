package mcp

import (
	"context"
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
func newRegisteredServer(t *testing.T, groups ToolGroupConfig) *Server {
	t.Helper()

	mock := testutil.NewMockOBSClient()
	mock.Connect()

	s := &Server{
		obsClient:  mock,
		ctx:        context.Background(),
		toolGroups: groups,
	}
	s.mcpServer = mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "agentic-obs-test", Version: "test"},
		nil,
	)
	s.registerToolHandlers()

	return s
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
// that are enabled, plus the always-on meta tools.
func declaredToolNames(groups ToolGroupConfig) []string {
	var names []string
	for _, group := range ToolGroupOrder {
		meta, ok := toolGroupMetadata[group]
		if !ok {
			continue
		}
		enabled := false
		switch group {
		case "Core":
			enabled = groups.Core
		case "Sources":
			enabled = groups.Sources
		case "Audio":
			enabled = groups.Audio
		case "Layout":
			enabled = groups.Layout
		case "Visual":
			enabled = groups.Visual
		case "Design":
			enabled = groups.Design
		case "Filters":
			enabled = groups.Filters
		case "Transitions":
			enabled = groups.Transitions
		case "Automation":
			enabled = groups.Automation
		}
		if enabled {
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
	declared := declaredToolNames(DefaultToolGroupConfig())

	assert.ElementsMatch(t, declared, served,
		"tools served over MCP must match toolGroupMetadata + MetaToolNames")
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
			groups := DefaultToolGroupConfig()
			switch group {
			case "Core":
				groups.Core = false
			case "Sources":
				groups.Sources = false
			case "Audio":
				groups.Audio = false
			case "Layout":
				groups.Layout = false
			case "Visual":
				groups.Visual = false
			case "Design":
				groups.Design = false
			case "Filters":
				groups.Filters = false
			case "Transitions":
				groups.Transitions = false
			case "Automation":
				groups.Automation = false
			}

			served := servedToolNames(t, newRegisteredServer(t, groups))

			assert.ElementsMatch(t, declaredToolNames(groups), served,
				"disabling %s must remove exactly that group's tools", group)

			// Meta tools can never be disabled.
			for _, meta := range MetaToolNames {
				assert.Contains(t, served, meta, "meta tool %s must always be served", meta)
			}
		})
	}
}
