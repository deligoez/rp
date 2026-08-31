package cmd_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Characterization test for `rp up` human output.
//
// Every other up test drives the --json path, leaving runUpHuman — the most
// complex function in the codebase — uncovered. This pins its exact output so
// a refactor of the phase pipeline cannot silently change what a human sees.
//
// The output is byte-stable: phases render in manifest order (not completion
// order), and no absolute path appears in it.
//
// It also pins two deliberate `up` behaviours that differ from the standalone
// commands, both encoded in TestQA_UpInstallNewClone / TestQA_UpUpdateExisting:
//   - install runs only for repos bootstrap just cloned; update only for
//     repos that already existed.
//   - under --dry-run the pipeline is simulated in order, so a repo that
//     bootstrap reports as "would clone" is up to date by the time sync runs.
// ---------------------------------------------------------------------------

// upHumanFixture builds a workspace covering every branch of the human
// renderer: a categorized owner with an existing repo that has install and
// update commands, an existing repo with neither, and a missing repo; plus a
// flat owner with an existing install-only repo.
func upHumanFixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()

	initGitRepo(t, filepath.Join(base, "acme", "svc", "alpha"))
	initGitRepo(t, filepath.Join(base, "acme", "svc", "beta"))
	initGitRepo(t, filepath.Join(base, "solo", "flat1"))

	return writeManifest(t, t.TempDir(), fmt.Sprintf(`
base_dir: %s
acme:
  svc:
    - repo: acme/alpha
      install:
        - echo installing-alpha
      update:
        - echo updating-alpha
    - repo: acme/beta
    - repo: acme/missing
solo:
  - repo: solo/flat1
    install:
      - echo installing-flat1
`, base))
}

func TestUpHumanDryRunOutput(t *testing.T) {
	binary := binaryForTest(t)
	manifest := upHumanFixture(t)

	cmd := exec.Command(binary, "--no-color", "--manifest", manifest, "up", "--dry-run")
	out, _ := cmd.Output()

	want := strings.Join([]string{
		"== Bootstrap ==",
		"acme",
		"  svc/alpha                 already exists — would skip",
		"  svc/beta                  already exists — would skip",
		"  svc/missing               would clone git@github.com:acme/missing.git",
		"solo",
		"  flat1                     already exists — would skip",
		"",
		"== Sync ==",
		"acme",
		"  svc/alpha                      would pull",
		"  svc/beta                       would pull",
		"  svc/missing                    OK up to date",
		"",
		"solo",
		"  flat1                          would pull",
		"",
		"== Install ==",
		"== Update ==",
		"acme",
		"  svc/alpha                would run: echo updating-alpha",
		"",
		"-- Summary --",
		"1 cloned, 3 already existed, 0 failed",
		"0 pulled, 1 up to date, 0 skipped",
		"install: 0 repos, 0 commands would run",
		"update: 1 repo, 1 command would run",
		"",
	}, "\n")

	if got := string(out); got != want {
		t.Errorf("up --dry-run human output changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestUpHumanNoInstallNoUpdateOutput(t *testing.T) {
	binary := binaryForTest(t)
	manifest := upHumanFixture(t)

	cmd := exec.Command(binary, "--no-color", "--manifest", manifest,
		"up", "--dry-run", "--no-install", "--no-update")
	out, _ := cmd.Output()

	got := string(out)
	for _, header := range []string{"== Bootstrap ==", "== Sync =="} {
		if !strings.Contains(got, header) {
			t.Errorf("expected %q in output:\n%s", header, got)
		}
	}
	for _, header := range []string{"== Install ==", "== Update =="} {
		if strings.Contains(got, header) {
			t.Errorf("did not expect %q with the phase skipped:\n%s", header, got)
		}
	}
	if !strings.Contains(got, "-- Summary --") {
		t.Errorf("expected a summary:\n%s", got)
	}
}
