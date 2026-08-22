package envfrom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func testdataPath(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", name)
}

func TestParseEnvFile_Basic(t *testing.T) {
	data := []byte("FOO=bar\nBAZ=qux\n")
	env, err := parseEnvFile(data)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if env["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", env["FOO"])
	}
	if env["BAZ"] != "qux" {
		t.Errorf("BAZ = %q, want qux", env["BAZ"])
	}
}

func TestParseEnvFile_CommentsAndEmpty(t *testing.T) {
	data := []byte("# comment\n\nFOO=bar\n  # another comment\n")
	env, err := parseEnvFile(data)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if len(env) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(env))
	}
	if env["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", env["FOO"])
	}
}

func TestParseEnvFile_Quotes(t *testing.T) {
	data := []byte("FOO=\"quoted value\"\nBAR='single quoted'\n")
	env, err := parseEnvFile(data)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if env["FOO"] != "quoted value" {
		t.Errorf("FOO = %q, want 'quoted value'", env["FOO"])
	}
	if env["BAR"] != "single quoted" {
		t.Errorf("BAR = %q, want 'single quoted'", env["BAR"])
	}
}

func TestParseEnvFile_NoEquals(t *testing.T) {
	data := []byte("NOEQUALSSIGN\nFOO=bar\n")
	env, err := parseEnvFile(data)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if len(env) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(env))
	}
}

func TestApplyPrefix_Empty(t *testing.T) {
	env := map[string]string{"FOO": "bar"}
	result := applyPrefix(env, "")
	if result["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", result["FOO"])
	}
}

func TestApplyPrefix_WithPrefix(t *testing.T) {
	env := map[string]string{"FOO": "bar", "BAZ": "qux"}
	result := applyPrefix(env, "APP_")
	if result["APP_FOO"] != "bar" {
		t.Errorf("APP_FOO = %q, want bar", result["APP_FOO"])
	}
	if result["APP_BAZ"] != "qux" {
		t.Errorf("APP_BAZ = %q, want qux", result["APP_BAZ"])
	}
	if _, ok := result["FOO"]; ok {
		t.Error("original key FOO should not be present")
	}
}

func TestResolve_SecretFile(t *testing.T) {
	projectDir := filepath.Join(testdataPath(""), "..")
	resolver := &Resolver{ProjectDir: projectDir}

	sources := []eval.EnvFromSource{
		{SecretFile: "testdata/secrets/api.env"},
	}

	env, err := resolver.Resolve(context.Background(), sources)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if env["DB_PASSWORD"] != "s3cret" {
		t.Errorf("DB_PASSWORD = %q, want s3cret", env["DB_PASSWORD"])
	}
	if env["API_KEY"] != "abc123" {
		t.Errorf("API_KEY = %q, want abc123", env["API_KEY"])
	}
	if env["CACHE_URL"] != "redis://localhost:6379" {
		t.Errorf("CACHE_URL = %q, want redis://localhost:6379", env["CACHE_URL"])
	}
}

func TestResolve_SecretFile_NotFound(t *testing.T) {
	resolver := &Resolver{ProjectDir: t.TempDir()}

	sources := []eval.EnvFromSource{
		{SecretFile: "nonexistent.env"},
	}

	_, err := resolver.Resolve(context.Background(), sources)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestResolve_WithPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.env"), []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := &Resolver{ProjectDir: dir}
	sources := []eval.EnvFromSource{
		{SecretFile: "test.env", Prefix: "PFX_"},
	}

	env, err := resolver.Resolve(context.Background(), sources)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env["PFX_FOO"] != "bar" {
		t.Errorf("PFX_FOO = %q, want bar", env["PFX_FOO"])
	}
}

type mockSopsRunner struct {
	stdout []byte
	stderr []byte
	err    error
}

func (m *mockSopsRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	return m.stdout, m.stderr, m.err
}

func TestResolve_SopsFile(t *testing.T) {
	dir := t.TempDir()
	// Create a placeholder file so the path exists.
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc.env"), []byte("encrypted"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockSopsRunner{
		stdout: []byte("SECRET_KEY=decrypted_value\n"),
	}
	resolver := &Resolver{ProjectDir: dir, Runner: runner}

	sources := []eval.EnvFromSource{
		{SopsFile: "secrets.enc.env"},
	}

	env, err := resolver.Resolve(context.Background(), sources)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env["SECRET_KEY"] != "decrypted_value" {
		t.Errorf("SECRET_KEY = %q, want decrypted_value", env["SECRET_KEY"])
	}
}

func TestResolve_SopsFile_Error(t *testing.T) {
	dir := t.TempDir()
	runner := &mockSopsRunner{
		stderr: []byte("decrypt failed"),
		err:    fmt.Errorf("exit 1"),
	}
	resolver := &Resolver{ProjectDir: dir, Runner: runner}

	sources := []eval.EnvFromSource{
		{SopsFile: "secrets.enc.env"},
	}

	_, err := resolver.Resolve(context.Background(), sources)
	if err == nil {
		t.Error("expected error for sops failure")
	}
}

func TestResolve_SopsFile_NilRunner(t *testing.T) {
	resolver := &Resolver{ProjectDir: t.TempDir()}

	sources := []eval.EnvFromSource{
		{SopsFile: "secrets.enc.env"},
	}

	_, err := resolver.Resolve(context.Background(), sources)
	if err == nil {
		t.Error("expected error when runner is nil")
	}
}

func TestResolveEnvFrom_MergePrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.env"), []byte("FOO=from_file\nBAR=from_file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image:       "node:18",
				Environment: map[string]string{"FOO": "explicit"},
				XNixCompose: &eval.NixComposeExtended{
					EnvFrom: []eval.EnvFromSource{
						{SecretFile: "test.env"},
					},
				},
			},
		},
	}

	resolver := &Resolver{ProjectDir: dir}
	err := ResolveEnvFrom(context.Background(), comp, resolver)
	if err != nil {
		t.Fatalf("ResolveEnvFrom: %v", err)
	}

	api := comp.Services["api"]
	// Explicit wins.
	if api.Environment["FOO"] != "explicit" {
		t.Errorf("FOO = %q, want explicit (explicit takes precedence)", api.Environment["FOO"])
	}
	// envFrom fills in missing keys.
	if api.Environment["BAR"] != "from_file" {
		t.Errorf("BAR = %q, want from_file", api.Environment["BAR"])
	}
}

func TestResolveEnvFrom_NilResolver(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {Image: "node:18"},
		},
	}
	err := ResolveEnvFrom(context.Background(), comp, nil)
	if err != nil {
		t.Errorf("expected nil error for nil resolver, got %v", err)
	}
}

func TestResolveEnvFrom_NoEnvFrom(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"api": {
				Image:       "node:18",
				XNixCompose: &eval.NixComposeExtended{},
			},
		},
	}
	resolver := &Resolver{ProjectDir: t.TempDir()}
	err := ResolveEnvFrom(context.Background(), comp, resolver)
	if err != nil {
		t.Errorf("expected nil error for no envFrom, got %v", err)
	}
}

func TestResolve_EmptySource(t *testing.T) {
	resolver := &Resolver{ProjectDir: t.TempDir()}
	sources := []eval.EnvFromSource{{}}

	env, err := resolver.Resolve(context.Background(), sources)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("expected empty env, got %v", env)
	}
}
