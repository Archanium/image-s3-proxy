// Package procopts parses and canonicalises imgproxy-style processing
// options from the segments of a /_p/ URL.
//
// Story: Processing Options
//
// Input:  the path segments of a /_p/ URL that sit between the signature
//
//	segment and the source tail, in imgproxy syntax (`name:arg:arg`).
//
// Process:
//  1. Split each segment into an option name and its arguments.
//  2. Resolve the name through the alias table to a canonical long name.
//  3. Apply the option's parser to the arguments, mutating an Options value.
//     Compound options (resize, size) delegate to the atomic parsers.
//  4. Normalise every parsed value so that equivalent spellings converge.
//
// Output: an Options value plus a canonical string that is stable across
//
//	option order, alias choice, compound-vs-atomic spelling, and
//	explicitly-stated defaults.
//
// Dependencies: stdlib only. No S3, no HTTP, no libvips.
// Side effects: none — every exported function is pure.
package procopts

import (
	"fmt"
	"strconv"
	"strings"
)

// ResizingType selects the geometry strategy applied during resize. The
// values mirror imgproxy's resizing_type vocabulary.
type ResizingType string

const (
	// ResizingTypeFit scales the image to fit inside the requested box,
	// preserving aspect ratio. Nothing is cropped. This is the default.
	ResizingTypeFit ResizingType = "fit"
	// ResizingTypeFill scales the image to cover the requested box,
	// preserving aspect ratio, then crops the excess at the gravity.
	ResizingTypeFill ResizingType = "fill"
	// ResizingTypeFillDown behaves like fill but never enlarges: a source
	// smaller than the request yields a smaller result at the requested
	// aspect ratio.
	ResizingTypeFillDown ResizingType = "fill-down"
	// ResizingTypeForce stretches the image to exactly the requested size,
	// ignoring aspect ratio.
	ResizingTypeForce ResizingType = "force"
	// ResizingTypeAuto uses fill when the source and the requested box share
	// an orientation, and fit otherwise.
	ResizingTypeAuto ResizingType = "auto"
)

var resizingTypes = map[string]ResizingType{
	"fit":       ResizingTypeFit,
	"fill":      ResizingTypeFill,
	"fill-down": ResizingTypeFillDown,
	"force":     ResizingTypeForce,
	"auto":      ResizingTypeAuto,
}

// GravityType names the anchor used when cropping or padding.
type GravityType string

const (
	GravityNorth      GravityType = "no"
	GravitySouth      GravityType = "so"
	GravityEast       GravityType = "ea"
	GravityWest       GravityType = "we"
	GravityNorthEast  GravityType = "noea"
	GravityNorthWest  GravityType = "nowe"
	GravitySouthEast  GravityType = "soea"
	GravitySouthWest  GravityType = "sowe"
	GravityCenter     GravityType = "ce"
	GravitySmart      GravityType = "sm"
	GravityFocusPoint GravityType = "fp"
)

var gravityTypes = map[string]GravityType{
	"no":   GravityNorth,
	"so":   GravitySouth,
	"ea":   GravityEast,
	"we":   GravityWest,
	"noea": GravityNorthEast,
	"nowe": GravityNorthWest,
	"soea": GravitySouthEast,
	"sowe": GravitySouthWest,
	"ce":   GravityCenter,
	"sm":   GravitySmart,
	"fp":   GravityFocusPoint,
}

// Gravity is an anchor plus its offsets. For GravityFocusPoint the X and Y
// fields are fractional coordinates in [0,1] rather than pixel offsets.
type Gravity struct {
	Type GravityType
	X    float64
	Y    float64
}

// IsDefault reports whether the gravity is absent or is the documented
// default (centre with no offsets). Canonicalisation drops such gravities.
func (g Gravity) IsDefault() bool {
	return g.Type == "" || (g.Type == GravityCenter && g.X == 0 && g.Y == 0)
}

// String renders the gravity in its canonical form: the bare type when the
// offsets carry no information, otherwise type:x:y. A focus point always
// renders its coordinates because 0:0 is a meaningful corner.
func (g Gravity) String() string {
	if g.Type == "" {
		return ""
	}
	if g.Type == GravityFocusPoint || g.X != 0 || g.Y != 0 {
		return fmt.Sprintf("%s:%s:%s", g.Type, formatFloat(g.X), formatFloat(g.Y))
	}
	return string(g.Type)
}

// Crop is a source region selected before any resize happens. Width and
// Height of 0 mean "the full source dimension"; a value in the open interval
// (0,1) is a fraction of the source dimension; anything >= 1 is absolute
// pixels.
type Crop struct {
	Width   float64
	Height  float64
	Gravity Gravity
}

