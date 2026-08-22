package procopts

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse_SplitsOptionsFromSourceTail(t *testing.T) {
	tests := []struct {
		name     string
		segments []string
		wantOpts Options
		wantTail []string
	}{
		{
			name:     "options then source",
			segments: []string{"w:240", "h:336", "13", "products", "foo.jpg"},
			wantOpts: Options{Width: 240, Height: 336},
			wantTail: []string{"13", "products", "foo.jpg"},
		},
		{
			name:     "no options at all",
			segments: []string{"13", "products", "foo.jpg"},
			wantOpts: Options{},
			wantTail: []string{"13", "products", "foo.jpg"},
		},
		{
			name:     "single source segment",
			segments: []string{"w:240", "foo.jpg"},
			wantOpts: Options{Width: 240},
			wantTail: []string{"foo.jpg"},
		},
		{
			name:     "files form source tail",
			segments: []string{"w:240", "13", "files", "42", "doc.pdf"},
			wantOpts: Options{Width: 240},
			wantTail: []string{"13", "files", "42", "doc.pdf"},
		},
		{
			name:     "tenant group in source tail",
			segments: []string{"w:240", "13-shop", "products", "foo.jpg"},
			wantOpts: Options{Width: 240},
			wantTail: []string{"13-shop", "products", "foo.jpg"},
		},
		{
			name:     "source segment with a dot is not mistaken for an option",
			segments: []string{"rt:fill", "13", "branding", "logo.png.webp"},
			wantOpts: Options{ResizingType: ResizingTypeFill},
			wantTail: []string{"13", "branding", "logo.png.webp"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			opts, tail, err := Parse(tt.segments)
			if err != nil {
				t.Fatalf("Parse(%q) returned unexpected error: %v", tt.segments, err)
			}
			if !reflect.DeepEqual(opts, tt.wantOpts) {
				t.Errorf("options = %+v, want %+v", opts, tt.wantOpts)
			}
			if !reflect.DeepEqual(tail, tt.wantTail) {
				t.Errorf("tail = %q, want %q", tail, tt.wantTail)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name        string
		segments    []string
		wantOption  string
		wantMessage string
	}{
		{
			name:        "unknown option name",
			segments:    []string{"zoom:2", "13", "products", "foo.jpg"},
			wantOption:  "zoom",
			wantMessage: "unknown processing option",
		},
		{
			name:        "unknown option reported by its own spelling",
			segments:    []string{"widht:240", "13", "products", "foo.jpg"},
			wantOption:  "widht",
			wantMessage: "unknown processing option",
		},
		{
			name:        "too many arguments",
			segments:    []string{"w:240:300", "13", "products", "foo.jpg"},
			wantOption:  "w",
			wantMessage: "at most 1",
		},
		{
			name:        "non-numeric width",
			segments:    []string{"w:abc", "13", "products", "foo.jpg"},
			wantOption:  "w",
			wantMessage: "invalid dimension",
		},
		{
			name:        "negative height",
			segments:    []string{"h:-10", "13", "products", "foo.jpg"},
			wantOption:  "h",
			wantMessage: "invalid dimension",
		},
		{
			name:        "unknown resizing type",
			segments:    []string{"rt:squish", "13", "products", "foo.jpg"},
			wantOption:  "rt",
			wantMessage: "unknown resizing type",
		},
		{
			name:        "invalid boolean",
			segments:    []string{"el:yes", "13", "products", "foo.jpg"},
			wantOption:  "el",
			wantMessage: "invalid boolean",
		},
		{
			name:        "bad hex colour",
			segments:    []string{"bg:xyz", "13", "products", "foo.jpg"},
			wantOption:  "bg",
			wantMessage: "invalid colour",
		},
		{
			name:        "hex colour of the wrong length",
			segments:    []string{"bg:ffff", "13", "products", "foo.jpg"},
			wantOption:  "bg",
			wantMessage: "3 or 6 hex digits",
		},
		{
			name:        "colour channel out of range",
			segments:    []string{"bg:256:0:0", "13", "products", "foo.jpg"},
			wantOption:  "bg",
			wantMessage: "0-255",
		},
		{
			name:        "two colour arguments is neither form",
			segments:    []string{"bg:255:255", "13", "products", "foo.jpg"},
			wantOption:  "bg",
			wantMessage: "hex colour or three",
		},
		{
			name:        "unknown gravity",
			segments:    []string{"g:up", "13", "products", "foo.jpg"},
			wantOption:  "g",
			wantMessage: "unknown gravity",
		},
		{
			name:        "smart gravity rejected on extend",
			segments:    []string{"ex:1:sm", "13", "products", "foo.jpg"},
			wantOption:  "ex",
			wantMessage: "not applicable",
		},
		{
			name:        "smart gravity rejected with offsets",
			segments:    []string{"g:sm:1:2", "13", "products", "foo.jpg"},
			wantOption:  "g",
			wantMessage: "does not accept offsets",
		},
		{
			name:        "non-numeric crop dimension",
			segments:    []string{"c:wide:300", "13", "products", "foo.jpg"},
			wantOption:  "c",
			wantMessage: "crop width",
		},
		{
			name:        "empty skip_processing",
			segments:    []string{"skp:", "13", "products", "foo.jpg"},
			wantOption:  "skp",
			wantMessage: "at least one extension",
		},
		{
			name:        "invalid skip_processing extension",
			segments:    []string{"skp:pn-g", "13", "products", "foo.jpg"},
			wantOption:  "skp",
			wantMessage: "invalid extension",
		},
		{
			name:        "crop gravity unknown",
			segments:    []string{"c:400:300:up", "13", "products", "foo.jpg"},
			wantOption:  "c",
			wantMessage: "unknown gravity",
		},
		{
			name:        "crop height negative",
			segments:    []string{"c:400:-300", "13", "products", "foo.jpg"},
			wantOption:  "c",
			wantMessage: "crop height",
		},
		{
			name:        "gravity offset not a number",
			segments:    []string{"g:no:x:2", "13", "products", "foo.jpg"},
			wantOption:  "g",
			wantMessage: "invalid gravity offset",
		},
		{
			name:        "gravity second offset not a number",
			segments:    []string{"g:no:1:y", "13", "products", "foo.jpg"},
			wantOption:  "g",
			wantMessage: "invalid gravity offset",
		},
		{
			name:        "too many gravity arguments",
			segments:    []string{"g:no:1:2:3", "13", "products", "foo.jpg"},
			wantOption:  "g",
			wantMessage: "at most 3 arguments",
		},
		{
			name:        "crop gravity offset not a number",
			segments:    []string{"c:0.5:0.5:fp:a:2", "13", "products", "foo.jpg"},
			wantOption:  "c",
			wantMessage: "invalid gravity offset",
		},
		{
			name:        "extend boolean invalid",
			segments:    []string{"ex:maybe", "13", "products", "foo.jpg"},
			wantOption:  "ex",
			wantMessage: "invalid boolean",
		},
		{
			name:        "extend_aspect_ratio boolean invalid",
			segments:    []string{"exar:maybe", "13", "products", "foo.jpg"},
			wantOption:  "exar",
			wantMessage: "invalid boolean",
		},
		{
			name:        "extend_aspect_ratio gravity unknown",
			segments:    []string{"exar:1:up", "13", "products", "foo.jpg"},
			wantOption:  "exar",
			wantMessage: "unknown gravity",
		},
		{
			name:        "raw boolean invalid",
			segments:    []string{"raw:maybe", "13", "products", "foo.jpg"},
			wantOption:  "raw",
			wantMessage: "invalid boolean",
		},
		{
			name:        "compound resize propagates a width error",
			segments:    []string{"rs:fill:abc", "13", "products", "foo.jpg"},
			wantOption:  "rs",
			wantMessage: "width",
		},
		{
			name:        "compound resize propagates a resizing type error",
			segments:    []string{"rs:squish:240", "13", "products", "foo.jpg"},
			wantOption:  "rs",
			wantMessage: "unknown resizing type",
		},
		{
			name:        "compound resize propagates an extend error",
			segments:    []string{"rs:fill:240:336:1:maybe", "13", "products", "foo.jpg"},
			wantOption:  "rs",
			wantMessage: "invalid boolean",
		},
		{
			name:        "compound size propagates a width error",
			segments:    []string{"s:abc", "13", "products", "foo.jpg"},
			wantOption:  "s",
			wantMessage: "width",
		},
		{
			name:        "compound size propagates a height error",
			segments:    []string{"s:240:-1", "13", "products", "foo.jpg"},
			wantOption:  "s",
			wantMessage: "height",
		},
		{
			name:        "compound size propagates an enlarge error",
			segments:    []string{"s:240:336:maybe", "13", "products", "foo.jpg"},
			wantOption:  "s",
			wantMessage: "invalid boolean",
		},
		{
			name:        "compound size propagates an extend error",
			segments:    []string{"s:240:336:0:maybe", "13", "products", "foo.jpg"},
			wantOption:  "s",
			wantMessage: "invalid boolean",
		},
		{
			name:        "missing source path",
			segments:    []string{"w:240", "h:336"},
			wantOption:  "",
			wantMessage: "missing source path",
		},
		{
			name:        "no segments at all",
			segments:    nil,
			wantOption:  "",
			wantMessage: "missing source path",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse(tt.segments)
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want an error", tt.segments)
			}
			perr, ok := err.(*Error)
			if !ok {
				t.Fatalf("Parse(%q) returned %T, want *procopts.Error", tt.segments, err)
			}
			if perr.Option != tt.wantOption {
				t.Errorf("Option = %q, want %q", perr.Option, tt.wantOption)
			}
			if !strings.Contains(perr.Message, tt.wantMessage) {
				t.Errorf("Message = %q, want it to contain %q", perr.Message, tt.wantMessage)
			}
			// The rendered error must name the offending option so the
			// HTTP layer can hand it straight to the client.
			if tt.wantOption != "" && !strings.Contains(err.Error(), tt.wantOption) {
				t.Errorf("Error() = %q, want it to name option %q", err.Error(), tt.wantOption)
			}
		})
	}
}

func TestParse_DoesNotMutateOnError(t *testing.T) {
	opts, tail, err := Parse([]string{"w:240", "rt:squish", "13", "products", "foo.jpg"})
	if err == nil {
		t.Fatal("Parse returned nil error, want an error")
	}
	if !reflect.DeepEqual(opts, Options{}) {
		t.Errorf("options = %+v, want the zero value on error", opts)
	}
	if tail != nil {
		t.Errorf("tail = %q, want nil on error", tail)
	}
}

func TestSplitSegment(t *testing.T) {
	tests := []struct {
		seg      string
		wantName string
		wantArgs []string
		wantOK   bool
	}{
		{"w:240", "w", []string{"240"}, true},
		{"skip_processing:png:webp", "skip_processing", []string{"png", "webp"}, true},
		{"BG:fff", "bg", []string{"fff"}, true},
		{"bg:", "bg", []string{""}, true},
		{"foo.jpg", "", nil, false},
		{"13", "", nil, false},
		{":240", "", nil, false},
		{"", "", nil, false},
		{"logo.png.webp", "", nil, false},
		{"9lives:1", "", nil, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.seg, func(t *testing.T) {
			name, args, ok := splitSegment(tt.seg)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("args = %q, want %q", args, tt.wantArgs)
			}
		})
	}
}
