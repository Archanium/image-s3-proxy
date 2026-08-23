package procopts

import (
	"strings"
	"testing"
)

func canonicalOf(t *testing.T, segments ...string) string {
	t.Helper()
	return parseOptions(t, segments...).Canonical()
}

func TestCanonical_EmptyOptionSet(t *testing.T) {
	if got := (Options{}).Canonical(); got != EmptyCanonical {
		t.Errorf("Canonical() = %q, want %q", got, EmptyCanonical)
	}
	if got := canonicalOf(t); got != EmptyCanonical {
		t.Errorf("Canonical() of a parsed empty option set = %q, want %q", got, EmptyCanonical)
	}
}

func TestCanonical_RendersEachOption(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    string
	}{
		{"width", "w:240", "width=240"},
		{"height", "h:336", "height=336"},
		{"resizing type", "rt:fill", "resizing_type=fill"},
		{"enlarge", "el:1", "enlarge=1"},
		{"extend", "ex:1", "extend=1"},
		{"extend with gravity", "ex:1:so", "extend=1:so"},
		{"extend with offsets", "ex:1:so:10:20", "extend=1:so:10:20"},
		{"extend aspect ratio", "exar:1", "extend_aspect_ratio=1"},
		{"gravity", "g:no", "gravity=no"},
		{"gravity smart", "g:sm", "gravity=sm"},
		{"gravity focus point", "g:fp:0.5:0.25", "gravity=fp:0.5:0.25"},
		{"crop absolute", "c:400:300", "crop=400:300"},
		{"crop relative", "c:0.5:0.5", "crop=0.5:0.5"},
		{"crop with gravity", "c:400:300:sm", "crop=400:300:sm"},
		{"background", "bg:1a2b3c", "background=1a2b3c"},
		{"raw", "raw:1", "raw=1"},
		{"skip processing", "skp:png:webp", "skip_processing=png:webp"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalOf(t, tt.segment); got != tt.want {
				t.Errorf("Canonical(%q) = %q, want %q", tt.segment, got, tt.want)
			}
		})
	}
}

func TestCanonical_DropsDocumentedDefaults(t *testing.T) {
	tests := []struct {
		name    string
		segment string
	}{
		{"explicit zero width", "w:0"},
		{"explicit zero height", "h:0"},
		{"explicit fit resizing type", "rt:fit"},
		{"explicit enlarge off", "el:0"},
		{"explicit extend off", "ex:0"},
		{"explicit extend off with gravity", "ex:0:so"},
		{"explicit extend_aspect_ratio off", "exar:0"},
		{"explicit centre gravity", "g:ce"},
		{"centre gravity with zero offsets", "g:ce:0:0"},
		{"crop covering the whole image", "c:0:0"},
		{"crop covering the whole image with gravity", "c:0:0:sm"},
		{"cleared background", "bg:"},
		{"explicit raw off", "raw:0"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalOf(t, tt.segment); got != EmptyCanonical {
				t.Errorf("Canonical(%q) = %q, want %q (a default must not appear in the key)", tt.segment, got, EmptyCanonical)
			}
		})
	}
}

// TestCanonical_EquivalenceClasses is the load-bearing test for the cache
// contract: every spelling of the same transformation must map to exactly
// one cached object.
func TestCanonical_EquivalenceClasses(t *testing.T) {
	classes := []struct {
		name      string
		want      string
		spellings [][]string
	}{
		{
			name: "spec worked example",
			want: "background=ffffff,height=336,resizing_type=fill,width=240",
			spellings: [][]string{
				{"h:336", "rs:fill", "w:240", "bg:ffffff"},
				{"rt:fill", "w:240", "h:336", "bg:fff"},
				{"background:255:255:255", "width:240", "height:336", "resizing_type:fill"},
				{"rs:fill:240:336", "bg:FFF"},
			},
		},
		{
			name: "option order does not matter",
			want: "enlarge=1,height=336,width=240",
			spellings: [][]string{
				{"w:240", "h:336", "el:1"},
				{"el:1", "h:336", "w:240"},
				{"h:336", "w:240", "el:1"},
			},
		},
		{
			name: "compound and atomic agree",
			want: "enlarge=1,height=336,resizing_type=force,width=240",
			spellings: [][]string{
				{"rs:force:240:336:1"},
				{"s:240:336:1", "rt:force"},
				{"rt:force", "w:240", "h:336", "el:true"},
			},
		},
		{
			name: "boolean spellings agree",
			want: "enlarge=1",
			spellings: [][]string{
				{"el:1"}, {"el:t"}, {"el:true"}, {"enlarge:TRUE"},
			},
		},
		{
			name: "stating a default matches omitting it",
			want: "width=240",
			spellings: [][]string{
				{"w:240"},
				{"w:240", "rt:fit"},
				{"w:240", "el:0"},
				{"w:240", "h:0"},
				{"w:240", "g:ce"},
				{"rs:fit:240:0:0"},
			},
		},
		{
			name: "duplicate options collapse to the last value",
			want: "width=200",
			spellings: [][]string{
				{"w:200"},
				{"w:100", "w:200"},
				{"width:999", "w:100", "w:200"},
			},
		},
		{
			name: "skip_processing order does not matter",
			want: "skip_processing=avif:png:webp",
			spellings: [][]string{
				{"skp:png:webp:avif"},
				{"skp:avif:webp:png"},
				{"skip_processing:webp:avif:png:png"},
			},
		},
	}

	for _, class := range classes {
		class := class
		t.Run(class.name, func(t *testing.T) {
			for _, spelling := range class.spellings {
				if got := canonicalOf(t, spelling...); got != class.want {
					t.Errorf("Canonical(%q) = %q, want %q", spelling, got, class.want)
				}
			}
		})
	}
}

func TestCanonical_SortedByOptionName(t *testing.T) {
	got := canonicalOf(t, "w:240", "raw:1", "bg:fff", "ex:1", "exar:1", "h:336", "el:1", "g:no", "c:10:10", "rt:fill", "skp:png")
	want := "background=ffffff,crop=10:10,enlarge=1,extend=1,extend_aspect_ratio=1," +
		"gravity=no,height=336,raw=1,resizing_type=fill,skip_processing=png,width=240"
	if got != want {
		t.Errorf("Canonical() = %q, want %q", got, want)
	}
}

// TestCanonical_IsKeySafe guards the S3-key and URL-path contract: the
// canonical string is embedded as a single path segment, so it must never
// contain a slash or whitespace.
func TestCanonical_IsKeySafe(t *testing.T) {
	got := canonicalOf(t, "w:240", "h:336", "bg:fff", "c:0.5:0.5:fp:0.1:0.2", "skp:png:webp", "ex:1:soea:5:6")
	for _, bad := range []string{"/", " ", "\t", "\n", "?", "#", "%"} {
		if strings.Contains(got, bad) {
			t.Errorf("Canonical() = %q, must not contain %q", got, bad)
		}
	}
}
