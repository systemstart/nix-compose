package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectNameFor(t *testing.T) {
	if got := ProjectNameFor(map[string]any{"name": "paperless-itest"}); got != "paperless-itest" {
		t.Errorf("ProjectNameFor = %q, want %q", got, "paperless-itest")
	}
	if got := ProjectNameFor(map[string]any{}); got != "" {
		t.Errorf("a document with no name should yield \"\", got %q", got)
	}
	// Not a string: validation rejects this, but the accessor must not panic.
	if got := ProjectNameFor(map[string]any{"name": 42}); got != "" {
		t.Errorf("a non-string name should yield \"\", got %q", got)
	}
}

func TestProjectNameFromDir(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "nix-compose.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	dir := write(t, "name: paperless-itest\nservices:\n  web:\n    image: nginx\n")
	if got := ProjectNameFromDir(dir); got != "paperless-itest" {
		t.Errorf("ProjectNameFromDir = %q, want %q", got, "paperless-itest")
	}

	dir = write(t, "services:\n  web:\n    image: nginx\n")
	if got := ProjectNameFromDir(dir); got != "" {
		t.Errorf("no name declared should yield \"\", got %q", got)
	}

	// No document at all, and an unparseable one: both fall back to "" so the
	// caller's own error reporting stays in charge.
	if got := ProjectNameFromDir(t.TempDir()); got != "" {
		t.Errorf("a directory with no project file should yield \"\", got %q", got)
	}
	dir = write(t, "this: is: not: valid: yaml\n")
	if got := ProjectNameFromDir(dir); got != "" {
		t.Errorf("an unparseable document should yield \"\", got %q", got)
	}
}

func TestValidateYAML_Name(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"valid", "name: paperless-itest\nservices:\n  web:\n    image: nginx\n", ""},
		{"valid with underscore", "name: pl_itest2\nservices:\n  web:\n    image: nginx\n", ""},
		{"uppercase", "name: Paperless\nservices:\n  web:\n    image: nginx\n", "not a valid project name"},
		{"leading dash", "name: -nope\nservices:\n  web:\n    image: nginx\n", "not a valid project name"},
		{"slash", "name: a/b\nservices:\n  web:\n    image: nginx\n", "not a valid project name"},
		{"empty", "name: \"\"\nservices:\n  web:\n    image: nginx\n", "is empty"},
		{"not a string", "name: 42\nservices:\n  web:\n    image: nginx\n", "must be a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "nix-compose.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := LoadYAML(path)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadYAML: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestEvalYAML_StripsName guards the round trip: `name` addresses the project,
// it is not part of the composition, so it must not survive into the JSON the
// backends consume.
func TestEvalYAML_StripsName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nix-compose.yaml")
	if err := os.WriteFile(path, []byte("name: itest\nservices:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Evaluator{ProjectDir: dir}
	raw, _, err := e.evalYAML(t.Context())
	if err != nil {
		t.Fatalf("evalYAML: %v", err)
	}
	if strings.Contains(string(raw), `"name"`) {
		t.Errorf("name leaked into the composition JSON: %s", raw)
	}
}
