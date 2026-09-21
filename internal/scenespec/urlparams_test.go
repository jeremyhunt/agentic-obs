package scenespec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ironystock/agentic-obs/internal/obs/obstest"

	"github.com/ironystock/agentic-obs/internal/scenespec"
)

// A browser source's URL can have two writers. In this workspace the "Starting
// Soon" overlay is the case: a setup script owns the page and its quad
// geometry, while the streaming dashboard owns "text" and "until" and writes
// them by rewriting the same URL. Either writer rewriting the whole URL loses
// the other's work, so both need a way to say "keep these, they are not mine".
//
// Every test below is about not losing something, or about not churning
// something that did not change -- because a URL that comes back different on
// every write reads as permanent drift, and a spec that always drifts is one
// nobody applies.

func urlOf(t *testing.T, settings map[string]interface{}) string {
	t.Helper()
	u, ok := settings["url"].(string)
	if !ok {
		t.Fatalf("settings have no url: %#v", settings)
	}
	return u
}

func TestPreserveCarriesAParamTheCallerDidNotSet(t *testing.T) {
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/page.html?quad=1,2"},
		map[string]interface{}{"url": "http://x/page.html?quad=9,9&text=hello"},
		[]string{"text", "until"},
	)

	if want := "http://x/page.html?quad=1,2&text=hello"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveLeavesTheCallersOwnQueryByteForByte(t *testing.T) {
	// The real URLs carry commas, pipes and a fixed parameter order. Round-
	// tripping them through a query encoder would re-sort the keys and escape
	// the commas, and the resulting string would differ from the spec's on
	// every single apply.
	caller := "file:///G:/w/index.html?quad=0,0,1920,0,1920,1080,0,1080&grade=1&queue=0"

	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": caller},
		map[string]interface{}{"url": "file:///G:/w/index.html?until=1700000000000"},
		[]string{"until"},
	)

	if want := caller + "&until=1700000000000"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveDoesNotOverrideAParamTheCallerSet(t *testing.T) {
	// Naming a parameter means "carry it over if I did not say". Saying it here
	// and now is more specific than what happens to be live, and carrying the
	// live value as well would put the key in the URL twice.
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/?text=mine"},
		map[string]interface{}{"url": "http://x/?text=theirs"},
		[]string{"text"},
	)

	if want := "http://x/?text=mine"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveIsIdempotent(t *testing.T) {
	// The second apply must be a no-op. If preserving appended again, a spec
	// would grow its URL by one parameter every time it was applied.
	spec := map[string]interface{}{"url": "http://x/?quad=1"}
	live := map[string]interface{}{"url": "http://x/?quad=1&text=hi"}

	once := scenespec.PreserveURLParams(spec, live, []string{"text"})
	twice := scenespec.PreserveURLParams(once, map[string]interface{}{"url": urlOf(t, once)}, []string{"text"})

	if urlOf(t, once) != urlOf(t, twice) {
		t.Errorf("applying twice changed the url: %q then %q", urlOf(t, once), urlOf(t, twice))
	}
}

