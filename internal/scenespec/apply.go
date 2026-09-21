package scenespec

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/andreykaipov/goobs/api/typedefs"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Operation results. Every op reports one of these, so a partial apply is a
// record of how far it got rather than an error with the scene left half done.
const (
	OpCreated   = "created"
	OpUpdated   = "updated"
	OpUnchanged = "unchanged"
	OpSkipped   = "skipped"
	OpFailed    = "failed"
)

// What to do with things the spec does not describe.
const (
	// UnmanagedKeep is the default, and it is the default because removing is
	// unrecoverable: a scene almost always holds sources someone configured by
	// hand and the spec was never meant to own.
	UnmanagedKeep   = "keep"
	UnmanagedHide   = "hide"
	UnmanagedRemove = "remove"
)

// ApplyOptions controls how much an apply is allowed to do.
type ApplyOptions struct {
	// DryRun plans without writing. It defaults to false here because the
	// caller that matters -- the MCP tool -- defaults it to true, and a library
	// function whose zero value silently refuses to work is worse than one
	// whose caller is explicit.
	DryRun bool

	// OnUnmanaged is keep (default), hide or remove.
	OnUnmanaged string
}

// OpResult is one operation an apply performed or planned.
type OpResult struct {
	Op      string `json:"op"`
	Subject string `json:"subject"`
	Result  string `json:"result"`
	Detail  string `json:"detail,omitempty"`
}

// Report is what an apply did.
type Report struct {
	Scene  string     `json:"scene"`
	DryRun bool       `json:"dry_run"`
	Ops    []OpResult `json:"ops"`

	// Before is the scene as it was, captured before anything was written.
	//
	// An apply to a live scene is not undoable without it, and nothing else
	// records what was replaced. Applying this spec puts the scene back.
	Before *Spec `json:"before,omitempty"`
}

// ApplyClient is the write side an apply needs, on top of the read side.
type ApplyClient interface {
	DiffReader

	// Existence is global, not per-scene. An input lives in OBS and scenes
	// reference it, so "is this source missing" cannot be answered from the
	// scene being applied: a source absent from this scene may be placed in
	// five others, and creating it again fails with ResourceAlreadyExists.
	ListSources() ([]*typedefs.Input, error)
	GetSceneList() ([]string, string, error)
	GetGroupList() ([]string, error)

	CreateInput(sceneName, sourceName, inputKind string, settings map[string]interface{}) (int, error)
	CreateSceneItem(sceneName, sourceName string, enabled bool) (int, error)
	RemoveSceneItem(sceneName string, sceneItemID int) error
	SetSourceSettings(sourceName string, settings map[string]interface{}, overlay bool) error
	SetSceneItemTransform(sceneName string, sceneItemID int, transform *obs.SceneItemTransform) error
	SetSceneItemEnabled(sceneName string, sceneItemID int, enabled bool) error
	SetSceneItemLocked(sceneName string, sceneItemID int, locked bool) error
	SetSceneItemIndex(sceneName string, sceneItemID int, index int) error
	CreateSourceFilter(sourceName, filterName, filterKind string, settings map[string]interface{}) error
	SetSourceFilterSettings(sourceName, filterName string, settings map[string]interface{}, overlay bool) error
	SetSourceFilterEnabled(sourceName, filterName string, enabled bool) error
	RemoveSourceFilter(sourceName, filterName string) error
}

