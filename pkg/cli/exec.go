package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/composition"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
)

var execCmd = &cobra.Command{
	Use:   "exec [service] [command...]",
	Short: "Execute a command in a running service",
	Long: `Execute a command in a running service container.
If no command is specified, uses the defaultExec from x-nix-compose.serviceInfo.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runExec,
}

func runExec(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try remote path (non-interactive only).
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteExec(ctx, rc, args)
	}

	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()
	return doExecCRI(ctx, criClient, args)
}

// remoteExec delegates exec to a remote orchestrate server.
// Only non-interactive ExecSync is supported via gRPC; interactive exec
// requires SPDY streaming which is not in the proto.
func remoteExec(ctx context.Context, rc *client.Client, args []string) error {
	dir := projectDir()
	project := projectNameFor(dir, flagProjectName)

	service := args[0]
	cmdArgs := args[1:]

	if len(cmdArgs) == 0 {
		defaultExec, err := resolveDefaultExec(ctx, dir, service)
		if err != nil {
			return err
		}
		cmdArgs = defaultExec
	}

	// Check if the command looks interactive — remote mode only supports non-interactive.
	stdinFd := int(os.Stdin.Fd()) //nolint:gosec // fd fits in int on all supported platforms
	if cri.IsTerminal(stdinFd) && isShellCommand(cmdArgs) {
		return fmt.Errorf("interactive exec is not supported via remote mode (requires SPDY streaming)")
	}

	resp, err := rc.ExecSync(ctx, project, service, cmdArgs, 0)
	if err != nil {
		return fmt.Errorf("remote exec: %w", err)
	}

	_, _ = os.Stdout.Write(resp.Stdout)
	_, _ = os.Stderr.Write(resp.Stderr)
	if resp.ExitCode != 0 {
		os.Exit(int(resp.ExitCode))
	}
	return nil
}

// doExecCRI runs exec via the CRI backend.
func doExecCRI(ctx context.Context, client *cri.Client, args []string) error {
	dir := projectDir()
	project := projectNameFor(dir, flagProjectName)

	service := args[0]
	cmdArgs := args[1:]

	// Resolve command: if none given, look up defaultExec from the composition.
	if len(cmdArgs) == 0 {
		defaultExec, err := resolveDefaultExec(ctx, dir, service)
		if err != nil {
			return err
		}
		cmdArgs = defaultExec
	}

	// Look up the container ID for this service.
	containerID, err := lookupContainerID(ctx, client, project, service)
	if err != nil {
		return fmt.Errorf("finding container for service %q: %w", service, err)
	}

	// Decide interactive vs non-interactive.
	stdinFd := int(os.Stdin.Fd()) //nolint:gosec // fd fits in int on all supported platforms
	interactive := cri.IsTerminal(stdinFd) && isShellCommand(cmdArgs)

	if interactive {
		return execInteractive(ctx, client, containerID, cmdArgs, stdinFd)
	}
	return execNonInteractive(ctx, client, containerID, cmdArgs)
}

// execNonInteractive runs a command via ExecSync and prints the output.
func execNonInteractive(ctx context.Context, client *cri.Client, containerID string, cmd []string) error {
	resp, err := client.ExecSync(ctx, containerID, cmd, 0)
	if err != nil {
		return fmt.Errorf("exec sync: %w", err)
	}
	_, _ = os.Stdout.Write(resp.Stdout)
	_, _ = os.Stderr.Write(resp.Stderr)
	if resp.ExitCode != 0 {
		os.Exit(int(resp.ExitCode))
	}
	return nil
}

// execInteractive runs an interactive command with TTY via CRI Exec + SPDY.
func execInteractive(ctx context.Context, client *cri.Client, containerID string, cmd []string, stdinFd int) error {
	streamURL, err := client.Exec(ctx, containerID, cmd, true, true)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	restore, err := cri.SetRawTerminal(stdinFd)
	if err != nil {
		return fmt.Errorf("setting raw terminal: %w", err)
	}
	defer restore()

	if err := cri.ExecStream(ctx, streamURL, cri.StreamOptions{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Tty:    true,
	}); err != nil {
		return fmt.Errorf("exec stream: %w", err)
	}
	return nil
}

// resolveDefaultExec evaluates the Nix composition to find the defaultExec
// for the given service.
func resolveDefaultExec(ctx context.Context, dir, service string) ([]string, error) {
	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}

	comp, _, err := evaluator.Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("evaluation failed: %w", err)
	}

	defaultExec, err := composition.LookupDefaultExec(comp, service)
	if err != nil {
		return nil, fmt.Errorf("no command specified and cannot determine default: %w", err)
	}
	if len(defaultExec) == 0 {
		return nil, fmt.Errorf("no command specified and no defaultExec configured for service %q", service)
	}
	return defaultExec, nil
}

// isShellCommand returns true if the command looks like a shell.
func isShellCommand(cmd []string) bool {
	if len(cmd) == 0 {
		return true
	}
	base := filepath.Base(cmd[0])
	switch base {
	case "sh", "bash", "zsh", "fish", "ash", "dash", "csh", "tcsh", "ksh", "psql", "mysql", "python", "python3", "node", "irb", "rails":
		return len(cmd) == 1
	}
	return false
}
