package scenespec

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Finding kinds. The taxonomy matters because each one needs a different answer
// from an apply, and collapsing them into "different" loses that.
const (
	// FindingDrift is a managed thing whose value moved. An apply writes it back.
	FindingDrift = "drift"

	// FindingMissing is in the spec and not in the scene. An apply creates it.
	FindingMissing = "missing"

	// FindingUnmanaged is in the scene and not in the spec. An apply leaves it
	// alone by default: a scene almost always holds things the spec was never
	// meant to own, and removing them is the most destructive thing an apply
	// can do.
	FindingUnmanaged = "unmanaged"

	// FindingKindMismatch is a source whose input kind changed. No settings
	// write reconciles it -- recreating it is the only fix, and that destroys
	// every placement -- so it is never silently treated as drift.
	FindingKindMismatch = "kind_mismatch"

	// FindingRenamed is a source with the spec's uuid under a different name.
	// Without it a rename reads as one source missing and another unmanaged,
	// and an apply acting on that recreates the source and orphans the original.
	FindingRenamed = "renamed"
)

// Finding is one difference between a spec and a scene.
type Finding struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Field   string `json:"field,omitempty"`
	Detail  string `json:"detail"`

	Spec any `json:"spec,omitempty"`
	Live any `json:"live,omitempty"`
}

// DiffReader is what a diff needs: everything a capture needs, plus the
// defaults that stop a stored default reading as drift.
type DiffReader interface {
	Reader
	GetInputDefaultSettings(inputKind string) (map[string]interface{}, error)
}

// Tolerances for comparing floats.
//
// OBS stores transform values as float32 and obs-websocket sends them as JSON
// numbers, so a float64 that goes out comes back rounded. Comparing exactly
// makes every capture-then-diff noisy, and noise is indistinguishable from a
// real nudge -- which is the failure that matters, because it is the one that
// gets a diff ignored.
//
// The classes differ because the units do. A hundredth of a pixel is invisible;
// a hundredth of a scale factor is a 1% size change.
const (
	positionTolerance = 0.01 // pixels
	scaleTolerance    = 1e-4 // multiplier
	rotationTolerance = 1e-3 // degrees
)

// Diff compares a spec against the live scene and reports what differs.
//
// It is a read. Nothing here changes OBS, and the findings are what an apply
// would act on -- so a diff is also the dry run.
func Diff(client DiffReader, spec *Spec, sceneName string) ([]Finding, error) {
	if spec == nil {
		return nil, fmt.Errorf("no spec to diff")
	}
	if spec.Scene != "" && sceneName != "" && spec.Scene != sceneName {
		// Almost always a mistake, and the output would be a wall of missing
		// and unmanaged that buries whatever the caller meant to see.
		return nil, fmt.Errorf(
			"this spec was captured from scene %q, not %q; diffing across scenes "+
				"reports every placement as missing or unmanaged. Capture %q first if "+
				"that is really what you want",
			spec.Scene, sceneName, sceneName)
	}
	if len(spec.Omitted) > 0 {
		return nil, fmt.Errorf(
			"this spec omitted %s, so a diff would report everything it skipped as "+
				"matching. Capture again with the defaults",
			strings.Join(spec.Omitted, " and "))
	}

	live, err := CaptureWith(client, sceneName, FullCapture())
	if err != nil {
		return nil, err
	}

	findings := []Finding{}
	findings = append(findings, diffSources(client, spec, live)...)
	findings = append(findings, diffItems(spec, live)...)

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Subject != findings[j].Subject {
			return findings[i].Subject < findings[j].Subject
		}
		return findings[i].Field < findings[j].Field
	})
	return findings, nil
}

