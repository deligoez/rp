package cmd

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	withVersion := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}

	cases := []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{
			name:    "a stamped version wins over the build info",
			stamped: "v1.2.3",
			info:    withVersion("v9.9.9"),
			ok:      true,
			want:    "v1.2.3",
		},
		{
			name:    "an unstamped build falls back to the module version",
			stamped: "dev",
			info:    withVersion("v0.9.0"),
			ok:      true,
			want:    "v0.9.0",
		},
		{
			name:    "no build info at all stays dev",
			stamped: "dev",
			info:    nil,
			ok:      false,
			want:    "dev",
		},
		{
			name:    "an empty module version stays dev",
			stamped: "dev",
			info:    withVersion(""),
			ok:      true,
			want:    "dev",
		},
		{
			name:    "a local build reports devel, which stays dev",
			stamped: "dev",
			info:    withVersion("(devel)"),
			ok:      true,
			want:    "dev",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.stamped, tc.info, tc.ok); got != tc.want {
				t.Errorf("resolveVersion(%q, %v, %v) = %q, want %q", tc.stamped, tc.info, tc.ok, got, tc.want)
			}
		})
	}
}
