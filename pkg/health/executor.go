package health

import "context"

// ExecResult holds output from a container exec.
type ExecResult struct {
	ExitCode int32
}

// ContainerExecutor abstracts ExecSync for testability.
type ContainerExecutor interface {
	ExecSync(ctx context.Context, containerID string, cmd []string,
		timeoutSecs int64) (*ExecResult, error)
}