// Apply reconciles a scene to a spec.
//
// The order is fixed, because the steps depend on each other: a source has to
// exist before it can be placed, and a placement has to exist before it can be
// transformed. Sources, then placements, then settings, then filters, then the
// per-placement state, then one ordering pass, then pruning.
//
// What to do is decided by Diff rather than by writing everything, which is
// what makes a second apply report nothing left to do. That matters beyond
// tidiness: every write is an event, and an automation rule watching the scene
// fires on each one, so an apply that writes unconditionally is indistinguishable
// from one that converges until something is listening.
//
// A failure is a per-op result, never an abort. A partial apply is the normal
// case against a live OBS -- a source can be locked, a file can have moved --
// and a run that stopped without saying how far it got is worse than one that
// finished and reported four failures.
func Apply(ctx context.Context, client ApplyClient, spec *Spec, sceneName string, opts ApplyOptions) (*Report, error) {
	if spec == nil {
		return nil, fmt.Errorf("no spec to apply")
	}
	if len(spec.Omitted) > 0 {
		// A spec captured without settings describes a layout, not a scene.
		// Applying it would write empty settings over every source in it.
		return nil, fmt.Errorf(
			"this spec omitted %s, so applying it would write over what it did not "+
				"capture. Capture again with the defaults",
			strings.Join(spec.Omitted, " and "))
	}
	if sceneName == "" {
		sceneName = spec.Scene
	}
	if sceneName == "" {
		return nil, fmt.Errorf("no scene to apply to")
	}
	if opts.OnUnmanaged == "" {
		opts.OnUnmanaged = UnmanagedKeep
	}

	before, err := CaptureWith(client, sceneName, FullCapture())
	if err != nil {
		return nil, fmt.Errorf("capturing %q before applying: %w", sceneName, err)
	}

	// Layouts become concrete transforms before anything is written, so every
	// step below works on numbers and the internal diff cannot reach a
	// different conclusion about where a placement goes. An unresolvable layout
	// stops the whole apply here rather than failing one op: a malformed
	// document half-applied leaves a scene nobody described.
	//
	// Report.Before stays the live capture, unresolved -- it is the undo.
	spec, err = resolveLayouts(client, spec, before)
	if err != nil {
		return nil, err
	}

	report := &Report{Scene: sceneName, DryRun: opts.DryRun, Before: before, Ops: []OpResult{}}
	run := &applyRun{ctx: ctx, client: client, scene: sceneName, opts: opts, report: report}

	run.ensureSources(spec, before)
	if run.cancelled() {
		return report, nil
	}
	run.ensurePlacements(spec)
	if run.cancelled() {
		return report, nil
	}
	run.reconcileState(spec)
	if run.cancelled() {
		return report, nil
	}
	run.reconcileOrder(spec)
	run.prune(spec)

	return report, nil
}

type applyRun struct {
	ctx    context.Context
	client ApplyClient
	scene  string
	opts   ApplyOptions
	report *Report

	// failed records sources whose earlier op failed, so the ops that depend on
	// them are skipped with the cause rather than failing again for a reason
	// that reads as unrelated.
	failed map[string]string
}

func (r *applyRun) cancelled() bool {
	select {
	case <-r.ctx.Done():
		r.record("apply", r.scene, OpSkipped, "cancelled: "+r.ctx.Err().Error())
		return true
	default:
		return false
	}
}

func (r *applyRun) record(op, subject, result, detail string) {
	r.report.Ops = append(r.report.Ops, OpResult{
		Op: op, Subject: subject, Result: result, Detail: detail,
	})
}

func (r *applyRun) markFailed(source, why string) {
	if r.failed == nil {
		r.failed = map[string]string{}
	}
	r.failed[source] = why
}

// blocked reports whether an earlier op on this source failed.
func (r *applyRun) blocked(source string) (string, bool) {
	why, ok := r.failed[source]
	return why, ok
}

// ensureSources creates the inputs the spec names and the scene lacks.
//
// CreateInput both creates the input and places it, so a source created here
// already has its first placement -- ensurePlacements accounts for that rather
// than adding a second.
func (r *applyRun) ensureSources(spec, live *Spec) {
	// What the scene already references, which is where the kind is known from.
	liveByName := map[string]SourceSpec{}
	for _, s := range live.Sources {
		liveByName[s.Name] = s
	}

	// What exists in OBS at all, which is a different question and the one that
	// decides whether anything needs creating.
	exists, kinds, err := r.existingSources()
	if err != nil {
		r.record("sources", r.scene, OpFailed, err.Error())
		return
	}

	for _, want := range spec.Sources {
		got, present := liveByName[want.Name]
		if !present && exists[want.Name] {
			// Present in OBS, just not placed in this scene. Its kind still has
			// to match, and ensurePlacements will place it.
			got = SourceSpec{Name: want.Name, Type: want.Type, Kind: kinds[want.Name]}
			if kinds[want.Name] == "" {
				got.Kind = want.Kind // a scene or group has none
			}
			present = true
		}

		if present {
			if want.Type != got.Type || (want.Type == SourceInput && want.Kind != got.Kind) {
				// Recreating is the only way to change a kind, and it destroys
				// every placement of the source in every scene. An apply does
				// not do that on its own.
				r.record("source", want.Name, OpFailed, fmt.Sprintf(
					"the spec has %s/%s and the scene has %s/%s; changing that means "+
						"removing and recreating the source, which destroys every "+
						"placement of it in every scene",
					want.Type, want.Kind, got.Type, got.Kind))
				r.markFailed(want.Name, "kind mismatch")
				continue
			}
			r.record("source", want.Name, OpUnchanged, "")
			continue
		}

		switch want.Type {
		case SourceGroup:
			// obs-websocket registers CreateScene and no CreateGroup. Reporting
			// this as failed would suggest retrying, and reporting success would
			// be a lie, so it is a skip that says why.
			r.record("source", want.Name, OpSkipped,
				"obs-websocket cannot create a group: it has CreateScene and no "+
					"CreateGroup, so this group has to exist already. Create it in the "+
					"OBS UI, or place its contents directly in the scene")
			r.markFailed(want.Name, "group cannot be created")

		case SourceScene:
			// A scene could be created empty, but an empty scene is not what the
			// spec describes -- a nested scene's contents are its own document.
			r.record("source", want.Name, OpSkipped, fmt.Sprintf(
				"scene %q does not exist; a nested scene is captured and applied as "+
					"its own spec, so create and apply that one first", want.Name))
			r.markFailed(want.Name, "nested scene does not exist")

		default:
			if r.opts.DryRun {
				r.record("source", want.Name, OpCreated,
					fmt.Sprintf("would create a %s and place it", want.Kind))
				continue
			}
			if _, err := r.client.CreateInput(r.scene, want.Name, want.Kind, want.Settings); err != nil {
				r.record("source", want.Name, OpFailed, err.Error())
				r.markFailed(want.Name, "could not be created")
				continue
			}
			r.record("source", want.Name, OpCreated, "created and placed")
		}
	}
}

