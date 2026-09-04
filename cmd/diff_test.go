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

func TestParseDiffCutoffWithFlag(t *testing.T) {
	diffSince = "2d"
	t.Cleanup(func() { diffSince = "" })

	before := time.Now()
	has, cutoff, err := parseDiffCutoff()
	after := time.Now()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Fatal("hasSince must be true when --since was given")
	}
	// The cutoff is two days back from whenever "now" was read, so it must
	// land inside the window the call itself spanned, shifted back 48h.
	earliest := before.Add(-48 * time.Hour)
	latest := after.Add(-48 * time.Hour)
	if cutoff.Before(earliest) || cutoff.After(latest) {
		t.Errorf("cutoff = %v, want within [%v, %v]", cutoff, earliest, latest)
	}
}

func TestParseDiffCutoffRejectsBadInput(t *testing.T) {
	diffSince = "nonsense"
	t.Cleanup(func() { diffSince = "" })

	has, _, err := parseDiffCutoff()

	if err == nil {
		t.Fatal("expected an error for an unparseable --since")
	}
	if has {
		t.Error("hasSince must be false when parsing failed")
	}
}

// loadManifest writes a manifest to a temp file and loads it, so tests can
// work with a real *manifest.Manifest without the package exporting a
// constructor that production code would never use.
func loadManifest(t *testing.T, body string) *manifest.Manifest {
	t.Helper()

	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return m
}

func TestDiffOwnerLookupMapsEveryRepo(t *testing.T) {
	m := loadManifest(t, `
base_dir: /base
acme:
  services:
    - repo: acme/api
    - repo: acme/web
solo:
  - repo: solo/tool
`)

	byRepo := diffOwnerLookup(m)

	if len(byRepo) != 3 {
		t.Fatalf("lookup has %d entries, want 3", len(byRepo))
	}
	for repo, owner := range map[string]string{
		"acme/api":  "acme",
		"acme/web":  "acme",
		"solo/tool": "solo",
	} {
		if got := byRepo[repo].Name; got != owner {
			t.Errorf("owner of %q = %q, want %q", repo, got, owner)
		}
	}
}

func TestDiffPluralRepos(t *testing.T) {
	for n, want := range map[int]string{0: "repos", 1: "repo", 2: "repos"} {
		if got := diffPluralRepos(n); got != want {
			t.Errorf("diffPluralRepos(%d) = %q, want %q", n, got, want)
		}
	}
}
