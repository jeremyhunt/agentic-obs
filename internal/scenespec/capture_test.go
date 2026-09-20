package scenespec_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs/obstest"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// A capture has to survive the collection it was written for, so the fixture is
// built to match its shapes rather than a convenient minimum: an input placed
// twice in one scene, an input shared with another scene, a nested scene, a
// group with children of its own, and filters on a source that is not an input.
//
// Those are not hypothetical. The live collection places jurmiey_avatar twice in
// Game, shares eleven sources across up to five scenes, and holds eight groups
// alongside its nested scenes.
func fixture(t *testing.T) (*obstest.Fake, string) {
	t.Helper()
	f := obstest.NewFake()

	const scene = "Game"
	for _, s := range []string{scene, "Other", "AI_AVATARS"} {
		if err := f.CreateScene(s); err != nil {
			t.Fatalf("CreateScene %s: %v", s, err)
		}
	}

	// An input, placed twice in the same scene.
	if _, err := f.CreateInput(scene, "jurmiey_avatar", "image_source",
		map[string]interface{}{"file": "avatar.png"}); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}
	if _, err := f.CreateSceneItem(scene, "jurmiey_avatar", true); err != nil {
		t.Fatalf("second placement: %v", err)
	}

	// An input shared with another scene entirely.
	if _, err := f.CreateInput(scene, "OVERLAY_NowPlaying", "browser_source",
		map[string]interface{}{"url": "http://localhost:8791/now"}); err != nil {
		t.Fatalf("CreateInput: %v", err)
	}
	if _, err := f.CreateSceneItem("Other", "OVERLAY_NowPlaying", true); err != nil {
		t.Fatalf("share into Other: %v", err)
	}

	// A nested scene, with a filter on it -- a scene is a source, so it can
	// carry filters exactly as an input does.
	if _, err := f.CreateSceneItem(scene, "AI_AVATARS", true); err != nil {
		t.Fatalf("nest AI_AVATARS: %v", err)
	}
	if err := f.CreateSourceFilter("AI_AVATARS", "Room haze", "color_filter_v2",
		map[string]interface{}{"opacity": 40}); err != nil {
		t.Fatalf("CreateSourceFilter on a scene: %v", err)
	}

	// A group with two children of its own.
	if err := f.CreateGroup("MERCH"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := f.CreateInput("MERCH", "shop-border", "image_source", nil); err != nil {
		t.Fatalf("CreateInput into the group: %v", err)
	}
	if _, err := f.CreateInput("MERCH", "shop-products", "browser_source", nil); err != nil {
		t.Fatalf("CreateInput into the group: %v", err)
	}
	if _, err := f.CreateSceneItem(scene, "MERCH", true); err != nil {
		t.Fatalf("place the group: %v", err)
	}

	return f, scene
}

func TestCaptureSeparatesSourcesFromPlacements(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// jurmiey_avatar is placed twice. A flat per-item model would either write
	// its settings twice -- so an apply would fight itself -- or lose a
	// placement. Sources are captured once; placements are captured per item.
	if got := countSources(spec, "jurmiey_avatar"); got != 1 {
		t.Errorf("jurmiey_avatar appears %d times in sources, want 1: a source is a "+
			"shared object and must be captured once however often it is placed", got)
	}
	if got := countItems(spec, "jurmiey_avatar"); got != 2 {
		t.Errorf("jurmiey_avatar has %d placements, want 2", got)
	}

	// Two placements of one source need to be told apart by something stable.
	occurrences := map[int]bool{}
	for _, item := range spec.Items {
		if item.Source == "jurmiey_avatar" {
			occurrences[item.Occurrence] = true
			if item.SceneItemID == 0 {
				t.Errorf("placement of %s has no scene item id", item.Source)
			}
		}
	}
	if len(occurrences) != 2 {
		t.Errorf("the two placements share an occurrence index: %v", occurrences)
	}
}

func TestCaptureRecordsWhatEachKindOfSourceActuallyHas(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	input := findSource(t, spec, "jurmiey_avatar")
	if input.Type != scenespec.SourceInput {
		t.Errorf("jurmiey_avatar captured as %q, want %q", input.Type, scenespec.SourceInput)
	}
	if input.Kind != "image_source" {
		t.Errorf("jurmiey_avatar kind is %q; without it an apply cannot create the input", input.Kind)
	}
	if input.Settings["file"] != "avatar.png" {
		t.Errorf("jurmiey_avatar settings are %v", input.Settings)
	}

	// A nested scene is not an input: obs-websocket answers InvalidResourceType
	// (602) for GetInputSettings on one, so there is no kind and no settings to
	// capture. It is a reference to a scene captured in its own right.
	nested := findSource(t, spec, "AI_AVATARS")
	if nested.Type != scenespec.SourceScene {
		t.Errorf("AI_AVATARS captured as %q, want %q", nested.Type, scenespec.SourceScene)
	}
	if nested.Kind != "" || len(nested.Settings) != 0 {
		t.Errorf("AI_AVATARS captured a kind (%q) or settings (%v); a scene has neither",
			nested.Kind, nested.Settings)
	}
	// It does carry filters, though.
	if len(nested.Filters) != 1 || nested.Filters[0].Name != "Room haze" {
		t.Errorf("filters on the nested scene are %v; a scene is a source and carries "+
			"them exactly as an input does", nested.Filters)
	}

	group := findSource(t, spec, "MERCH")
	if group.Type != scenespec.SourceGroup {
		t.Errorf("MERCH captured as %q, want %q -- a group reports the same sourceType "+
			"as a nested scene and is told apart only by isGroup", group.Type, scenespec.SourceGroup)
	}
	if len(group.Items) != 2 {
		t.Errorf("MERCH captured %d children, want 2; a group's contents come from "+
			"GetGroupSceneItemList, which is the only call that will open one", len(group.Items))
	}
}