func TestPreserveAddsAQuestionMarkWhenTheCallerHasNoQuery(t *testing.T) {
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/page.html"},
		map[string]interface{}{"url": "http://x/page.html?text=hi"},
		[]string{"text"},
	)

	if want := "http://x/page.html?text=hi"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveKeepsTheFragment(t *testing.T) {
	// Appending to the end of the string would put the parameter inside the
	// fragment, where the page never sees it.
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/page.html?a=1#top"},
		map[string]interface{}{"url": "http://x/page.html?text=hi"},
		[]string{"text"},
	)

	if want := "http://x/page.html?a=1&text=hi#top"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveEscapesTheValueItCarries(t *testing.T) {
	// The dashboard writes "text=A|B|C" as three rotating lines.
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/?a=1"},
		map[string]interface{}{"url": "http://x/?text=A%7CB%7CC"},
		[]string{"text"},
	)

	if want := "http://x/?a=1&text=A%7CB%7CC"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveCarriesEveryValueOfARepeatedParam(t *testing.T) {
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/"},
		map[string]interface{}{"url": "http://x/?text=one&text=two"},
		[]string{"text"},
	)

	if want := "http://x/?text=one&text=two"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveCarriesInTheOrderTheNamesWereGiven(t *testing.T) {
	// Not in the order the live URL happens to list them, and not sorted --
	// the result has to be reproducible from the spec alone.
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/"},
		map[string]interface{}{"url": "http://x/?until=9&text=hi"},
		[]string{"text", "until"},
	)

	if want := "http://x/?text=hi&until=9"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveDoesNothingWithoutNames(t *testing.T) {
	spec := map[string]interface{}{"url": "http://x/?a=1"}
	got := scenespec.PreserveURLParams(spec, map[string]interface{}{"url": "http://x/?text=hi"}, nil)

	if want := "http://x/?a=1"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveDoesNothingWhenTheSourceIsNotYetLive(t *testing.T) {
	// ensure_input calls this on the create path too, where there is no live
	// settings object to read from.
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/?a=1"},
		nil,
		[]string{"text"},
	)

	if want := "http://x/?a=1"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveDoesNothingWhenTheLiveURLLacksTheParam(t *testing.T) {
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"url": "http://x/?a=1"},
		map[string]interface{}{"url": "http://x/?b=2"},
		[]string{"text"},
	)

	if want := "http://x/?a=1"; urlOf(t, got) != want {
		t.Errorf("url = %q, want %q", urlOf(t, got), want)
	}
}

func TestPreserveLeavesSettingsWithoutAURLAlone(t *testing.T) {
	// Every kind but browser_source. The field is harmless rather than an error
	// so a spec can name it once and apply to a scene where only some sources
	// have URLs.
	got := scenespec.PreserveURLParams(
		map[string]interface{}{"file": "C:/x.png"},
		map[string]interface{}{"url": "http://x/?text=hi"},
		[]string{"text"},
	)

	if _, ok := got["url"]; ok {
		t.Errorf("a url was invented: %#v", got)
	}
	if got["file"] != "C:/x.png" {
		t.Errorf("other settings were disturbed: %#v", got)
	}
}

func TestPreserveDoesNotMutateTheSettingsItWasGiven(t *testing.T) {
	// A spec's settings map is read again by the diff that follows an apply.
	spec := map[string]interface{}{"url": "http://x/?a=1"}
	scenespec.PreserveURLParams(spec, map[string]interface{}{"url": "http://x/?text=hi"}, []string{"text"})

	if want := "http://x/?a=1"; spec["url"] != want {
		t.Errorf("the input map was rewritten: %q", spec["url"])
	}
}

// --- the same rule, through a spec -----------------------------------------
//
// The helper above is only half the feature. A spec that names a parameter has
// to be quiet about it in a diff as well, or the source reads as drifted on
// every comparison and the apply that follows rewrites a URL that was correct.

const sharedOverlay = "OVERLAY_NowPlaying"

func sourceIn(t *testing.T, spec *scenespec.Spec, name string) *scenespec.SourceSpec {
	t.Helper()
	for i := range spec.Sources {
		if spec.Sources[i].Name == name {
			return &spec.Sources[i]
		}
	}
	t.Fatalf("no source %q in the spec", name)
	return nil
}

// dashboardWrites simulates the other writer: it appends its own parameter to
// the live URL without touching anything else.
func dashboardWrites(t *testing.T, f *obstest.Fake, param string) {
	t.Helper()
	live, err := f.GetSourceSettings(sharedOverlay)
	if err != nil {
		t.Fatalf("GetSourceSettings: %v", err)
	}
	url := live["url"].(string)
	if strings.Contains(url, "?") {
		url += "&" + param
	} else {
		url += "?" + param
	}
	if err := f.SetSourceSettings(sharedOverlay, map[string]interface{}{"url": url}, true); err != nil {
		t.Fatalf("SetSourceSettings: %v", err)
	}
}

