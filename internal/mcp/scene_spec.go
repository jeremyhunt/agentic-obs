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

	Fields []string `json:"fields,omitempty" jsonschema:"Aspects to compare: source, placement, kind, settings, filters, transform, enabled, locked, blend_mode, order. Omit for all of them"`
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

	findings, err := scenespec.DiffWith(s.obsClient, input.Spec, scene,
		scenespec.DiffOptions{Fields: input.Fields})
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
	if len(input.Fields) > 0 {
		// Said before anything else, because "matches" below means "matches in
		// the aspects that were compared" and would otherwise read as more.
		result["fields"] = input.Fields
	}
	if len(findings) == 0 {
		result["note"] = "The scene matches the spec."
		if len(input.Fields) > 0 {
			result["note"] = fmt.Sprintf(
				"The scene matches the spec in %s. Nothing else was compared.",
				strings.Join(input.Fields, ", "))
		}
	} else {
		result["note"] = "drift is a managed value that moved; missing is in the spec " +
			"and not the scene; unmanaged is in the scene and not the spec, and an " +
			"apply leaves it alone; kind_mismatch needs the source recreated, which " +
			"destroys its placements; renamed is the same source under a new name."
	}

	s.recordAction("diff_scene_spec", "Diff scene spec", input, result, true, time.Since(start))
	return nil, result, nil
}

// ApplySceneSpecInput is the input for reconciling a scene to a spec.
type ApplySceneSpecInput struct {
	Spec      *scenespec.Spec `json:"spec" jsonschema:"A spec document, as returned by capture_scene_spec"`
	SceneName string          `json:"scene_name,omitempty" jsonschema:"Scene to apply to. Defaults to the scene the spec was captured from"`

	DryRun      *bool  `json:"dry_run,omitempty" jsonschema:"Plan without writing. DEFAULTS TO TRUE: pass false to actually change the scene"`
	OnUnmanaged string `json:"on_unmanaged,omitempty" jsonschema:"What to do with things the scene has and the spec does not: keep (default), hide, or remove. remove takes out scene items only and never the sources behind them"`

	Fields []string `json:"fields,omitempty" jsonschema:"Aspects to reconcile: source, placement, kind, settings, filters, transform, enabled, locked, blend_mode, order. Omit for all of them. fields=[enabled] is a visibility preset"`
}

// handleApplySceneSpec reconciles a scene to a spec.
//
// dry_run defaults to **true** here, where the library function defaults it to
// false. The difference is deliberate: a tool call is a decision made by a model
// reading a description, and the scene may be on air. Planning first and saying
// what would change costs one extra call; writing to a live scene by accident
// costs a broadcast.
//
// The report carries the scene as it was, so applying that document puts it
// back. (FB-84)
func (s *Server) handleApplySceneSpec(ctx context.Context, request *mcpsdk.CallToolRequest, input ApplySceneSpecInput) (*mcpsdk.CallToolResult, any, error) {
	start := time.Now()

	fail := func(err error) (*mcpsdk.CallToolResult, any, error) {
		s.recordAction("apply_scene_spec", "Apply scene spec", input, nil, false, time.Since(start))
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

	dryRun := true
	if input.DryRun != nil {
		dryRun = *input.DryRun
	}

	switch input.OnUnmanaged {
	case "", scenespec.UnmanagedKeep, scenespec.UnmanagedHide, scenespec.UnmanagedRemove:
	default:
		return fail(fmt.Errorf("on_unmanaged must be keep, hide or remove; got %q", input.OnUnmanaged))
	}

	log.Printf("Applying scene spec to %s (dry_run=%v)", scene, dryRun)

	report, err := scenespec.Apply(ctx, s.obsClient, input.Spec, scene, scenespec.ApplyOptions{
		DryRun:      dryRun,
		OnUnmanaged: input.OnUnmanaged,
		Fields:      input.Fields,
	})
	if err != nil {
		return fail(fmt.Errorf("applying to scene %q: %w", scene, err))
	}

	counts := map[string]int{}
	for _, op := range report.Ops {
		counts[op.Result]++
	}

	result := map[string]interface{}{
		"scene":     report.Scene,
		"dry_run":   report.DryRun,
		"ops":       report.Ops,
		"by_result": counts,
		"before":    report.Before,
	}
	if report.DryRun {
		result["note"] = "Nothing was changed. Pass dry_run=false to apply these operations."
	} else {
		result["note"] = "`before` is the scene as it was; applying that document undoes this."
	}
	if counts[scenespec.OpFailed] > 0 {
		result["note"] = fmt.Sprintf("%d operation(s) failed; the rest were applied. %v",
			counts[scenespec.OpFailed], result["note"])
	}

	s.recordAction("apply_scene_spec", "Apply scene spec", input, result, true, time.Since(start))
	return nil, result, nil
}
