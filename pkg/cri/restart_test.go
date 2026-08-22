package cri

import "testing"

func TestParseRestartPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  RestartPolicy
	}{
		{"no", RestartNo},
		{"always", RestartAlways},
		{"on-failure", RestartOnFailure},
		{"unless-stopped", RestartUnlessStopped},
		{"", RestartNo},
		{"bogus", RestartNo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseRestartPolicy(tt.input)
			if got != tt.want {
				t.Errorf("ParseRestartPolicy(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestShouldRestart(t *testing.T) {
	tests := []struct {
		name          string
		policy        RestartPolicy
		exitCode      int
		stoppedByUser bool
		want          bool
	}{
		// RestartNo never restarts.
		{"no/exit0", RestartNo, 0, false, false},
		{"no/exit1", RestartNo, 1, false, false},
		{"no/stopped", RestartNo, 0, true, false},

		// RestartAlways always restarts.
		{"always/exit0", RestartAlways, 0, false, true},
		{"always/exit1", RestartAlways, 1, false, true},
		{"always/stopped", RestartAlways, 0, true, true},

		// RestartOnFailure restarts only on non-zero exit.
		{"on-failure/exit0", RestartOnFailure, 0, false, false},
		{"on-failure/exit1", RestartOnFailure, 1, false, true},
		{"on-failure/exit137", RestartOnFailure, 137, false, true},
		{"on-failure/stopped", RestartOnFailure, 1, true, true},

		// RestartUnlessStopped restarts unless the user stopped it.
		{"unless-stopped/exit0", RestartUnlessStopped, 0, false, true},
		{"unless-stopped/exit1", RestartUnlessStopped, 1, false, true},
		{"unless-stopped/stopped", RestartUnlessStopped, 0, true, false},
		{"unless-stopped/stopped-fail", RestartUnlessStopped, 1, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.ShouldRestart(tt.exitCode, tt.stoppedByUser)
			if got != tt.want {
				t.Errorf("ShouldRestart(%d, %v) = %v, want %v", tt.exitCode, tt.stoppedByUser, got, tt.want)
			}
		})
	}
}
