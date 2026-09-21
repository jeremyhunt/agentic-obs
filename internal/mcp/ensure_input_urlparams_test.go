package mcp

import (
	"testing"

	"github.com/ironystock/agentic-obs/internal/mcp/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A browser source's URL can have two writers. Here the "Starting Soon" overlay
// is the case: a setup script owns the page and its geometry, and the streaming
// dashboard owns "text" and "until", which it sets by rewriting the same URL.
// Re-running the setup would drop the dashboard's parameters, so the setup names
// them and they survive.

const overlayURL = "http://localhost:8791/soon.html?quad=1"

// liveURL reads a source's URL back out of OBS.
func liveURL(t *testing.T, mock *testutil.MockOBSClient, name string) string {
	t.Helper()
	settings, err := mock.GetSourceSettings(name)
	require.NoError(t, err)
	url, _ := settings["url"].(string)
	return url
}

func TestEnsureInputPreserveURLParams(t *testing.T) {
	// seed creates the overlay and then has the dashboard append its parameter,
	// which is the state every case below starts from.
	seed := func(t *testing.T, server *Server, mock *testutil.MockOBSClient) EnsureInputInput {
		t.Helper()
		in := EnsureInputInput{
			SceneName:  "Scene 1",
			SourceName: "OVERLAY_StartingSoon",
			InputKind:  "browser_source",
			Settings:   map[string]interface{}{"url": overlayURL},
		}
		require.Equal(t, "created", ensure(t, server, in)["action"])
		require.NoError(t, mock.SetSourceSettings(in.SourceName,
			map[string]interface{}{"url": overlayURL + "&text=hi"}, true))

		// Every case below is about what happens to this parameter, so a seed
		// that quietly failed to write it would make all of them vacuous.
		require.Contains(t, liveURL(t, mock, in.SourceName), "text=hi",
			"the seed did not put the other writer's parameter on the live URL")
		return in
	}

	t.Run("keeps a parameter another writer owns", func(t *testing.T) {
		server, mock := testServer(t)
		in := seed(t, server, mock)

		in.Settings = map[string]interface{}{"url": "http://localhost:8791/soon.html?quad=2"}
		in.PreserveURLParams = []string{"text", "until"}
		assert.Equal(t, "updated", ensure(t, server, in)["action"])

		got := liveURL(t, mock, in.SourceName)
		assert.Contains(t, got, "quad=2", "the caller's own change was not applied")
		assert.Contains(t, got, "text=hi", "the other writer's parameter was lost")
	})

	t.Run("without the field the other writer's parameter is lost", func(t *testing.T) {
		// The control. If this ever stops losing the parameter, the test above
		// proves nothing.
		server, mock := testServer(t)
		in := seed(t, server, mock)

		in.Settings = map[string]interface{}{"url": "http://localhost:8791/soon.html?quad=2"}
		ensure(t, server, in)

		assert.NotContains(t, liveURL(t, mock, in.SourceName), "text=hi")
	})

	t.Run("reports unchanged when only the preserved parameter differs", func(t *testing.T) {
		// The reason this matters: a setup script re-run against a scene the
		// dashboard has since written to should report that nothing needed
		// doing. Reporting "updated" every time is what makes a status useless.
		server, mock := testServer(t)
		in := seed(t, server, mock)

		in.PreserveURLParams = []string{"text"}
		assert.Equal(t, "unchanged", ensure(t, server, in)["action"])
	})
}
