package procopts

import (
	"reflect"
	"testing"
)

// source is the trailing segment set appended to every parse case so that
// Parse has a non-empty source tail to return.
var source = []string{"13", "products", "foo.jpg"}

func parseOptions(t *testing.T, segments ...string) Options {
	t.Helper()
	opts, tail, err := Parse(append(append([]string{}, segments...), source...))
	if err != nil {
		t.Fatalf("Parse(%q) returned unexpected error: %v", segments, err)
	}
	if !reflect.DeepEqual(tail, source) {
		t.Fatalf("Parse(%q) tail = %q, want %q", segments, tail, source)
	}
	return opts
}

func TestParse_OptionSpellings(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    Options
	}{
		{"width long name", "width:240", Options{Width: 240}},
		{"width alias", "w:240", Options{Width: 240}},
		{"width zero is explicit default", "w:0", Options{}},
		{"height long name", "height:336", Options{Height: 336}},
		{"height alias", "h:336", Options{Height: 336}},

		{"resizing type fit", "resizing_type:fit", Options{ResizingType: ResizingTypeFit}},
		{"resizing type fill", "rt:fill", Options{ResizingType: ResizingTypeFill}},
		{"resizing type fill-down", "rt:fill-down", Options{ResizingType: ResizingTypeFillDown}},
		{"resizing type force", "rt:force", Options{ResizingType: ResizingTypeForce}},
		{"resizing type auto", "rt:auto", Options{ResizingType: ResizingTypeAuto}},
		{"resizing type uppercase", "rt:FILL", Options{ResizingType: ResizingTypeFill}},

		{"enlarge 1", "enlarge:1", Options{Enlarge: true}},
		{"enlarge t", "el:t", Options{Enlarge: true}},
		{"enlarge true", "el:true", Options{Enlarge: true}},
		{"enlarge 0", "el:0", Options{Enlarge: false}},
		{"enlarge f", "el:f", Options{Enlarge: false}},
		{"enlarge false", "el:false", Options{Enlarge: false}},

		{"extend long name", "extend:1", Options{Extend: Extend{Enabled: true}}},
		{"extend alias", "ex:1", Options{Extend: Extend{Enabled: true}}},
		{"extend with gravity", "ex:1:so", Options{Extend: Extend{Enabled: true, Gravity: Gravity{Type: GravitySouth}}}},
		{"extend with gravity offsets", "ex:1:so:10:20", Options{Extend: Extend{Enabled: true, Gravity: Gravity{Type: GravitySouth, X: 10, Y: 20}}}},

		{"extend_aspect_ratio long name", "extend_aspect_ratio:1", Options{ExtendAspectRatio: Extend{Enabled: true}}},
		{"extend_aspect_ratio alias extend_ar", "extend_ar:1", Options{ExtendAspectRatio: Extend{Enabled: true}}},
		{"extend_aspect_ratio alias exar", "exar:1:no", Options{ExtendAspectRatio: Extend{Enabled: true, Gravity: Gravity{Type: GravityNorth}}}},

		{"gravity long name", "gravity:no", Options{Gravity: Gravity{Type: GravityNorth}}},
		{"gravity alias", "g:ce", Options{Gravity: Gravity{Type: GravityCenter}}},
		{"gravity smart", "g:sm", Options{Gravity: Gravity{Type: GravitySmart}}},
		{"gravity focus point", "g:fp:0.5:0.25", Options{Gravity: Gravity{Type: GravityFocusPoint, X: 0.5, Y: 0.25}}},
		{"gravity with offsets", "g:soea:10:20", Options{Gravity: Gravity{Type: GravitySouthEast, X: 10, Y: 20}}},

		{"crop long name absolute", "crop:400:300", Options{Crop: Crop{Width: 400, Height: 300}}},
		{"crop alias absolute", "c:400:300", Options{Crop: Crop{Width: 400, Height: 300}}},
		{"crop relative", "c:0.5:0.5", Options{Crop: Crop{Width: 0.5, Height: 0.5}}},
		{"crop with smart gravity", "c:400:300:sm", Options{Crop: Crop{Width: 400, Height: 300, Gravity: Gravity{Type: GravitySmart}}}},
		{"crop with gravity offsets", "c:400:300:no:5:6", Options{Crop: Crop{Width: 400, Height: 300, Gravity: Gravity{Type: GravityNorth, X: 5, Y: 6}}}},

		{"background hex shorthand", "background:fff", Options{Background: Color{255, 255, 255}, HasBackground: true}},
		{"background hex full", "bg:ffffff", Options{Background: Color{255, 255, 255}, HasBackground: true}},
		{"background hex uppercase", "bg:FFFFFF", Options{Background: Color{255, 255, 255}, HasBackground: true}},
		{"background channels", "bg:255:255:255", Options{Background: Color{255, 255, 255}, HasBackground: true}},
		{"background non-white", "bg:1a2b3c", Options{Background: Color{0x1a, 0x2b, 0x3c}, HasBackground: true}},
		{"background disabled by empty args", "bg:", Options{}},

		{"raw enabled", "raw:1", Options{Raw: true}},
		{"raw disabled", "raw:0", Options{}},

		{"skip_processing long name", "skip_processing:png", Options{SkipProcessing: []string{"png"}}},
		{"skip_processing alias multi", "skp:png:webp", Options{SkipProcessing: []string{"png", "webp"}}},
		{"skip_processing normalises case", "skp:PNG", Options{SkipProcessing: []string{"png"}}},
		{"skip_processing dedupes", "skp:png:png", Options{SkipProcessing: []string{"png"}}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := parseOptions(t, tt.segment)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.segment, got, tt.want)
			}
		})
	}
}

