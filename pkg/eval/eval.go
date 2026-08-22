package eval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/systemstart/nix-compose/pkg/nixerror"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	// Run executes a command and returns its stdout, stderr, and any error.
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner is the real CommandRunner that uses os/exec.
type ExecRunner struct {
	Dir string
}

func (r *ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = r.Dir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// Mode indicates how the project should be evaluated.
type Mode int

const (
	ModeFlake  Mode = iota // Project has a flake.nix.
	ModeLegacy             // Project has compose.nix without flake.
	ModeYAML               // Project has nix-compose.yaml and no Nix file.
)

// DetectMode determines how a project should be evaluated.
//
// The Nix front-ends are checked first, so adding a nix-compose.yaml to a
// project that already has a flake.nix cannot silently change how it is
// evaluated.
func DetectMode(projectDir string) (Mode, error) {
	if _, err := os.Stat(filepath.Join(projectDir, "flake.nix")); err == nil {
		return ModeFlake, nil
	}
	if _, err := os.Stat(filepath.Join(projectDir, "compose.nix")); err == nil {
		return ModeLegacy, nil
	}
	if FindYAMLFile(projectDir) != "" {
		return ModeYAML, nil
	}
	return 0, fmt.Errorf("no flake.nix, compose.nix or %s found in %s",
		YAMLFileNames[0], projectDir)
}

// Evaluator evaluates a Nix project and returns a Composition.
type Evaluator struct {
	Runner     CommandRunner
	ProjectDir string
	FlakeAttr  string
	Impure     bool
}

// Eval runs nix eval and parses the result into a Composition.
// It also returns the raw JSON output for downstream use (e.g. store path scanning).
func (e *Evaluator) Eval(ctx context.Context) (*Composition, []byte, error) {
	mode, err := DetectMode(e.ProjectDir)
	if err != nil {
		return nil, nil, fmt.Errorf("detecting mode: %w", err)
	}

	var stdout, stderr []byte
	switch mode {
	case ModeFlake:
		stdout, stderr, err = e.evalFlake(ctx)
	case ModeLegacy:
		stdout, stderr, err = e.evalLegacy(ctx)
	case ModeYAML:
		stdout, stderr, err = e.evalYAML(ctx)
	}

	if err != nil {
		// YAML mode phrases its own errors — see userError.
		var ready userError
		if errors.As(err, &ready) {
			return nil, nil, ready.error
		}
		nixErr := nixerror.ParseStderr(string(stderr), exitCode(err))
		return nil, nil, fmt.Errorf("nix eval failed: %w", nixErr)
	}

	comp, err := ParseComposition(stdout)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing nix eval output: %w", err)
	}

	return comp, stdout, nil
}

func (e *Evaluator) evalFlake(ctx context.Context) ([]byte, []byte, error) {
	attr := e.FlakeAttr
	if attr == "" {
		attr = "composition"
	}
	installable := fmt.Sprintf(".#%s.config.out.dockerComposeYamlAttrs", attr)

	args := []string{"eval", "--json", installable}
	if e.Impure {
		args = append(args, "--impure")
	}

	stdout, stderr, err := e.Runner.Run(ctx, "nix", args...)
	if err != nil {
		return stdout, stderr, fmt.Errorf("nix eval flake: %w", err)
	}
	return stdout, stderr, nil
}

func (e *Evaluator) evalLegacy(ctx context.Context) ([]byte, []byte, error) {
	pkgsFile := filepath.Join(e.ProjectDir, "pkgs.nix")
	composeFile := filepath.Join(e.ProjectDir, "compose.nix")

	expr := fmt.Sprintf(
		`let pkgs = if builtins.pathExists %s then import %s else import <nixpkgs> {};`+
			` eval = import %s { inherit pkgs; };`+
			` in eval.config.out.dockerComposeYamlAttrs`,
		pkgsFile, pkgsFile, composeFile,
	)

	args := []string{"eval", "--json", "--impure", "--expr", expr}
	stdout, stderr, err := e.Runner.Run(ctx, "nix", args...)
	if err != nil {
		return stdout, stderr, fmt.Errorf("nix eval legacy: %w", err)
	}
	return stdout, stderr, nil
}

// FlakeEvalArgs returns the nix command arguments for flake evaluation.
// Exported for testing command construction.
func (e *Evaluator) FlakeEvalArgs() []string {
	attr := e.FlakeAttr
	if attr == "" {
		attr = "composition"
	}
	installable := fmt.Sprintf(".#%s.config.out.dockerComposeYamlAttrs", attr)

	args := []string{"eval", "--json", installable}
	if e.Impure {
		args = append(args, "--impure")
	}
	return args
}

func exitCode(err error) int {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