// IsDefault reports whether the crop selects the whole image, in which case
// it is a no-op and canonicalisation drops it.
func (c Crop) IsDefault() bool { return c.Width == 0 && c.Height == 0 }

// String renders the crop as width:height, with the gravity appended only
// when it is not the default.
func (c Crop) String() string {
	s := formatFloat(c.Width) + ":" + formatFloat(c.Height)
	if !c.Gravity.IsDefault() {
		s += ":" + c.Gravity.String()
	}
	return s
}

// Extend describes padding applied after resize — either out to the
// requested size (Options.Extend) or out to the requested aspect ratio
// (Options.ExtendAspectRatio).
type Extend struct {
	Enabled bool
	Gravity Gravity
}

// IsDefault reports whether the extend is disabled, in which case its
// gravity carries no meaning and canonicalisation drops the whole option.
func (e Extend) IsDefault() bool { return !e.Enabled }

// String renders the extend as "1", with the gravity appended only when it
// is not the default.
func (e Extend) String() string {
	if !e.Enabled {
		return "0"
	}
	if !e.Gravity.IsDefault() {
		return "1:" + e.Gravity.String()
	}
	return "1"
}

// Color is an 8-bit-per-channel opaque colour.
type Color struct {
	R uint8
	G uint8
	B uint8
}

// Hex renders the colour as six lowercase hex digits, which is the
// canonical form regardless of how it was written in the URL.
func (c Color) Hex() string { return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B) }

// Options is the fully parsed, normalised processing-option set for one
// request. Every field's zero value is that option's documented default, so
// the zero Options means "no processing options given".
type Options struct {
	Width             int
	Height            int
	ResizingType      ResizingType
	Enlarge           bool
	Extend            Extend
	ExtendAspectRatio Extend
	Gravity           Gravity
	Crop              Crop
	Background        Color
	HasBackground     bool
	Raw               bool
	SkipProcessing    []string
}

// EffectiveResizingType returns the resizing type to hand to the resizer,
// substituting the documented default when the URL did not name one.
func (o Options) EffectiveResizingType() ResizingType {
	if o.ResizingType == "" {
		return ResizingTypeFit
	}
	return o.ResizingType
}

// SkipsFormat reports whether skip_processing was given and names format,
// meaning the source bytes must be served untouched. The comparison is
// case-insensitive and tolerates a leading dot on the caller's side.
func (o Options) SkipsFormat(format string) bool {
	format = strings.ToLower(strings.TrimPrefix(format, "."))
	if format == "" {
		return false
	}
	for _, ext := range o.SkipProcessing {
		if ext == format {
			return true
		}
	}
	return false
}

// Error is a parse or validation failure carrying the option that caused
// it, so the HTTP layer can return a 400 that names the offending option.
type Error struct {
	Option  string
	Message string
}

func (e *Error) Error() string {
	if e.Option == "" {
		return e.Message
	}
	return fmt.Sprintf("option %q: %s", e.Option, e.Message)
}

func errf(format string, a ...interface{}) error {
	return &Error{Message: fmt.Sprintf(format, a...)}
}

// optionDef binds a canonical long name and an argument applier to every
// spelling (long name plus aliases) that may appear in a URL.
type optionDef struct {
	name    string
	maxArgs int // -1 means variadic
	apply   func(o *Options, args []string) error
}

// optionDefs maps every accepted spelling to its definition. Built once at
// package init from optionList; never mutated afterwards.
var optionDefs = map[string]*optionDef{}

var optionList = []struct {
	name    string
	aliases []string
	maxArgs int
	apply   func(*Options, []string) error
}{
	{name: "resize", aliases: []string{"rs"}, maxArgs: -1, apply: applyResize},
	{name: "size", aliases: []string{"s"}, maxArgs: -1, apply: applySize},
	{name: "width", aliases: []string{"w"}, maxArgs: 1, apply: applyWidth},
	{name: "height", aliases: []string{"h"}, maxArgs: 1, apply: applyHeight},
	{name: "resizing_type", aliases: []string{"rt"}, maxArgs: 1, apply: applyResizingType},
	{name: "enlarge", aliases: []string{"el"}, maxArgs: 1, apply: applyEnlarge},
	{name: "extend", aliases: []string{"ex"}, maxArgs: -1, apply: applyExtend},
	{name: "extend_aspect_ratio", aliases: []string{"extend_ar", "exar"}, maxArgs: -1, apply: applyExtendAspectRatio},
	{name: "gravity", aliases: []string{"g"}, maxArgs: -1, apply: applyGravity},
	{name: "crop", aliases: []string{"c"}, maxArgs: -1, apply: applyCrop},
	{name: "background", aliases: []string{"bg"}, maxArgs: 3, apply: applyBackground},
	{name: "raw", aliases: nil, maxArgs: 1, apply: applyRaw},
	{name: "skip_processing", aliases: []string{"skp"}, maxArgs: -1, apply: applySkipProcessing},
}

