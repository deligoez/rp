package cmd

import "testing"

func TestUpExitTallyCode(t *testing.T) {
	cases := []struct {
		name  string
		tally upExitTally
		want  int
	}{
		{"nothing wrong", upExitTally{}, 0},
		{"a skip needs a look", upExitTally{skipped: 1}, 1},
		{"a failure is hard", upExitTally{failed: 1}, 2},
		{"failure outranks skip", upExitTally{failed: 1, skipped: 5}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tally.code(); got != tc.want {
				t.Errorf("%+v.code() = %d, want %d", tc.tally, got, tc.want)
			}
		})
	}
}

func TestUpExitTallyAdd(t *testing.T) {
	var total upExitTally
	total.add(upExitTally{failed: 1})
	total.add(upExitTally{failed: 2, skipped: 3})

	if total.failed != 3 {
		t.Errorf("failed = %d, want 3", total.failed)
	}
	if total.skipped != 3 {
		t.Errorf("skipped = %d, want 3", total.skipped)
	}
}

func TestUpHumanExitCodeSumsEveryPhasesFailures(t *testing.T) {
	cases := []struct {
		name string
		boot upBootstrapOutcome
		sync upSyncCounts
		inst upCommandCounts
		upd  upCommandCounts
		want int
	}{
		{name: "all clear", want: 0},
		{name: "bootstrap failed", boot: upBootstrapOutcome{failed: 1}, want: 2},
		{name: "sync failed", sync: upSyncCounts{failed: 1}, want: 2},
		{name: "install failed", inst: upCommandCounts{failed: 1}, want: 2},
		{name: "update failed", upd: upCommandCounts{failed: 1}, want: 2},
		{name: "sync skipped", sync: upSyncCounts{skipped: 2}, want: 1},

		// A command phase skipping a repo is not an error: the repo simply was
		// not on disk. Only sync's skips raise the exit code.
		{name: "install skipped", inst: upCommandCounts{ran: true}, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := upHumanExitCode(tc.boot, tc.sync, tc.inst, tc.upd)
			if got != tc.want {
				t.Errorf("upHumanExitCode = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestUpHumanExitCodeMatchesTheJSONPath(t *testing.T) {
	// Both output modes must agree, which is the reason they share code().
	boot := upBootstrapOutcome{failed: 1}
	sync := upSyncCounts{skipped: 2}

	human := upHumanExitCode(boot, sync, upCommandCounts{}, upCommandCounts{})

	var tally upExitTally
	tally.add(upExitTally{failed: boot.failed})
	tally.add(upExitTally{failed: sync.failed, skipped: sync.skipped})

	if human != tally.code() {
		t.Errorf("human exit code %d != JSON exit code %d", human, tally.code())
	}
}
