package procopts

import "fmt"

// Validate rejects option sets that would ask libvips for an unreasonable
// amount of work.
//
// maxDimension bounds the requested width and height and any absolute crop
// dimension. A crop dimension below 1 is a fraction of the source rather
// than a pixel count, so it is never over the bound. A maxDimension of 0
// disables the check.
//
// This exists because the legacy URL families cap width and height at four
// digits in the regex itself. A free-form option vocabulary removes that
// bound, and an uncapped resize endpoint is an out-of-memory vector.
func (o Options) Validate(maxDimension int) error {
	if maxDimension <= 0 {
		return nil
	}
	limit := float64(maxDimension)
	for _, c := range []struct {
		name  string
		value float64
	}{
		{"width", float64(o.Width)},
		{"height", float64(o.Height)},
		{"crop width", o.Crop.Width},
		{"crop height", o.Crop.Height},
	} {
		if c.value > limit {
			return &Error{Message: fmt.Sprintf(
				"%s %s exceeds the maximum of %d", c.name, formatFloat(c.value), maxDimension)}
		}
	}
	return nil
}
