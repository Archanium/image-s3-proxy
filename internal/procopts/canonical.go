package procopts

import (
	"sort"
	"strconv"
	"strings"
)

// EmptyCanonical is the canonical form of an empty option set. It is a
// literal placeholder rather than an empty string so that the cache key
// never contains an empty path segment.
const EmptyCanonical = "_"

// Canonical renders the options as a stable string suitable for embedding in
// an S3 cache key.
//
// The result is invariant under option order, alias choice, compound-vs-atomic
// spelling, and explicitly-writing-a-default, so every URL that requests the
// same transformation maps to exactly one cached object. Options sitting at
// their documented default are omitted entirely.
//
// The output contains only characters that are safe unescaped in both an S3
// key and a URL path segment, and never contains a slash.
func (o Options) Canonical() string {
	type pair struct{ name, value string }
	var parts []pair
	add := func(name, value string) { parts = append(parts, pair{name, value}) }

	if o.HasBackground {
		add("background", o.Background.Hex())
	}
	if !o.Crop.IsDefault() {
		add("crop", o.Crop.String())
	}
	if o.Enlarge {
		add("enlarge", "1")
	}
	if !o.Extend.IsDefault() {
		add("extend", o.Extend.String())
	}
	if !o.ExtendAspectRatio.IsDefault() {
		add("extend_aspect_ratio", o.ExtendAspectRatio.String())
	}
	if !o.Gravity.IsDefault() {
		add("gravity", o.Gravity.String())
	}
	if o.Height != 0 {
		add("height", strconv.Itoa(o.Height))
	}
	if o.Raw {
		add("raw", "1")
	}
	// fit is the documented default, so naming it explicitly must not
	// produce a different cache key than omitting it.
	if rt := o.ResizingType; rt != "" && rt != ResizingTypeFit {
		add("resizing_type", string(rt))
	}
	if len(o.SkipProcessing) > 0 {
		exts := append([]string(nil), o.SkipProcessing...)
		sort.Strings(exts)
		add("skip_processing", strings.Join(exts, ":"))
	}
	if o.Width != 0 {
		add("width", strconv.Itoa(o.Width))
	}

	if len(parts) == 0 {
		return EmptyCanonical
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].name < parts[j].name })

	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(p.name)
		sb.WriteByte('=')
		sb.WriteString(p.value)
	}
	return sb.String()
}