func urlDrift(findings []scenespec.Finding) *scenespec.Finding {
	for i := range findings {
		if findings[i].Subject == sharedOverlay && findings[i].Field == "settings.url" {
			return &findings[i]
		}
	}
	return nil
}

func TestDiffReportsAForeignParamWhenNothingSaysToKeepIt(t *testing.T) {
	// The control for the test below: without the field, this is drift, which
	// is what makes the field do real work rather than nothing.
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dashboardWrites(t, f, "text=hi")

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if urlDrift(findings) == nil {
		t.Error("a URL the spec does not describe was not reported as drift")
	}
}

func TestDiffIgnoresAParamTheSpecSaysToPreserve(t *testing.T) {
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dashboardWrites(t, f, "text=hi")
	sourceIn(t, spec, sharedOverlay).PreserveURLParams = []string{"text"}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if d := urlDrift(findings); d != nil {
		t.Errorf("a preserved parameter was reported as drift: %s", d.Detail)
	}
}

func TestDiffStillReportsARealURLChangeAlongsideAPreservedParam(t *testing.T) {
	// Preserving must not blind the diff to the part of the URL the spec does
	// own. This is the failure that would make the field dangerous rather than
	// merely useless.
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dashboardWrites(t, f, "text=hi")

	source := sourceIn(t, spec, sharedOverlay)
	source.PreserveURLParams = []string{"text"}
	source.Settings["url"] = "http://localhost:8791/other"

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if urlDrift(findings) == nil {
		t.Error("a genuinely different URL was not reported")
	}
}

func TestApplyKeepsAPreservedParamWhileWritingTheRest(t *testing.T) {
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dashboardWrites(t, f, "text=hi")

	source := sourceIn(t, spec, sharedOverlay)
	source.PreserveURLParams = []string{"text"}
	source.Settings["url"] = "http://localhost:8791/other"

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	live, err := f.GetSourceSettings(sharedOverlay)
	if err != nil {
		t.Fatalf("GetSourceSettings: %v", err)
	}
	got, _ := live["url"].(string)
	if !strings.Contains(got, "/other") {
		t.Errorf("the spec's own URL was not applied: %q", got)
	}
	if !strings.Contains(got, "text=hi") {
		t.Errorf("the other writer's parameter was lost: %q", got)
	}
}

func TestApplyWithoutTheFieldDropsTheOtherWritersParam(t *testing.T) {
	// The control again. Settings are applied with overlay=false so the live
	// object matches the spec, which is right for every key the spec owns and
	// is exactly what loses a key it does not.
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dashboardWrites(t, f, "text=hi")
	sourceIn(t, spec, sharedOverlay).Settings["url"] = "http://localhost:8791/other"

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	live, _ := f.GetSourceSettings(sharedOverlay)
	if got, _ := live["url"].(string); strings.Contains(got, "text=hi") {
		t.Errorf("this test no longer demonstrates the loss it exists to show: %q", got)
	}
}

func TestApplyWithPreservedParamsHasNothingToDoOnASecondRun(t *testing.T) {
	f, scene := fixture(t)
	spec, err := scenespec.Capture(f, scene)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	dashboardWrites(t, f, "text=hi")

	source := sourceIn(t, spec, sharedOverlay)
	source.PreserveURLParams = []string{"text"}
	source.Settings["url"] = "http://localhost:8791/other"

	if _, err := scenespec.Apply(context.Background(), f, spec, scene,
		scenespec.ApplyOptions{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	findings, err := scenespec.Diff(f, spec, scene)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if d := urlDrift(findings); d != nil {
		t.Errorf("the URL still drifts after an apply that preserved it: %s", d.Detail)
	}
}
