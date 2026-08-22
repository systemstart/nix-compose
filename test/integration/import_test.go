//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportProducesRunnableProject is the on-ramp end to end: a compose file
// goes in, and what comes out is a project nix-compose can actually evaluate.
//
// The conversion is unit-tested in pkg/composeimport; what this covers is the
// part those tests cannot — that the file written to disk is one the loader
// then accepts, with no step in between.
func TestImportProducesRunnableProject(t *testing.T) {
	dir := t.TempDir()
	proj := uniqueProjectName(t, "import")

	compose := `version: "3.8"
services:
  web:
    image: nginx:alpine
    command: ["sleep", "infinity"]
    ports:
      - 8080:80
    environment:
      - NGINX_HOST=example.com
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: hunter2
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, proj, dir, "import")
	t.Logf("import output:\n%s", out)
	if !strings.Contains(out, "2 services") {
		t.Errorf("expected both services to convert, got:\n%s", out)
	}

	generated := filepath.Join(dir, "nix-compose.yaml")
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("import did not write %s: %v", generated, err)
	}

	// The real assertion: the generated file evaluates. `plan` reads the
	// project without touching the runtime.
	out = mustRun(t, proj, dir, "plan")
	t.Logf("plan output:\n%s", out)
	for _, want := range []string{"nginx:alpine", "postgres:16"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the plan, got:\n%s", want, out)
		}
	}
}

// TestImportRefusesToClobber covers the one destructive thing this command
// could do. The generated file is the user's project, and a second import
// must not silently overwrite whatever they have edited into it.
func TestImportRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	proj := uniqueProjectName(t, "importclobber")

	compose := "services:\n  web:\n    image: nginx:alpine\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	mustRun(t, proj, dir, "import")

	generated := filepath.Join(dir, "nix-compose.yaml")
	edited := "services:\n  web:\n    package: hello   # hand-edited\n"
	if err := os.WriteFile(generated, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, proj, dir, "import")
	if err == nil {
		t.Fatalf("a second import should have refused to overwrite, got:\n%s", out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("the refusal should mention --force, got:\n%s", out)
	}

	current, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != edited {
		t.Errorf("the hand-edited file was modified:\n%s", current)
	}

	// With --force it goes through, and the edit is gone.
	mustRun(t, proj, dir, "import", "--force")
	current, err = os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(current), "hand-edited") {
		t.Error("--force should have overwritten the file")
	}
}