func TestParse_CompoundOptions(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    Options
	}{
		{
			name:    "resize full arity",
			segment: "resize:fill:240:336:1",
			want:    Options{ResizingType: ResizingTypeFill, Width: 240, Height: 336, Enlarge: true},
		},
		{
			name:    "resize alias",
			segment: "rs:fill:240:336:1",
			want:    Options{ResizingType: ResizingTypeFill, Width: 240, Height: 336, Enlarge: true},
		},
		{
			name:    "resize partial arity",
			segment: "rs:fill",
			want:    Options{ResizingType: ResizingTypeFill},
		},
		{
			name:    "resize skips omitted arguments",
			segment: "rs::240::1",
			want:    Options{Width: 240, Enlarge: true},
		},
		{
			name:    "resize with extend group",
			segment: "rs:fill:240:336:0:1:so",
			want: Options{
				ResizingType: ResizingTypeFill, Width: 240, Height: 336,
				Extend: Extend{Enabled: true, Gravity: Gravity{Type: GravitySouth}},
			},
		},
		{
			name:    "size full arity",
			segment: "size:240:336:1",
			want:    Options{Width: 240, Height: 336, Enlarge: true},
		},
		{
			name:    "size alias",
			segment: "s:240:336",
			want:    Options{Width: 240, Height: 336},
		},
		{
			name:    "size with extend group",
			segment: "s:240:336:0:1",
			want:    Options{Width: 240, Height: 336, Extend: Extend{Enabled: true}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := parseOptions(t, tt.segment)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.segment, got, tt.want)
			}
		})
	}
}

func TestParse_CompoundMatchesAtomicSpelling(t *testing.T) {
	compound := parseOptions(t, "rs:fill:240:336:1")
	atomic := parseOptions(t, "rt:fill", "w:240", "h:336", "el:1")
	if !reflect.DeepEqual(compound, atomic) {
		t.Errorf("compound %+v != atomic %+v", compound, atomic)
	}
}

func TestParse_LastOptionWins(t *testing.T) {
	got := parseOptions(t, "w:100", "width:200")
	if got.Width != 200 {
		t.Errorf("Width = %d, want 200 (a later option must override an earlier one)", got.Width)
	}
}

func TestOptions_EffectiveResizingType(t *testing.T) {
	if got := (Options{}).EffectiveResizingType(); got != ResizingTypeFit {
		t.Errorf("unset EffectiveResizingType() = %q, want %q", got, ResizingTypeFit)
	}
	if got := (Options{ResizingType: ResizingTypeForce}).EffectiveResizingType(); got != ResizingTypeForce {
		t.Errorf("EffectiveResizingType() = %q, want %q", got, ResizingTypeForce)
	}
}

