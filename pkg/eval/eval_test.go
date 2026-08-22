package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// mockRunner records and replays command executions.
type mockRunner struct {
	calls  [][]string
	stdout []byte
	stderr []byte
	err    error
}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	return m.stdout, m.stderr, m.err
}

func TestDetectMode_Flake(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	mode, err := DetectMode(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeFlake {
		t.Errorf("mode = %d, want ModeFlake (%d)", mode, ModeFlake)
	}
}

func TestDetectMode_Legacy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	mode, err := DetectMode(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeLegacy {
		t.Errorf("mode = %d, want ModeLegacy (%d)", mode, ModeLegacy)
	}
}

func TestDetectMode_FlakePreferred(t *testing.T) {
	dir := t.TempDir()
	// Both files exist; flake takes priority.
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	mode, err := DetectMode(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeFlake {
		t.Errorf("mode = %d, want ModeFlake (%d)", mode, ModeFlake)
	}
}

func TestDetectMode_NoFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := DetectMode(dir)
	if err == nil {
		t.Error("expected error for empty directory")
	}
}

func TestFlakeEvalArgs_Default(t *testing.T) {
	e := &Evaluator{
		Impure: true,
	}

	args := e.FlakeEvalArgs()
	expected := []string{"eval", "--json", ".#composition.config.out.dockerComposeYamlAttrs", "--impure"}
	if !reflect.DeepEqual(args, expected) {
		t.Errorf("args = %v, want %v", args, expected)
	}
}

func TestFlakeEvalArgs_CustomAttr(t *testing.T) {
	e := &Evaluator{
		FlakeAttr: "myApp",
		Impure:    false,
	}

	args := e.FlakeEvalArgs()
	expected := []string{"eval", "--json", ".#myApp.config.out.dockerComposeYamlAttrs"}
	if !reflect.DeepEqual(args, expected) {
		t.Errorf("args = %v, want %v", args, expected)
	}
}

func TestEval_FlakeMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixture, err := os.ReadFile(testdataPath("minimal.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	runner := &mockRunner{stdout: fixture}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		Impure:     true,
	}

	comp, raw, err := e.Eval(context.Background())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	if len(comp.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(comp.Services))
	}
	if len(raw) == 0 {
		t.Error("expected non-empty raw output")
	}

	// Verify the command was constructed correctly.
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call[0] != "nix" {
		t.Errorf("command = %q, want nix", call[0])
	}
}

func TestEval_NixError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{
		stderr: []byte("error: attribute 'missing' not found\n       at /test.nix:1:1:\n"),
		err:    fmt.Errorf("exit status 1"),
	}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		Impure:     true,
	}

	_, _, err := e.Eval(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEval_LegacyMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixture, err := os.ReadFile(testdataPath("minimal.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	runner := &mockRunner{stdout: fixture}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		Impure:     true,
	}

	comp, _, err := e.Eval(context.Background())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	if len(comp.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(comp.Services))
	}

	// Verify legacy eval was called with --expr.
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	foundExpr := false
	for _, arg := range call {
		if arg == "--expr" {
			foundExpr = true
		}
	}
	if !foundExpr {
		t.Errorf("expected --expr in legacy args: %v", call)
	}
}

func TestEval_LegacyMode_Error(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{
		stderr: []byte("error: something broke\n"),
		err:    fmt.Errorf("exit status 1"),
	}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		Impure:     true,
	}

	_, _, err := e.Eval(context.Background())
	if err == nil {
		t.Fatal("expected error from legacy eval")
	}
}

func TestEval_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{stdout: []byte(`{invalid`)}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		Impure:     true,
	}

	_, _, err := e.Eval(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON output")
	}
}

func TestEval_NoProjectFiles(t *testing.T) {
	dir := t.TempDir()
	runner := &mockRunner{}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
	}

	_, _, err := e.Eval(context.Background())
	if err == nil {
		t.Fatal("expected error for missing project files")
	}
}

func TestExitCode_NonExitError(t *testing.T) {
	code := exitCode(fmt.Errorf("not an exit error"))
	if code != 1 {
		t.Errorf("exitCode = %d, want 1", code)
	}
}

func TestExitCode_RealExitError(t *testing.T) {
	// Create a real exec.ExitError by running a command that fails.
	cmd := exec.Command("sh", "-c", "exit 42")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error")
	}
	code := exitCode(err)
	if code != 42 {
		t.Errorf("exitCode = %d, want 42", code)
	}
}

func TestEval_FlakeMode_NoImpure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixture, err := os.ReadFile(testdataPath("minimal.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	runner := &mockRunner{stdout: fixture}
	e := &Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		Impure:     false,
	}

	_, _, err = e.Eval(context.Background())
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	// Verify --impure is NOT in args.
	call := runner.calls[0]
	for _, arg := range call {
		if arg == "--impure" {
			t.Error("unexpected --impure flag when impure=false")
		}
	}
}