func TestCaptureTakesOnlyWhatTheSceneReferences(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// OVERLAY_NowPlaying is placed in this scene and in Other. Capturing the
	// scene must record the source it references, and must not reach into the
	// other scene's placements -- those belong to that scene's spec.
	if countSources(spec, "OVERLAY_NowPlaying") != 1 {
		t.Errorf("a source shared with another scene was not captured")
	}
	if got := countItems(spec, "OVERLAY_NowPlaying"); got != 1 {
		t.Errorf("OVERLAY_NowPlaying has %d placements in this spec, want 1; the "+
			"placement in Other belongs to Other's spec", got)
	}

	// The group's children are sources of the group, not placements in the
	// scene. Listing them at the top level would make an apply put them
	// directly in the scene.
	if countItems(spec, "shop-border") != 0 {
		t.Errorf("a group's child was captured as a scene placement")
	}
}

func TestCapturePreservesPlacementOrder(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Order is z-order, and the established convention in this collection is a
	// stack of full-canvas layers where order is the only thing distinguishing
	// them. A spec that lost it would render the scene wrong while every
	// placement looked right.
	for i, item := range spec.Items {
		if item.Order != i {
			t.Errorf("item %d (%s) reports order %d", i, item.Source, item.Order)
		}
	}
}

func TestCaptureIsJSONAndCarriesItsVersion(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if spec.Version == 0 {
		t.Error("spec has no version; a stored document with no version cannot be migrated")
	}
	if spec.Scene != scene {
		t.Errorf("spec names scene %q, want %q", spec.Scene, scene)
	}

	encoded, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatalf("a spec must be a document: %v", err)
	}
	// The document is the agent-facing artefact, so its field names are the
	// snake_case the rest of the MCP surface uses.
	for _, want := range []string{`"version"`, `"scene"`, `"sources"`, `"items"`, `"scene_item_id"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("encoded spec has no %s field:\n%s", want, encoded)
		}
	}
}

func TestCaptureRefusesAGroup(t *testing.T) {
	f, _ := fixture(t)

	// GetSceneItemList answers InvalidResourceType (602) for a group, so a
	// capture aimed at one has to say so rather than return an empty document
	// that looks like an empty scene.
	if _, err := scenespec.Capture(f, "MERCH"); err == nil {
		t.Error("capturing a group succeeded; a group is not a scene and must be " +
			"captured as part of the scene that places it")
	}
}

func countSources(spec *scenespec.Spec, name string) int {
	n := 0
	for _, s := range spec.Sources {
		if s.Name == name {
			n++
		}
	}
	return n
}

func countItems(spec *scenespec.Spec, name string) int {
	n := 0
	for _, i := range spec.Items {
		if i.Source == name {
			n++
		}
	}
	return n
}

func findSource(t *testing.T, spec *scenespec.Spec, name string) scenespec.SourceSpec {
	t.Helper()
	for _, s := range spec.Sources {
		if s.Name == name {
			return s
		}
	}
	names := make([]string, 0, len(spec.Sources))
	for _, s := range spec.Sources {
		names = append(names, s.Name)
	}
	t.Fatalf("source %q is not in the spec; it holds %v", name, names)
	return scenespec.SourceSpec{}
}

func TestCaptureSaysWhatItLeftOut(t *testing.T) {
	f, scene := fixture(t)

	// A lighter document is useful for reading a layout: a browser source's
	// settings can dwarf everything else in the scene. But a spec captured
	// without settings cannot be applied, and this project's recurring defect
	// is output that looks complete while omitting half its subject. So the
	// document records what was skipped.
	spec, err := scenespec.CaptureWith(f, scene, scenespec.Options{
		IncludeSettings: false,
		IncludeFilters:  false,
	})
	if err != nil {
		t.Fatalf("CaptureWith: %v", err)
	}

	if len(spec.Omitted) == 0 {
		t.Fatal("a partial capture claims to be complete; nothing records that " +
			"settings and filters were skipped")
	}
	got := strings.Join(spec.Omitted, ",")
	for _, want := range []string{"settings", "filters"} {
		if !strings.Contains(got, want) {
			t.Errorf("omitted is %v, missing %q", spec.Omitted, want)
		}
	}

	input := findSource(t, spec, "jurmiey_avatar")
	if len(input.Settings) != 0 {
		t.Errorf("settings were captured despite IncludeSettings=false: %v", input.Settings)
	}
	nested := findSource(t, spec, "AI_AVATARS")
	if len(nested.Filters) != 0 {
		t.Errorf("filters were captured despite IncludeFilters=false: %v", nested.Filters)
	}

	// Placements are the point of the document and are never optional.
	if len(spec.Items) == 0 {
		t.Error("a partial capture dropped the placements too")
	}
}

func TestCaptureDefaultsToEverything(t *testing.T) {
	f, scene := fixture(t)

	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(spec.Omitted) != 0 {
		t.Errorf("a full capture reports omissions: %v", spec.Omitted)
	}
}