func diffSources(client DiffReader, spec, live *Spec) []Finding {
	findings := []Finding{}

	liveByName := map[string]SourceSpec{}
	liveByUUID := map[string]SourceSpec{}
	for _, s := range live.Sources {
		liveByName[s.Name] = s
		if s.UUID != "" {
			liveByUUID[s.UUID] = s
		}
	}

	for _, want := range spec.Sources {
		got, present := liveByName[want.Name]
		if !present {
			// Before calling it missing, check whether the same object is
			// present under another name. A rename keeps the uuid.
			if want.UUID != "" {
				if renamed, ok := liveByUUID[want.UUID]; ok {
					findings = append(findings, Finding{
						Kind:    FindingRenamed,
						Subject: want.Name,
						Detail: fmt.Sprintf(
							"the spec calls this %q; the same source (uuid %s) is now called %q",
							want.Name, want.UUID, renamed.Name),
						Spec: want.Name,
						Live: renamed.Name,
					})
					continue
				}
			}
			findings = append(findings, Finding{
				Kind:    FindingMissing,
				Subject: want.Name,
				Detail:  fmt.Sprintf("source %q is in the spec and not in the scene", want.Name),
			})
			continue
		}

		if want.Type != got.Type {
			findings = append(findings, Finding{
				Kind:    FindingKindMismatch,
				Subject: want.Name,
				Field:   "type",
				Detail: fmt.Sprintf("the spec has a %s, the scene has a %s; these are "+
					"different kinds of object and no write reconciles them", want.Type, got.Type),
				Spec: want.Type, Live: got.Type,
			})
			continue
		}

		if want.Type == SourceInput && want.Kind != got.Kind {
			findings = append(findings, Finding{
				Kind:    FindingKindMismatch,
				Subject: want.Name,
				Field:   "kind",
				Detail: fmt.Sprintf("the spec has kind %q, the scene has %q; recreating "+
					"is the only fix and it destroys every placement", want.Kind, got.Kind),
				Spec: want.Kind, Live: got.Kind,
			})
			continue
		}

		findings = append(findings, diffSettings(client, want, got)...)
		findings = append(findings, diffFilters(want, got)...)
	}

	specNames := map[string]bool{}
	for _, s := range spec.Sources {
		specNames[s.Name] = true
	}
	for _, got := range live.Sources {
		if !specNames[got.Name] && !renamedInto(findings, got.Name) {
			findings = append(findings, Finding{
				Kind:    FindingUnmanaged,
				Subject: got.Name,
				Detail: fmt.Sprintf("source %q is in the scene and not in the spec; "+
					"an apply leaves it alone", got.Name),
			})
		}
	}

	return findings
}

func renamedInto(findings []Finding, liveName string) bool {
	for _, f := range findings {
		if f.Kind == FindingRenamed && f.Live == liveName {
			return true
		}
	}
	return false
}

// diffSettings compares a source's settings, with defaults merged into both
// sides first.
//
// GetInputSettings returns only what differs from the kind's defaults, so a
// spec that stored a value *equal* to a default reads as "spec says X, live
// says nothing". Merging the defaults under both makes the two comparable, and
// it is the single most common false positive a settings diff can have: it
// fires on every source whose document mentions a default.
func diffSettings(client DiffReader, want, got SourceSpec) []Finding {
	if want.Type != SourceInput {
		return nil // scenes and groups have no settings
	}

	defaults := map[string]interface{}{}
	if d, err := client.GetInputDefaultSettings(want.Kind); err == nil {
		defaults = d
	}

	// A parameter the spec says belongs to another writer is carried onto the
	// spec's URL before comparing, so it is the same on both sides and cannot
	// read as drift. Without this the source drifts on every diff, and an apply
	// that always has work to do is one nobody trusts.
	wantSettings := PreserveURLParams(want.Settings, got.Settings, want.PreserveURLParams)

	wantFull := mergeOver(defaults, wantSettings)
	gotFull := mergeOver(defaults, got.Settings)

	findings := []Finding{}
	for _, key := range sortedKeys(wantFull) {
		wv, gv := wantFull[key], gotFull[key]
		if valuesEqual(wv, gv) {
			continue
		}
		findings = append(findings, Finding{
			Kind:    FindingDrift,
			Subject: want.Name,
			Field:   "settings." + key,
			Detail:  fmt.Sprintf("spec has %v, scene has %v", wv, gv),
			Spec:    wv, Live: gv,
		})
	}
	return findings
}