// existingSources reports every source OBS holds, and the kind of each input.
//
// Three lookups because OBS keeps three lists and a source appears in exactly
// one of them: inputs in GetInputList, scenes in GetSceneList, groups in
// GetGroupList. A name in none of them is genuinely absent.
func (r *applyRun) existingSources() (map[string]bool, map[string]string, error) {
	exists := map[string]bool{}
	kinds := map[string]string{}

	inputs, err := r.client.ListSources()
	if err != nil {
		return nil, nil, fmt.Errorf("listing inputs: %w", err)
	}
	for _, in := range inputs {
		if in == nil {
			continue
		}
		exists[in.InputName] = true
		kinds[in.InputName] = in.InputKind
	}

	scenes, _, err := r.client.GetSceneList()
	if err != nil {
		return nil, nil, fmt.Errorf("listing scenes: %w", err)
	}
	for _, name := range scenes {
		exists[name] = true
	}

	groups, err := r.client.GetGroupList()
	if err != nil {
		return nil, nil, fmt.Errorf("listing groups: %w", err)
	}
	for _, name := range groups {
		exists[name] = true
	}

	return exists, kinds, nil
}

// ensurePlacements adds the scene items the spec names and the scene lacks.
func (r *applyRun) ensurePlacements(spec *Spec) {
	live, err := CaptureWith(r.client, r.scene, FullCapture())
	if err != nil {
		r.record("placements", r.scene, OpFailed, err.Error())
		return
	}

	have := map[string]int{}
	for _, item := range live.Items {
		have[item.Source]++
	}

	want := map[string]int{}
	for _, item := range spec.Items {
		want[item.Source]++
	}

	for _, source := range sortedStringKeys(want) {
		if why, blocked := r.blocked(source); blocked {
			r.record("placement", source, OpSkipped, "depends on the source, which "+why)
			continue
		}
		missing := want[source] - have[source]
		if missing <= 0 {
			r.record("placement", source, OpUnchanged, "")
			continue
		}
		for i := 0; i < missing; i++ {
			if r.opts.DryRun {
				r.record("placement", source, OpCreated, "would place it in the scene")
				continue
			}
			if _, err := r.client.CreateSceneItem(r.scene, source, true); err != nil {
				r.record("placement", source, OpFailed, err.Error())
				r.markFailed(source, "could not be placed")
				break
			}
			r.record("placement", source, OpCreated, "placed in the scene")
		}
	}
}

