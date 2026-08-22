package cri

// RestartPolicy represents the restart behavior for a container.
type RestartPolicy int

const (
	RestartNo RestartPolicy = iota
	RestartAlways
	RestartOnFailure
	RestartUnlessStopped
)

// ParseRestartPolicy converts a compose-style restart string to a RestartPolicy.
func ParseRestartPolicy(s string) RestartPolicy {
	switch s {
	case "always":
		return RestartAlways
	case "on-failure":
		return RestartOnFailure
	case "unless-stopped":
		return RestartUnlessStopped
	default:
		return RestartNo
	}
}

// ShouldRestart returns whether the container should be restarted given its
// exit code and whether the user explicitly stopped it.
func (p RestartPolicy) ShouldRestart(exitCode int, stoppedByUser bool) bool {
	switch p {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return exitCode != 0
	case RestartUnlessStopped:
		return !stoppedByUser
	default:
		return false
	}
}