func init() {
	for i := range optionList {
		o := optionList[i]
		def := &optionDef{name: o.name, maxArgs: o.maxArgs, apply: o.apply}
		optionDefs[o.name] = def
		for _, alias := range o.aliases {
			optionDefs[alias] = def
		}
	}
}

// argAt returns args[i] or the empty string when the argument was omitted.
// An omitted argument always means "leave this field at its default".
func argAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// formatFloat renders a float in its shortest exact decimal form so that
// 0.5 stays "0.5" and 400 stays "400" rather than "400.000000".
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "1", "t", "true":
		return true, nil
	case "0", "f", "false":
		return false, nil
	}
	return false, errf("invalid boolean %q (expected 1/t/true or 0/f/false)", s)
}

func parseDimension(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, errf("invalid dimension %q (expected a non-negative integer)", s)
	}
	if v < 0 {
		return 0, errf("invalid dimension %q (must not be negative)", s)
	}
	return v, nil
}

func parseFloatDimension(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errf("invalid dimension %q (expected a non-negative number)", s)
	}
	if v < 0 {
		return 0, errf("invalid dimension %q (must not be negative)", s)
	}
	return v, nil
}

func parseOffset(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errf("invalid gravity offset %q (expected a number)", s)
	}
	return v, nil
}

// parseGravity reads a gravity from a flat argument list of the form
// type[:x:y]. Smart gravity is meaningful only where libvips can pick a
// region of interest, so callers that pad rather than crop pass
// allowSmart=false.
func parseGravity(args []string, allowSmart bool) (Gravity, error) {
	var g Gravity
	raw := strings.ToLower(argAt(args, 0))
	if raw == "" {
		return g, nil
	}
	t, ok := gravityTypes[raw]
	if !ok {
		return g, errf("unknown gravity %q (expected no, so, ea, we, noea, nowe, soea, sowe, ce, sm or fp)", raw)
	}
	if t == GravitySmart && !allowSmart {
		return g, errf("gravity %q is not applicable here (smart gravity requires a crop)", raw)
	}
	g.Type = t
	if x := argAt(args, 1); x != "" {
		v, err := parseOffset(x)
		if err != nil {
			return g, err
		}
		g.X = v
	}
	if y := argAt(args, 2); y != "" {
		v, err := parseOffset(y)
		if err != nil {
			return g, err
		}
		g.Y = v
	}
	if len(args) > 3 {
		return g, errf("gravity accepts at most 3 arguments, got %d", len(args))
	}
	if t == GravitySmart && (g.X != 0 || g.Y != 0) {
		return g, errf("smart gravity does not accept offsets")
	}
	return g, nil
}

func parseHexColor(s string) (Color, error) {
	h := strings.ToLower(s)
	if len(h) != 3 && len(h) != 6 {
		return Color{}, errf("invalid colour %q (expected 3 or 6 hex digits)", s)
	}
	if len(h) == 3 {
		// Expand the shorthand: fff -> ffffff, so both spellings
		// canonicalise to the same key.
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, errf("invalid colour %q (expected hex digits)", s)
	}
	return Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}, nil
}

func parseChannel(s string) (uint8, error) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > 255 {
		return 0, errf("invalid colour channel %q (expected 0-255)", s)
	}
	return uint8(v), nil
}

func applyWidth(o *Options, args []string) error {
	s := argAt(args, 0)
	if s == "" {
		return nil
	}
	v, err := parseDimension(s)
	if err != nil {
		return errf("width: %v", err)
	}
	o.Width = v
	return nil
}

func applyHeight(o *Options, args []string) error {
	s := argAt(args, 0)
	if s == "" {
		return nil
	}
	v, err := parseDimension(s)
	if err != nil {
		return errf("height: %v", err)
	}
	o.Height = v
	return nil
}

func applyResizingType(o *Options, args []string) error {
	s := strings.ToLower(argAt(args, 0))
	if s == "" {
		return nil
	}
	t, ok := resizingTypes[s]
	if !ok {
		return errf("unknown resizing type %q (expected fit, fill, fill-down, force or auto)", s)
	}
	o.ResizingType = t
	return nil
}

