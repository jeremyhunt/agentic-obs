package scenespec

import (
	"fmt"
	"sort"
	"strings"
)

// A spec describes a whole scene, but a caller often owns only part of one. A
// scene preset is the case already in this repo: a list of which sources are
// visible and nothing else, where reconciling a transform somebody moved on
// purpose would be wrong.
//
// Fields is that restriction, and it is expressed once. Because an apply is
// driven by a diff, masking the diff masks the writes -- the two cannot end up
// disagreeing about what is in scope.

// The aspects a spec describes. One of these is what a Finding's Field reduces
// to, and what a caller names in Fields.
const (
	FieldSource    = "source"     // a source missing from, or unmanaged in, the scene
	FieldPlacement = "placement"  // a placement missing from, or unmanaged in, the scene
	FieldKind      = "kind"       // a source's type or input kind changed
	FieldSettings  = "settings"   // a source's own configuration
	FieldFilters   = "filters"    // filters on a source
	FieldTransform = "transform"  // placement geometry
	FieldEnabled   = "enabled"    // placement visibility
	FieldLocked    = "locked"     // placement lock state
	FieldBlendMode = "blend_mode" // how a placement composites
	FieldOrder     = "order"      // render order
)

func allFields() []string {
	return []string{
		FieldSource, FieldPlacement, FieldKind, FieldSettings, FieldFilters,
		FieldTransform, FieldEnabled, FieldLocked, FieldBlendMode, FieldOrder,
	}
}

// fieldMask answers whether an aspect is in scope. A nil mask means everything,
// which is what an empty Fields asks for.
type fieldMask map[string]bool

// newFieldMask validates the names and builds the mask.
//
// An unknown name is an error rather than something to ignore, and that is the
// whole safety property here: a mask that quietly matched nothing would make a
// diff report no differences and an apply write nothing, and both would look
// exactly like success.
func newFieldMask(fields []string) (fieldMask, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	valid := map[string]bool{}
	for _, name := range allFields() {
		valid[name] = true
	}

	mask := fieldMask{}
	for _, raw := range fields {
		name := strings.ToLower(strings.TrimSpace(raw))
		if !valid[name] {
			return nil, fmt.Errorf("unknown field %q; use one of %s",
				raw, strings.Join(allFields(), ", "))
		}
		mask[name] = true
	}
	return mask, nil
}

// covers reports whether an aspect is in scope.
func (m fieldMask) covers(field string) bool {
	if m == nil {
		return true
	}
	return m[field]
}

// fieldOf reduces a finding to the aspect it belongs to.
//
// Findings name a specific thing -- "settings.url", "filter.Room haze.opacity",
// "transform.position_x" -- so the aspect is the part before the first dot, with
// two exceptions: a structural finding about a source carries no field at all,
// and filters are plural as an aspect and singular in a finding.
func fieldOf(f Finding) string {
	switch {
	case f.Field == "":
		return FieldSource
	case strings.HasPrefix(f.Field, "filter."):
		return FieldFilters
	case strings.HasPrefix(f.Field, "settings."):
		return FieldSettings
	case strings.HasPrefix(f.Field, "transform."):
		return FieldTransform
	case f.Field == "type":
		return FieldKind
	}
	return f.Field
}

// filterFindings drops everything outside the mask, and sorts nothing: the
// caller has already ordered them.
func filterFindings(findings []Finding, mask fieldMask) []Finding {
	if mask == nil {
		return findings
	}
	kept := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if mask.covers(fieldOf(f)) {
			kept = append(kept, f)
		}
	}
	return kept
}

// sortedFields renders a mask for a message, so a report says what it looked at.
func (m fieldMask) sortedFields() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