// reconcileState writes settings, filters and per-placement state, but only
// where a diff says they differ.
func (r *applyRun) reconcileState(spec *Spec) {
	findings, err := Diff(r.client, spec, r.scene)
	if err != nil {
		r.record("diff", r.scene, OpFailed, err.Error())
		return
	}

	// Group the findings: many transform fields are one write, and many
	// settings keys are one write.
	needsSettings := map[string]bool{}
	needsTransform := map[string]bool{}
	needsEnabled := map[string]bool{}
	needsLocked := map[string]bool{}
	needsFilter := map[string]map[string]bool{}

	for _, f := range findings {
		switch {
		case f.Kind == FindingUnmanaged || f.Kind == FindingRenamed:
			// Handled elsewhere, or not by an apply at all.
		case strings.HasPrefix(f.Field, "settings."):
			needsSettings[f.Subject] = true
		case strings.HasPrefix(f.Field, "transform."):
			needsTransform[f.Subject] = true
		case f.Field == "enabled":
			needsEnabled[f.Subject] = true
		case f.Field == "locked":
			needsLocked[f.Subject] = true
		case strings.HasPrefix(f.Field, "filter."):
			name := strings.SplitN(strings.TrimPrefix(f.Field, "filter."), ".", 2)[0]
			if needsFilter[f.Subject] == nil {
				needsFilter[f.Subject] = map[string]bool{}
			}
			needsFilter[f.Subject][name] = true
		}
	}

	for _, source := range spec.Sources {
		if _, blocked := r.blocked(source.Name); blocked {
			continue
		}
		r.applySettings(source, needsSettings[source.Name])
		r.applyFilters(source, needsFilter[source.Name])
	}

	live, err := CaptureWith(r.client, r.scene, FullCapture())
	if err != nil {
		r.record("placements", r.scene, OpFailed, err.Error())
		return
	}
	liveByKey := map[string]ItemSpec{}
	for _, item := range live.Items {
		liveByKey[itemKey(item)] = item
	}

	for _, want := range spec.Items {
		got, ok := liveByKey[itemKey(want)]
		if !ok {
			continue // already reported as failed or skipped
		}
		r.applyPlacement(want, got,
			needsTransform[want.Source], needsEnabled[want.Source], needsLocked[want.Source])
	}
}

func (r *applyRun) applySettings(source SourceSpec, differs bool) {
	if source.Type != SourceInput || len(source.Settings) == 0 {
		return
	}
	if !differs {
		r.record("settings", source.Name, OpUnchanged, "")
		return
	}
	if r.opts.DryRun {
		r.record("settings", source.Name, OpUpdated, "would write the spec's settings")
		return
	}
	settings := source.Settings
	if len(source.PreserveURLParams) > 0 {
		// The one read this costs happens only for a source that declares the
		// field, and only on an apply that is actually going to write.
		live, err := r.client.GetSourceSettings(source.Name)
		if err != nil {
			r.record("settings", source.Name, OpFailed, err.Error())
			return
		}
		settings = PreserveURLParams(settings, live, source.PreserveURLParams)
	}

	// overlay=false: the live object is made to match the spec rather than
	// merged with it, so a key the spec dropped goes back to its default
	// instead of lingering, and a second apply has nothing to do.
	if err := r.client.SetSourceSettings(source.Name, settings, false); err != nil {
		r.record("settings", source.Name, OpFailed, err.Error())
		return
	}
	r.record("settings", source.Name, OpUpdated, "")
}

func (r *applyRun) applyFilters(source SourceSpec, differing map[string]bool) {
	for _, want := range source.Filters {
		if !differing[want.Name] {
			r.record("filter", source.Name+"/"+want.Name, OpUnchanged, "")
			continue
		}
		if r.opts.DryRun {
			r.record("filter", source.Name+"/"+want.Name, OpUpdated, "would reconcile the filter")
			continue
		}

		// Create it if it is not there; the create fails harmlessly if it is,
		// and the settings write covers both cases.
		if err := r.client.CreateSourceFilter(source.Name, want.Name, want.Kind, want.Settings); err == nil {
			r.record("filter", source.Name+"/"+want.Name, OpCreated, "")
		} else {
			if err := r.client.SetSourceFilterSettings(source.Name, want.Name, want.Settings, false); err != nil {
				r.record("filter", source.Name+"/"+want.Name, OpFailed, err.Error())
				continue
			}
			r.record("filter", source.Name+"/"+want.Name, OpUpdated, "")
		}
		if err := r.client.SetSourceFilterEnabled(source.Name, want.Name, want.Enabled); err != nil {
			r.record("filter", source.Name+"/"+want.Name, OpFailed, err.Error())
		}
	}
}

func (r *applyRun) applyPlacement(want, got ItemSpec, transform, enabled, locked bool) {
	subject := want.Source

	if transform && want.Transform != nil {
		if r.opts.DryRun {
			r.record("transform", subject, OpUpdated, "would write the spec's transform")
		} else if err := r.client.SetSceneItemTransform(r.scene, got.SceneItemID, want.Transform); err != nil {
			r.record("transform", subject, OpFailed, err.Error())
		} else {
			r.record("transform", subject, OpUpdated, "")
		}
	}

	if enabled {
		if r.opts.DryRun {
			r.record("visibility", subject, OpUpdated, fmt.Sprintf("would set enabled=%v", want.Enabled))
		} else if err := r.client.SetSceneItemEnabled(r.scene, got.SceneItemID, want.Enabled); err != nil {
			r.record("visibility", subject, OpFailed, err.Error())
		} else {
			r.record("visibility", subject, OpUpdated, "")
		}
	}

	// Locked last of the per-placement writes: locking first would make every
	// write after it fail.
	if locked {
		if r.opts.DryRun {
			r.record("lock", subject, OpUpdated, fmt.Sprintf("would set locked=%v", want.Locked))
		} else if err := r.client.SetSceneItemLocked(r.scene, got.SceneItemID, want.Locked); err != nil {
			r.record("lock", subject, OpFailed, err.Error())
		} else {
			r.record("lock", subject, OpUpdated, "")
		}
	}
}

