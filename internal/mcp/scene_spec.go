package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// CaptureSceneSpecInput is the input for capturing a scene as a document.
type CaptureSceneSpecInput struct {
	SceneName string `json:"scene_name" jsonschema:"Name of the scene to capture. Must be a scene, not a group: a group is captured as part of whichever scene places it"`

	// Both default to true, so the document that comes back can be applied.
	IncludeSettings *bool `json:"include_settings,omitempty" jsonschema:"Capture each input's settings (default true). Set false for a lighter document when you only want the layout -- one browser source's settings can dwarf every placement in the scene"`
	IncludeFilters  *bool `json:"include_filters,omitempty" jsonschema:"Capture the filters on each source (default true). Filters attach to scenes and groups as well as inputs"`
}

// handleCaptureSceneSpec turns a scene into a document and returns it inline.
//
// The document separates sources from placements, because OBS does: an input is
// a shared object and a scene item is one reference to it. In this collection
// one source is placed twice in a single scene and eleven are shared across up
// to five scenes, so a flat list of placements carrying their own settings
// cannot round-trip.
//
// It is returned inline rather than stored. A spec's real home is the caller's
// git repository next to the code that builds the scene, and a storage table
// can come later without changing what this returns. (FB-82)
func (s *Server) handleCaptureSceneSpec(ctx context.Context, request *mcpsdk.CallToolRequest, input CaptureSceneSpecInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("capture_scene_spec", "Capture scene spec", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	if strings.TrimSpace(input.SceneName) == "" {
		return fail(fmt.Errorf("scene_name is required"))
	}

	opts := scenespec.FullCapture()
	if input.IncludeSettings != nil {
		opts.IncludeSettings = *input.IncludeSettings
	}
	if input.IncludeFilters != nil {
		opts.IncludeFilters = *input.IncludeFilters
	}

	log.Printf("Capturing scene spec for %s", input.SceneName)

	spec, err := scenespec.CaptureWith(s.obsClient, input.SceneName, opts)
	if err != nil {
		// GetSceneItemList answers InvalidResourceType (602) for a group, so
		// this is the path a capture aimed at one takes. Naming the likely
		// cause saves a round trip, without claiming to know it.
		return fail(fmt.Errorf("capturing scene %q: %w. "+
			"If this is a group rather than a scene, capture the scene that places it instead",
			input.SceneName, err))
	}

	result := map[string]interface{}{
		"spec":         spec,
		"scene":        spec.Scene,
		"source_count": len(spec.Sources),
		"item_count":   len(spec.Items),
	}
	if len(spec.Omitted) > 0 {
		result["note"] = fmt.Sprintf(
			"This capture omitted %s, so it describes the layout but cannot be applied. "+
				"Capture again with the defaults for a complete document.",
			strings.Join(spec.Omitted, " and "))
	}

	s.recordAction("capture_scene_spec", "Capture scene spec", input, result, true, time.Since(start))
	return nil, result, nil
}

// DiffSceneSpecInput is the input for comparing a spec against a live scene.
type DiffSceneSpecInput struct {
	Spec      *scenespec.Spec `json:"spec" jsonschema:"A spec document, as returned by capture_scene_spec"`
	SceneName string          `json:"scene_name,omitempty" jsonschema:"Scene to compare against. Defaults to the scene the spec was captured from"`
}

// handleDiffSceneSpec reports what differs between a spec and a scene.
//
// It is a read: nothing here changes OBS. The findings are exactly what an
// apply would act on, so a diff is also the dry run.
//
// Most of the work is in *not* reporting things. A settings value equal to its
// kind's default is absent from GetInputSettings, a transform that went out as
// float64 comes back rounded to float32, and anything the spec does not manage
// shifts every index below it. Each of those reads as drift to a naive
// comparison, and a diff that cries wolf is ignored exactly when it matters.
// (FB-83)
func (s *Server) handleDiffSceneSpec(ctx context.Context, request *mcpsdk.CallToolRequest, input DiffSceneSpecInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("diff_scene_spec", "Diff scene spec", input, nil, false, time.Since(start))
		return nil, nil, err
	}

	if input.Spec == nil {
		return fail(fmt.Errorf("spec is required; capture one with capture_scene_spec"))
	}

	scene := strings.TrimSpace(input.SceneName)
	if scene == "" {
		scene = input.Spec.Scene
	}
	if scene == "" {
		return fail(fmt.Errorf("scene_name is required: this spec does not name the scene it came from"))
	}

	log.Printf("Diffing scene spec against %s", scene)

	findings, err := scenespec.Diff(s.obsClient, input.Spec, scene)
	if err != nil {
		return fail(fmt.Errorf("diffing scene %q: %w", scene, err))
	}

	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Kind]++
	}

	result := map[string]interface{}{
		"scene":    scene,
		"findings": findings,
		"count":    len(findings),
		"by_kind":  counts,
		"matches":  len(findings) == 0,
	}
	if len(findings) == 0 {
		result["note"] = "The scene matches the spec."
	} else {
		result["note"] = "drift is a managed value that moved; missing is in the spec " +
			"and not the scene; unmanaged is in the scene and not the spec, and an " +
			"apply leaves it alone; kind_mismatch needs the source recreated, which " +
			"destroys its placements; renamed is the same source under a new name."
	}

	s.recordAction("diff_scene_spec", "Diff scene spec", input, result, true, time.Since(start))
	return nil, result, nil
}