func TestOptions_SkipsFormat(t *testing.T) {
	o := parseOptions(t, "skp:png:webp")
	tests := []struct {
		format string
		want   bool
	}{
		{"png", true},
		{"PNG", true},
		{".png", true},
		{"webp", true},
		{"jpg", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := o.SkipsFormat(tt.format); got != tt.want {
			t.Errorf("SkipsFormat(%q) = %v, want %v", tt.format, got, tt.want)
		}
	}
	if (Options{}).SkipsFormat("png") {
		t.Error("SkipsFormat on empty options = true, want false")
	}
}

func TestValueTypes_String(t *testing.T) {
	t.Run("gravity", func(t *testing.T) {
		tests := []struct {
			g    Gravity
			want string
		}{
			{Gravity{}, ""},
			{Gravity{Type: GravityCenter}, "ce"},
			{Gravity{Type: GravitySouth, X: 10, Y: 20}, "so:10:20"},
			{Gravity{Type: GravitySouth, X: 0, Y: 20}, "so:0:20"},
			// A focus point always renders its coordinates because 0:0 is
			// a meaningful corner rather than "no offset".
			{Gravity{Type: GravityFocusPoint}, "fp:0:0"},
			{Gravity{Type: GravityFocusPoint, X: 0.5, Y: 0.25}, "fp:0.5:0.25"},
		}
		for _, tt := range tests {
			if got := tt.g.String(); got != tt.want {
				t.Errorf("Gravity%+v.String() = %q, want %q", tt.g, got, tt.want)
			}
		}
	})

	t.Run("extend", func(t *testing.T) {
		tests := []struct {
			e    Extend
			want string
		}{
			{Extend{}, "0"},
			{Extend{Gravity: Gravity{Type: GravityNorth}}, "0"},
			{Extend{Enabled: true}, "1"},
			{Extend{Enabled: true, Gravity: Gravity{Type: GravityCenter}}, "1"},
			{Extend{Enabled: true, Gravity: Gravity{Type: GravityNorth}}, "1:no"},
			{Extend{Enabled: true, Gravity: Gravity{Type: GravityNorth, X: 1, Y: 2}}, "1:no:1:2"},
		}
		for _, tt := range tests {
			if got := tt.e.String(); got != tt.want {
				t.Errorf("Extend%+v.String() = %q, want %q", tt.e, got, tt.want)
			}
		}
	})

	t.Run("crop", func(t *testing.T) {
		tests := []struct {
			c    Crop
			want string
		}{
			{Crop{Width: 400, Height: 300}, "400:300"},
			{Crop{Width: 0.5, Height: 0.25}, "0.5:0.25"},
			{Crop{Width: 400, Height: 300, Gravity: Gravity{Type: GravityCenter}}, "400:300"},
			{Crop{Width: 400, Height: 300, Gravity: Gravity{Type: GravitySmart}}, "400:300:sm"},
			{Crop{Width: 400, Height: 0, Gravity: Gravity{Type: GravityNorth, X: 5, Y: 6}}, "400:0:no:5:6"},
		}
		for _, tt := range tests {
			if got := tt.c.String(); got != tt.want {
				t.Errorf("Crop%+v.String() = %q, want %q", tt.c, got, tt.want)
			}
		}
	})

	t.Run("color", func(t *testing.T) {
		tests := []struct {
			c    Color
			want string
		}{
			{Color{}, "000000"},
			{Color{255, 255, 255}, "ffffff"},
			{Color{0x1a, 0x2b, 0x3c}, "1a2b3c"},
			{Color{1, 2, 3}, "010203"},
		}
		for _, tt := range tests {
			if got := tt.c.Hex(); got != tt.want {
				t.Errorf("Color%+v.Hex() = %q, want %q", tt.c, got, tt.want)
			}
		}
	})
}

func TestIsExtension(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"png", true},
		{"webp", true},
		{"jp2", true},
		{"", false},
		{"pn-g", false},
		{"pn.g", false},
		{"PNG", false}, // callers lowercase before validating
	}
	for _, tt := range tests {
		if got := isExtension(tt.s); got != tt.want {
			t.Errorf("isExtension(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}