func diffFilters(want, got SourceSpec) []Finding {
	findings := []Finding{}

	gotByName := map[string]FilterSpec{}
	for _, f := range got.Filters {
		gotByName[f.Name] = f
	}

	for _, wf := range want.Filters {
		gf, ok := gotByName[wf.Name]
		if !ok {
			findings = append(findings, Finding{
				Kind:    FindingMissing,
				Subject: want.Name,
				Field:   "filter." + wf.Name,
				Detail:  fmt.Sprintf("filter %q is in the spec and not on the source", wf.Name),
			})
			continue
		}
		if wf.Kind != gf.Kind {
			findings = append(findings, Finding{
				Kind:    FindingKindMismatch,
				Subject: want.Name,
				Field:   "filter." + wf.Name,
				Detail:  fmt.Sprintf("filter kind is %q in the spec and %q on the source", wf.Kind, gf.Kind),
				Spec:    wf.Kind, Live: gf.Kind,
			})
			continue
		}
		if wf.Enabled != gf.Enabled {
			findings = append(findings, Finding{
				Kind:    FindingDrift,
				Subject: want.Name,
				Field:   "filter." + wf.Name + ".enabled",
				Detail:  fmt.Sprintf("spec has %v, scene has %v", wf.Enabled, gf.Enabled),
				Spec:    wf.Enabled, Live: gf.Enabled,
			})
		}
		for _, key := range sortedKeys(wf.Settings) {
			if !valuesEqual(wf.Settings[key], gf.Settings[key]) {
				findings = append(findings, Finding{
					Kind:    FindingDrift,
					Subject: want.Name,
					Field:   "filter." + wf.Name + "." + key,
					Detail:  fmt.Sprintf("spec has %v, scene has %v", wf.Settings[key], gf.Settings[key]),
					Spec:    wf.Settings[key], Live: gf.Settings[key],
				})
			}
		}
	}

	specFilters := map[string]bool{}
	for _, f := range want.Filters {
		specFilters[f.Name] = true
	}
	for _, gf := range got.Filters {
		if !specFilters[gf.Name] {
			findings = append(findings, Finding{
				Kind:    FindingUnmanaged,
				Subject: want.Name,
				Field:   "filter." + gf.Name,
				Detail:  fmt.Sprintf("filter %q is on the source and not in the spec", gf.Name),
			})
		}
	}

	return findings
}

// diffItems compares placements.
//
// Order is compared by the relative rank of managed items, never by absolute
// index. Anything the spec does not manage shifts every index below it, so an
// index comparison reports the whole scene as drifted the moment someone adds
// an overlay the spec was never meant to own.
func diffItems(spec, live *Spec) []Finding {
	findings := []Finding{}

	liveByKey := map[string]ItemSpec{}
	for _, item := range live.Items {
		liveByKey[itemKey(item)] = item
	}

	managedRanks := []ItemSpec{}
	for _, want := range spec.Items {
		got, ok := liveByKey[itemKey(want)]
		if !ok {
			findings = append(findings, Finding{
				Kind:    FindingMissing,
				Subject: want.Source,
				Field:   "placement",
				Detail: fmt.Sprintf("placement %d of %q is in the spec and not in the scene",
					want.Occurrence, want.Source),
			})
			continue
		}
		managedRanks = append(managedRanks, got)
		findings = append(findings, diffPlacement(want, got)...)
	}

	// Relative rank: the managed items, in the order the scene has them, must
	// appear in the order the spec has them.
	if len(managedRanks) > 1 {
		specOrder := map[string]int{}
		for i, want := range spec.Items {
			specOrder[itemKey(want)] = i
		}
		sorted := append([]ItemSpec(nil), managedRanks...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })
		for i := 1; i < len(sorted); i++ {
			prev, cur := sorted[i-1], sorted[i]
			if specOrder[itemKey(prev)] > specOrder[itemKey(cur)] {
				findings = append(findings, Finding{
					Kind:    FindingDrift,
					Subject: cur.Source,
					Field:   "order",
					Detail: fmt.Sprintf("%q now renders above %q; the spec has them the "+
						"other way round", cur.Source, prev.Source),
					Spec: specOrder[itemKey(cur)], Live: cur.Order,
				})
			}
		}
	}

	specKeys := map[string]bool{}
	for _, want := range spec.Items {
		specKeys[itemKey(want)] = true
	}
	for _, got := range live.Items {
		if !specKeys[itemKey(got)] {
			findings = append(findings, Finding{
				Kind:    FindingUnmanaged,
				Subject: got.Source,
				Field:   "placement",
				Detail: fmt.Sprintf("placement %d of %q is in the scene and not in the "+
					"spec; an apply leaves it alone", got.Occurrence, got.Source),
			})
		}
	}

	return findings
}

