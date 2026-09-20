//go:build obslive

// The capture, run against the collection it was designed from.
//
// Every shape this package exists to handle is present in that collection and
// absent from a tidy fixture: a source placed twice in one scene, eight groups
// sitting beside ten nested-scene placements, containers carrying filters, and
// sources shared across five scenes. The unit tests pin the behaviour; this
// says the behaviour is the one a real OBS produces.
//
// It is read-only. It captures and asserts, and writes nothing.
package scenespec_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs"
	"github.com/ironystock/agentic-obs/internal/scenespec"
)

func liveClient(t *testing.T) *obs.Client {
	t.Helper()

	if os.Getenv("OBS_LIVE_TEST") == "" {
		t.Skip("live OBS tests are opt-in: set OBS_LIVE_TEST=1")
	}

	host, port := envOr("OBS_HOST", "localhost"), envOr("OBS_PORT", "4455")
	client := obs.NewClient(obs.ConnectionConfig{
		Host: host, Port: port, Password: os.Getenv("OBS_PASSWORD"),
	})
	if err := client.Connect(); err != nil {
		t.Fatalf("could not reach OBS at %s:%s: %v", host, port, err)
	}
	t.Cleanup(func() { _ = client.Disconnect() })

	return client
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestLiveCaptureEveryScene(t *testing.T) {
	client := liveClient(t)

	scenes, _, err := client.GetSceneList()
	if err != nil {
		t.Fatalf("GetSceneList: %v", err)
	}
	if len(scenes) == 0 {
		t.Skip("no scenes on the main canvas")
	}

	groups, err := client.GetGroupList()
	if err != nil {
		t.Fatalf("GetGroupList: %v", err)
	}
	isGroup := map[string]bool{}
	for _, g := range groups {
		isGroup[g] = true
	}

	var (
		totalSources, totalItems        int
		sawGroup, sawNested, sawRepeat  bool
		sawContainerFilter, sawSettings bool
	)

	for _, name := range scenes {
		spec, err := scenespec.Capture(client, name)
		if err != nil {
			t.Errorf("capturing %q: %v", name, err)
			continue
		}

		totalSources += len(spec.Sources)
		totalItems += len(spec.Items)

		// A source is captured once however often it is placed. This is the
		// property a flat model cannot have, and the collection exercises it.
		seen := map[string]int{}
		for _, s := range spec.Sources {
			seen[s.Name]++
		}
		for sourceName, n := range seen {
			if n > 1 {
				t.Errorf("%s: source %q captured %d times", name, sourceName, n)
			}
		}

		placements := map[string]int{}
		for _, item := range spec.Items {
			placements[item.Source]++
			if _, ok := seen[item.Source]; !ok {
				t.Errorf("%s: placement of %q has no source entry", name, item.Source)
			}
		}
		for sourceName, n := range placements {
			if n > 1 {
				sawRepeat = true
				// Repeated placements must be distinguishable.
				occ := map[int]bool{}
				for _, item := range spec.Items {
					if item.Source == sourceName {
						occ[item.Occurrence] = true
					}
				}
				if len(occ) != n {
					t.Errorf("%s: %q is placed %d times but has %d distinct occurrence "+
						"indices", name, sourceName, n, len(occ))
				}
			}
		}

		for _, s := range spec.Sources {
			switch s.Type {
			case scenespec.SourceGroup:
				sawGroup = true
				if !isGroup[s.Name] {
					t.Errorf("%s: %q captured as a group but GetGroupList does not list it",
						name, s.Name)
				}
				if s.Kind != "" || len(s.Settings) > 0 {
					t.Errorf("%s: group %q captured a kind or settings; it has neither",
						name, s.Name)
				}

			case scenespec.SourceScene:
				sawNested = true
				if isGroup[s.Name] {
					t.Errorf("%s: %q is a group but was captured as a nested scene -- "+
						"both report OBS_SOURCE_TYPE_SCENE and only isGroup separates them",
						name, s.Name)
				}
				if s.Kind != "" || len(s.Settings) > 0 {
					t.Errorf("%s: nested scene %q captured a kind or settings", name, s.Name)
				}

			case scenespec.SourceInput:
				if s.Kind == "" {
					t.Errorf("%s: input %q captured with no kind; an apply could not "+
						"create it", name, s.Name)
				}
				if len(s.Settings) > 0 {
					sawSettings = true
				}
			}

			if len(s.Filters) > 0 && s.Type != scenespec.SourceInput {
				sawContainerFilter = true
			}
		}

		if _, err := json.Marshal(spec); err != nil {
			t.Errorf("%s: spec does not encode: %v", name, err)
		}
	}

	t.Logf("captured %d scenes: %d source entries, %d placements",
		len(scenes), totalSources, totalItems)
	t.Logf("shapes exercised: group=%v nested=%v repeated placement=%v "+
		"filter on a container=%v input settings=%v",
		sawGroup, sawNested, sawRepeat, sawContainerFilter, sawSettings)

	// The collection is known to hold all of these. If one stops appearing, the
	// capture has started missing it rather than the collection having changed
	// -- and either way the assertion is the thing that says so.
	if !sawGroup {
		t.Error("no group was captured, though GetGroupList reports some")
	}
	if !sawNested {
		t.Error("no nested scene was captured")
	}
	if !sawRepeat {
		t.Error("no source was captured with more than one placement")
	}
}

func TestLiveCaptureReadsEachSourceOnce(t *testing.T) {
	client := liveClient(t)

	// The expensive calls are per source, not per placement. A scene with ten
	// placements of two sources must cost two settings reads, or capturing a
	// collection scales with placements and a layered stack becomes slow for
	// no reason.
	scenes, _, err := client.GetSceneList()
	if err != nil || len(scenes) == 0 {
		t.Fatalf("GetSceneList: %v", err)
	}

	// A fresh counter per scene. Sharing one across the search totals every
	// scene tried and reports a number that has nothing to do with the scene
	// finally measured.
	var target string
	var spec *scenespec.Spec
	var counter *countingReader
	for _, name := range scenes {
		c := &countingReader{Reader: client}
		s, err := scenespec.Capture(c, name)
		if err != nil {
			continue
		}
		if len(s.Items) > len(s.Sources) {
			target, spec, counter = name, s, c
			break
		}
	}
	if spec == nil {
		t.Skip("no scene on this collection places a source more than once")
	}

	if counter.settings > len(spec.Sources) {
		t.Errorf("%s: %d settings reads for %d sources (%d placements); sources must "+
			"be read once each", target, counter.settings, len(spec.Sources), len(spec.Items))
	}
	t.Logf("%s: %d placements, %d sources, %d settings reads",
		target, len(spec.Items), len(spec.Sources), counter.settings)
}

// countingReader counts the per-source reads a capture makes.
type countingReader struct {
	scenespec.Reader
	settings int
}

func (c *countingReader) GetSourceSettings(name string) (map[string]interface{}, error) {
	c.settings++
	return c.Reader.GetSourceSettings(name)
}
