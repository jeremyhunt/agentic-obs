package mcp

import (
	"github.com/ironystock/agentic-obs/internal/obs"
)

// OBSClient is what the Server needs from OBS: every role, because the Server is
// the composition root and registers tools across the whole surface.
//
// It is a composition rather than a list. The list ran to 76 hand-maintained
// method signatures, which meant a capability could be added to the client and
// never reach a consumer, or be declared here and exist nowhere else. Roles are
// declared once in internal/obs/roles.go, next to the client that implements
// them, and consumers narrower than the Server take only the roles they use --
// see automation.OBSClient.
//
// Do not add a method signature here. Add it to a role; a test enforces this.
type OBSClient interface {
	obs.Connector
	obs.SceneReader
	obs.SceneWriter
	obs.SceneItemReader
	obs.SceneItemWriter
	obs.InputReader
	obs.InputConfigurer
	obs.AudioController
	obs.FilterManager
	obs.OutputController
	obs.StudioController
	obs.TransitionController
	obs.HotkeyTrigger
	obs.Screenshotter
	obs.StatusReader
	obs.CanvasReader
	obs.ScenePresetOperator
	obs.AdvancedSceneSwitcherController
	obs.VendorCaller
	obs.RawRequester
	obs.EventSource
}

// Verify that obs.Client implements OBSClient at compile time
var _ OBSClient = (*obs.Client)(nil)
