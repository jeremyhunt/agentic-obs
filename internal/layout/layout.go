// Package layout turns a placement intent into the transform fields OBS needs.
//
// It is pure: no client, no I/O, no source dimensions. That last one is the
// design rather than an omission. OBS already implements fit, fill and stretch
// as bounding-box types, so computing a scale factor from a source's own size
// would duplicate work OBS does better, divide by zero before a browser source
// has loaded, and go stale the moment the asset behind the source is replaced.
// Resolving to a bounding box needs the canvas and nothing else. (FB-75)
package layout

import (
	"fmt"
	"strings"

	"github.com/ironystock/agentic-obs/internal/obs"
)

// Canvas is the coordinate space scene items live in -- OBS's base resolution,
// not its output resolution.
type Canvas struct {
	Width  float64
	Height float64
}

// Region is a rectangle in canvas coordinates. Omit it to mean the whole canvas.
type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Spec is a placement intent.
// It carries JSON tags because a scene spec stores one verbatim (ADR-011). The
// resolver's input and the stored document are deliberately one type: a second
// copy of {mode, region, anchor} is a second place for them to disagree.
type Spec struct {
	// Mode is how the source should fill its region. See modeBounds.
	Mode string `json:"mode"`
	// Region is where on the canvas; the whole canvas when nil.
	Region *Region `json:"region,omitempty"`
	// Anchor is where the source sits within its region when the mode leaves
	// spare space. Empty means centre.
	Anchor string `json:"anchor,omitempty"`
}

// Result is the transform fields a placement resolves to.
type Result struct {
	PositionX       float64
	PositionY       float64
	Alignment       int
	BoundsType      string
	BoundsAlignment int
	BoundsWidth     float64
	BoundsHeight    float64
}

// modeBounds maps each mode to the OBS bounds type that implements it.
//
// The names are the seven types from obs_transform_info, in plainer words. They
// are one-to-one on purpose: a mode that did not correspond to a bounds type
// would be arithmetic done here instead of by OBS.
var modeBounds = map[string]string{
	"fit":     obs.BoundsScaleInner,    // whole source visible, letterboxed
	"fill":    obs.BoundsScaleOuter,    // covers the region, overflow cropped
	"stretch": obs.BoundsStretch,       // fills the region, aspect ignored
	"width":   obs.BoundsScaleToWidth,  // matches region width, height follows
	"height":  obs.BoundsScaleToHeight, // matches region height, width follows
	"shrink":  obs.BoundsMaxOnly,       // scales down to fit, never up
	"none":    obs.BoundsNone,          // no bounding box; position only
}

// anchorAlignment maps an anchor to an OBS alignment flag combination.
//
// Centre is 0 -- the absence of edge flags rather than a flag of its own -- so
// it is listed explicitly instead of being ORed in.
var anchorAlignment = map[string]int{
	"":             obs.AlignCenter,
	"center":       obs.AlignCenter,
	"centre":       obs.AlignCenter,
	"top-left":     obs.AlignTop | obs.AlignLeft,
	"top":          obs.AlignTop,
	"top-right":    obs.AlignTop | obs.AlignRight,
	"left":         obs.AlignLeft,
	"right":        obs.AlignRight,
	"bottom-left":  obs.AlignBottom | obs.AlignLeft,
	"bottom":       obs.AlignBottom,
	"bottom-right": obs.AlignBottom | obs.AlignRight,
}

// Modes lists the placement modes, for error messages and schemas.
func Modes() []string {
	return []string{"fit", "fill", "stretch", "width", "height", "shrink", "none"}
}

// Anchors lists the anchors, for the same reason.
func Anchors() []string {
	return []string{
		"center", "top-left", "top", "top-right",
		"left", "right", "bottom-left", "bottom", "bottom-right",
	}
}

// Resolve turns a spec into transform fields.
func Resolve(spec Spec, canvas Canvas) (Result, error) {
	boundsType, ok := modeBounds[strings.ToLower(spec.Mode)]
	if !ok {
		return Result{}, fmt.Errorf("unknown layout mode %q; use one of %s",
			spec.Mode, strings.Join(Modes(), ", "))
	}

	boundsAlignment, ok := anchorAlignment[strings.ToLower(spec.Anchor)]
	if !ok {
		return Result{}, fmt.Errorf("unknown anchor %q; use one of %s",
			spec.Anchor, strings.Join(Anchors(), ", "))
	}

	if canvas.Width <= 0 || canvas.Height <= 0 {
		return Result{}, fmt.Errorf(
			"canvas is %vx%v; read it from get_obs_status rather than assuming a size",
			canvas.Width, canvas.Height)
	}

	region := Region{Width: canvas.Width, Height: canvas.Height}
	if spec.Region != nil {
		region = *spec.Region
		if region.Width <= 0 || region.Height <= 0 {
			return Result{}, fmt.Errorf("region is %vx%v; both dimensions must be positive",
				region.Width, region.Height)
		}
	}

	return Result{
		// The position is the region's top-left corner, and the item is
		// anchored there. Alignment and BoundsAlignment are different fields:
		// alignment says which point of the item sits at the position,
		// BoundsAlignment says where the source sits inside the box. Pinning
		// the first keeps the second the only thing that moves anything.
		PositionX: region.X,
		PositionY: region.Y,
		Alignment: obs.AlignTopLeft,

		BoundsType:      boundsType,
		BoundsAlignment: boundsAlignment,

		// obs-websocket rejects a bounds dimension below 1 even when the bounds
		// type makes it inert, so a resolved transform never carries one. (FB-64)
		BoundsWidth:  atLeastOne(region.Width),
		BoundsHeight: atLeastOne(region.Height),
	}, nil
}

func atLeastOne(v float64) float64 {
	if v < 1 {
		return 1
	}
	return v
}
