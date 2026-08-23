package procopts

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		segments []string
		max      int
		wantErr  string
	}{
		{name: "within the bound", segments: []string{"w:240", "h:336"}, max: 5120},
		{name: "exactly at the bound", segments: []string{"w:5120", "h:5120"}, max: 5120},
		{name: "no options", segments: []string{}, max: 5120},
		{name: "a relative crop is never over the bound", segments: []string{"c:0.5:0.5"}, max: 1},
		{name: "the check is disabled at zero", segments: []string{"w:99999"}, max: 0},
		{name: "the check is disabled when negative", segments: []string{"w:99999"}, max: -1},

		{name: "width over the bound", segments: []string{"w:5121"}, max: 5120, wantErr: "width 5121 exceeds the maximum of 5120"},
		{name: "height over the bound", segments: []string{"h:9000"}, max: 5120, wantErr: "height 9000 exceeds"},
		{name: "crop width over the bound", segments: []string{"c:99999:10"}, max: 5120, wantErr: "crop width 99999 exceeds"},
		{name: "crop height over the bound", segments: []string{"c:10:99999"}, max: 5120, wantErr: "crop height 99999 exceeds"},
		{name: "compound resize is bounded too", segments: []string{"rs:fill:99999:10"}, max: 5120, wantErr: "width 99999 exceeds"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			opts := parseOptions(t, tt.segments...)
			err := opts.Validate(tt.max)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate(%d) = %v, want nil", tt.max, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%d) = nil, want an error containing %q", tt.max, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate(%d) = %q, want it to contain %q", tt.max, err.Error(), tt.wantErr)
			}
		})
	}
}
