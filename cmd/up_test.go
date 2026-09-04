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

