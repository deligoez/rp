package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deligoez/rp/internal/manifest"
)

func TestParseDiffDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "7d", want: 7 * 24 * time.Hour},
		{in: "1d", want: 24 * time.Hour},
		{in: "0d", want: 0},
		{in: "24h", want: 24 * time.Hour},
		{in: "1h", want: time.Hour},
		{in: "365d", want: 365 * 24 * time.Hour},

		{in: "", wantErr: true},
		{in: "d", wantErr: true},
		{in: "7", wantErr: true},
		{in: "7w", wantErr: true},
		{in: "-1d", wantErr: true},
		{in: "1.5d", wantErr: true},
		{in: "7D", wantErr: true},
		{in: "abcd", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseDiffDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseDiffDuration(%q) = %v, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDiffDuration(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseDiffDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDiffCutoffWithoutFlag(t *testing.T) {
	diffSince = ""
	t.Cleanup(func() { diffSince = "" })

	has, cutoff, err := parseDiffCutoff()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Error("hasSince must be false when --since was not given")
	}
	if !cutoff.IsZero() {
		t.Errorf("cutoff = %v, want the zero time when unused", cutoff)
	}
}

