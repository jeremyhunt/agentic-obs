package obs

// Alignment and bounds constants, taken from OBS rather than inferred.
//
// These are the values obs-websocket passes straight through to libobs, and the
// protocol reference does not define them -- it documents the request shape and
// leaves the meaning to OBS. Both sources are cited per group below, because
// guessing here is how a transform ends up anchored to the wrong corner while
// every test still passes.

// Alignment flags, from libobs/obs-defs.h:
//
//	#define OBS_ALIGN_CENTER (0)
//	#define OBS_ALIGN_LEFT   (1 << 0)
//	#define OBS_ALIGN_RIGHT  (1 << 1)
//	#define OBS_ALIGN_TOP    (1 << 2)
//	#define OBS_ALIGN_BOTTOM (1 << 3)
//
// They combine with OR. Centre is the absence of an edge flag, not a flag of
// its own, so AlignCenter|AlignLeft is just AlignLeft -- there is no way to say
// "centred vertically, left horizontally" other than by omitting the vertical
// flags, which is what AlignLeft alone already means.
const (
	AlignCenter = 0
	AlignLeft   = 1 << 0
	AlignRight  = 1 << 1
	AlignTop    = 1 << 2
	AlignBottom = 1 << 3

	// AlignTopLeft is what libobs gives a newly created scene item, and the
	// value a live OBS reports for one. It appeared as a bare 5 in several
	// places before this. (FB-74)
	AlignTopLeft = AlignTop | AlignLeft
)

// Bounds types, from the obs_transform_info reference in obs-studio's docs
// (docs/sphinx/reference-scenes.rst).
//
// Two fields are easy to conflate and mean different things:
//
//   - Alignment is the alignment of the scene item relative to *its position* --
//     which point of the source sits at positionX/positionY.
//   - BoundsAlignment is the alignment of the source *within the bounding box*,
//     and only matters when a bounds type other than none is set.
//
// The bounds types are also why placement needs the canvas and not the source
// size: OBS already implements fit, fill and stretch, so computing scale from a
// source's own dimensions is both redundant and stale the moment the asset is
// replaced.
const (
	// BoundsNone is no bounding box; the item is sized by its scale alone.
	BoundsNone = "OBS_BOUNDS_NONE"

	// BoundsStretch fills the box, ignoring aspect ratio.
	BoundsStretch = "OBS_BOUNDS_STRETCH"

	// BoundsScaleInner fits inside the box, preserving aspect ratio. This is
	// "letterbox": the whole source is visible, with space left over.
	BoundsScaleInner = "OBS_BOUNDS_SCALE_INNER"

	// BoundsScaleOuter covers the box, preserving aspect ratio. This is "crop
	// to fill": no empty space, and the overflowing edges are outside the box.
	BoundsScaleOuter = "OBS_BOUNDS_SCALE_OUTER"

	// BoundsScaleToWidth matches the box width, preserving aspect ratio; the
	// height follows and may exceed the box.
	BoundsScaleToWidth = "OBS_BOUNDS_SCALE_TO_WIDTH"

	// BoundsScaleToHeight matches the box height, preserving aspect ratio.
	BoundsScaleToHeight = "OBS_BOUNDS_SCALE_TO_HEIGHT"

	// BoundsMaxOnly scales down to fit but never up, so a source smaller than
	// the box is left at its own size.
	BoundsMaxOnly = "OBS_BOUNDS_MAX_ONLY"
)

// BoundsTypes is every bounds type OBS accepts, for validating a caller's input
// against something better than a guess.
var BoundsTypes = []string{
	BoundsNone,
	BoundsStretch,
	BoundsScaleInner,
	BoundsScaleOuter,
	BoundsScaleToWidth,
	BoundsScaleToHeight,
	BoundsMaxOnly,
}

// IsValidBoundsType reports whether OBS would accept this bounds type.
func IsValidBoundsType(boundsType string) bool {
	for _, t := range BoundsTypes {
		if t == boundsType {
			return true
		}
	}
	return false
}