// itemKey identifies a placement across a capture and a later one.
//
// Not the scene item id: OBS hands out a new one for a recreated placement, so
// a spec applied to a fresh scene would match nothing. Source plus occurrence
// is what survives, and occurrence is why repeated placements of one source do
// not collapse.
func itemKey(item ItemSpec) string {
	return fmt.Sprintf("%s#%d", item.Source, item.Occurrence)
}

func diffPlacement(want, got ItemSpec) []Finding {
	findings := []Finding{}
	add := func(field string, specVal, liveVal any) {
		findings = append(findings, Finding{
			Kind:    FindingDrift,
			Subject: want.Source,
			Field:   field,
			Detail:  fmt.Sprintf("spec has %v, scene has %v", specVal, liveVal),
			Spec:    specVal, Live: liveVal,
		})
	}

	if want.Enabled != got.Enabled {
		add("enabled", want.Enabled, got.Enabled)
	}
	if want.Locked != got.Locked {
		add("locked", want.Locked, got.Locked)
	}
	if want.BlendMode != "" && got.BlendMode != "" && want.BlendMode != got.BlendMode {
		add("blend_mode", want.BlendMode, got.BlendMode)
	}
	if want.Transform != nil && got.Transform != nil {
		findings = append(findings, diffTransform(want.Source, want.Transform, got.Transform)...)
	}

	return findings
}

func diffTransform(subject string, want, got *obs.SceneItemTransform) []Finding {
	findings := []Finding{}
	check := func(field string, w, g, tolerance float64) {
		if math.Abs(w-g) > tolerance {
			findings = append(findings, Finding{
				Kind:    FindingDrift,
				Subject: subject,
				Field:   "transform." + field,
				Detail:  fmt.Sprintf("spec has %v, scene has %v", w, g),
				Spec:    w, Live: g,
			})
		}
	}

	check("position_x", want.PositionX, got.PositionX, positionTolerance)
	check("position_y", want.PositionY, got.PositionY, positionTolerance)
	check("scale_x", want.ScaleX, got.ScaleX, scaleTolerance)
	check("scale_y", want.ScaleY, got.ScaleY, scaleTolerance)
	check("rotation", want.Rotation, got.Rotation, rotationTolerance)
	check("crop_left", float64(want.CropLeft), float64(got.CropLeft), positionTolerance)
	check("crop_right", float64(want.CropRight), float64(got.CropRight), positionTolerance)
	check("crop_top", float64(want.CropTop), float64(got.CropTop), positionTolerance)
	check("crop_bottom", float64(want.CropBottom), float64(got.CropBottom), positionTolerance)

	if want.BoundsType != got.BoundsType {
		findings = append(findings, Finding{
			Kind: FindingDrift, Subject: subject, Field: "transform.bounds_type",
			Detail: fmt.Sprintf("spec has %v, scene has %v", want.BoundsType, got.BoundsType),
			Spec:   want.BoundsType, Live: got.BoundsType,
		})
	}
	// Bounds dimensions are inert under OBS_BOUNDS_NONE, and OBS reports zero
	// for an item that never had a bounding box, so comparing them there would
	// report drift on a value neither side can act on.
	if want.BoundsType != "" && want.BoundsType != "OBS_BOUNDS_NONE" {
		check("bounds_width", want.BoundsWidth, got.BoundsWidth, positionTolerance)
		check("bounds_height", want.BoundsHeight, got.BoundsHeight, positionTolerance)
	}
	if want.Alignment != got.Alignment {
		findings = append(findings, Finding{
			Kind: FindingDrift, Subject: subject, Field: "transform.alignment",
			Detail: fmt.Sprintf("spec has %v, scene has %v", want.Alignment, got.Alignment),
			Spec:   want.Alignment, Live: got.Alignment,
		})
	}

	return findings
}

// mergeOver layers settings on top of defaults, returning a new map.
func mergeOver(defaults, settings map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(defaults)+len(settings))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range settings {
		out[k] = v
	}
	return out
}

// valuesEqual compares two settings values.
//
// JSON gives every number back as float64 whatever went out, so 400 and 400.0
// are the same value arriving by different routes. Comparing them with == or
// reflect.DeepEqual reports drift on a value nobody changed.
func valuesEqual(a, b interface{}) bool {
	an, aok := asFloat(a)
	bn, bok := asFloat(b)
	if aok && bok {
		return math.Abs(an-bn) < 1e-9
	}
	return reflect.DeepEqual(a, b)
}

func asFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