// reconcileOrder puts the managed placements back into the spec's relative
// order.
//
// Managed items are permuted among the positions they already occupy, and
// nothing unmanaged moves. Using the spec's own index as the target would be
// wrong twice over: the scene holds items the spec does not, so an index past
// the end is out of range, and an unmanaged item sitting between two managed
// ones would be shoved out of place by a stack that assumed it owned every
// slot.
func (r *applyRun) reconcileOrder(spec *Spec) {
	rank := map[string]int{}
	for i, want := range spec.Items {
		rank[itemKey(want)] = i
	}

	live, err := CaptureWith(r.client, r.scene, FullCapture())
	if err != nil {
		r.record("order", r.scene, OpFailed, err.Error())
		return
	}

	// The positions managed items currently occupy, and the order the spec
	// wants them in. Those two lists zipped together are the target.
	slots := []int{}
	managed := []ItemSpec{}
	for _, item := range live.Items {
		if _, ok := rank[itemKey(item)]; ok {
			slots = append(slots, item.Order)
			managed = append(managed, item)
		}
	}
	sort.SliceStable(managed, func(i, j int) bool {
		return rank[itemKey(managed[i])] < rank[itemKey(managed[j])]
	})

	moved := false
	for k, target := range slots {
		want := managed[k]

		// Re-read: every move shifts the positions after it, so reasoning about
		// where things ended up is a second implementation of the same logic
		// and a second chance to get it wrong.
		live, err = CaptureWith(r.client, r.scene, FullCapture())
		if err != nil {
			r.record("order", r.scene, OpFailed, err.Error())
			return
		}
		current := -1
		var itemID int
		for _, item := range live.Items {
			if itemKey(item) == itemKey(want) {
				current, itemID = item.Order, item.SceneItemID
				break
			}
		}
		if current < 0 || current == target {
			continue
		}

		if r.opts.DryRun {
			r.record("order", want.Source, OpUpdated,
				fmt.Sprintf("would move from %d to %d", current, target))
			moved = true
			continue
		}
		if err := r.client.SetSceneItemIndex(r.scene, itemID, target); err != nil {
			r.record("order", want.Source, OpFailed, err.Error())
			continue
		}
		r.record("order", want.Source, OpUpdated, fmt.Sprintf("moved to %d", target))
		moved = true
	}

	if !moved {
		r.record("order", r.scene, OpUnchanged, "")
	}
}

// prune deals with placements the spec does not describe.
func (r *applyRun) prune(spec *Spec) {
	if r.opts.OnUnmanaged == UnmanagedKeep {
		return
	}

	live, err := CaptureWith(r.client, r.scene, FullCapture())
	if err != nil {
		r.record("prune", r.scene, OpFailed, err.Error())
		return
	}

	managed := map[string]bool{}
	for _, item := range spec.Items {
		managed[itemKey(item)] = true
	}

	for _, got := range live.Items {
		if managed[itemKey(got)] {
			continue
		}
		switch r.opts.OnUnmanaged {
		case UnmanagedHide:
			if r.opts.DryRun {
				r.record("prune", got.Source, OpUpdated, "would hide it")
				continue
			}
			if err := r.client.SetSceneItemEnabled(r.scene, got.SceneItemID, false); err != nil {
				r.record("prune", got.Source, OpFailed, err.Error())
				continue
			}
			r.record("prune", got.Source, OpUpdated, "hidden")

		case UnmanagedRemove:
			if r.opts.DryRun {
				r.record("prune", got.Source, OpUpdated, "would remove the placement")
				continue
			}
			// RemoveSceneItem, never RemoveInput. An input is a shared object,
			// and removing it blanks it in every other scene showing it. OBS
			// refcounts sources, so the input goes away by itself once nothing
			// references it -- which is the right behaviour and not this
			// function's decision to make.
			if err := r.client.RemoveSceneItem(r.scene, got.SceneItemID); err != nil {
				r.record("prune", got.Source, OpFailed, err.Error())
				continue
			}
			r.record("prune", got.Source, OpUpdated, "placement removed; the source itself is untouched")
		}
	}
}

func sortedStringKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