func applyEnlarge(o *Options, args []string) error {
	s := argAt(args, 0)
	if s == "" {
		return nil
	}
	v, err := parseBool(s)
	if err != nil {
		return errf("enlarge: %v", err)
	}
	o.Enlarge = v
	return nil
}

// parseExtendArgs reads the shared `%extend:%gravity` argument shape used by
// both extend and extend_aspect_ratio.
func parseExtendArgs(args []string) (Extend, error) {
	var e Extend
	if s := argAt(args, 0); s != "" {
		v, err := parseBool(s)
		if err != nil {
			return e, err
		}
		e.Enabled = v
	}
	if len(args) > 1 {
		g, err := parseGravity(args[1:], false)
		if err != nil {
			return e, err
		}
		e.Gravity = g
	}
	return e, nil
}

func applyExtend(o *Options, args []string) error {
	e, err := parseExtendArgs(args)
	if err != nil {
		return err
	}
	o.Extend = e
	return nil
}

func applyExtendAspectRatio(o *Options, args []string) error {
	e, err := parseExtendArgs(args)
	if err != nil {
		return err
	}
	o.ExtendAspectRatio = e
	return nil
}

func applyGravity(o *Options, args []string) error {
	g, err := parseGravity(args, true)
	if err != nil {
		return err
	}
	o.Gravity = g
	return nil
}

func applyCrop(o *Options, args []string) error {
	var c Crop
	if s := argAt(args, 0); s != "" {
		v, err := parseFloatDimension(s)
		if err != nil {
			return errf("crop width: %v", err)
		}
		c.Width = v
	}
	if s := argAt(args, 1); s != "" {
		v, err := parseFloatDimension(s)
		if err != nil {
			return errf("crop height: %v", err)
		}
		c.Height = v
	}
	if len(args) > 2 {
		g, err := parseGravity(args[2:], true)
		if err != nil {
			return err
		}
		c.Gravity = g
	}
	o.Crop = c
	return nil
}

func applyBackground(o *Options, args []string) error {
	nonEmpty := 0
	for _, a := range args {
		if a != "" {
			nonEmpty++
		}
	}
	// No arguments at all disables the background, per imgproxy.
	if nonEmpty == 0 {
		o.HasBackground = false
		o.Background = Color{}
		return nil
	}
	switch len(args) {
	case 1:
		c, err := parseHexColor(args[0])
		if err != nil {
			return err
		}
		o.Background = c
	case 3:
		var c Color
		channels := []*uint8{&c.R, &c.G, &c.B}
		for i, dst := range channels {
			v, err := parseChannel(args[i])
			if err != nil {
				return err
			}
			*dst = v
		}
		o.Background = c
	default:
		return errf("background expects a hex colour or three 0-255 channels, got %d arguments", len(args))
	}
	o.HasBackground = true
	return nil
}

func applyRaw(o *Options, args []string) error {
	s := argAt(args, 0)
	if s == "" {
		return nil
	}
	v, err := parseBool(s)
	if err != nil {
		return errf("raw: %v", err)
	}
	o.Raw = v
	return nil
}

func applySkipProcessing(o *Options, args []string) error {
	var exts []string
	seen := map[string]bool{}
	for _, a := range args {
		if a == "" {
			continue
		}
		ext := strings.ToLower(a)
		if !isExtension(ext) {
			return errf("invalid extension %q (expected letters and digits only)", a)
		}
		if seen[ext] {
			continue
		}
		seen[ext] = true
		exts = append(exts, ext)
	}
	if len(exts) == 0 {
		return errf("skip_processing requires at least one extension")
	}
	o.SkipProcessing = exts
	return nil
}

func isExtension(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// applyResize implements the compound resize:%type:%width:%height:%enlarge:%extend
// by delegating to the atomic appliers, so a compound spelling and the
// equivalent atomic spelling produce identical Options.
func applyResize(o *Options, args []string) error {
	steps := []func(*Options, []string) error{
		applyResizingType, applyWidth, applyHeight, applyEnlarge,
	}
	for i, step := range steps {
		if err := step(o, []string{argAt(args, i)}); err != nil {
			return err
		}
	}
	if len(args) > 4 {
		return applyExtend(o, args[4:])
	}
	return nil
}

// applySize implements the compound size:%width:%height:%enlarge:%extend.
func applySize(o *Options, args []string) error {
	steps := []func(*Options, []string) error{
		applyWidth, applyHeight, applyEnlarge,
	}
	for i, step := range steps {
		if err := step(o, []string{argAt(args, i)}); err != nil {
			return err
		}
	}
	if len(args) > 3 {
		return applyExtend(o, args[3:])
	}
	return nil
}
