package procopts

import (
	"errors"
	"fmt"
	"strings"
)

// Parse splits a /_p/ URL's segments into processing options and the source
// tail, and applies every option in order.
//
// A segment is an option when it looks like `name:...` — an ASCII-letter and
// underscore name followed by a colon. The source tail begins at the first
// segment that does not have that shape, and runs to the end. Once a segment
// is recognised as an option, an unknown name is an error rather than the
// start of the tail, so a typo returns a descriptive 400 instead of silently
// becoming part of a source key that will 404.
//
// A later option overrides an earlier one of the same name, matching
// imgproxy. Canonicalisation makes such duplicates converge.
func Parse(segments []string) (Options, []string, error) {
	var o Options

	i := 0
	for ; i < len(segments); i++ {
		name, args, ok := splitSegment(segments[i])
		if !ok {
			break
		}
		def, known := optionDefs[name]
		if !known {
			return Options{}, nil, &Error{Option: name, Message: "unknown processing option"}
		}
		if def.maxArgs >= 0 && len(args) > def.maxArgs {
			return Options{}, nil, &Error{
				Option:  name,
				Message: fmt.Sprintf("expects at most %d argument(s), got %d", def.maxArgs, len(args)),
			}
		}
		if err := def.apply(&o, args); err != nil {
			return Options{}, nil, &Error{Option: name, Message: message(err)}
		}
	}

	tail := segments[i:]
	if len(tail) == 0 {
		return Options{}, nil, &Error{Message: "missing source path"}
	}
	return o, tail, nil
}

// splitSegment recognises the `name:arg:arg` option shape. It reports ok=false
// for anything else, which is how the caller finds the boundary between the
// options and the source tail.
func splitSegment(seg string) (name string, args []string, ok bool) {
	idx := strings.Index(seg, ":")
	if idx <= 0 {
		return "", nil, false
	}
	name = seg[:idx]
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' {
			return "", nil, false
		}
	}
	// "bg:" yields a single empty argument, which the appliers read as
	// "explicitly cleared" rather than "absent".
	return strings.ToLower(name), strings.Split(seg[idx+1:], ":"), true
}

// message unwraps a parse error down to its bare text so the caller can
// re-wrap it with the option name that was actually written in the URL.
func message(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
