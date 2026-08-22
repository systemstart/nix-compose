package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProjectNameFor covers the precedence every command shares. Getting this
// wrong in one place means `up` creates containers `down` cannot find.
func TestProjectNameFor(t *testing.T) {
	withDoc := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "nix-compose.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	named := withDoc(t, "name: declared\nservices:\n  web:\n    image: nginx\n")
	anon := withDoc(t, "services:\n  web:\n    image: nginx\n")
	bare := t.TempDir()

	tests := []struct {
		name     string
		dir      string
		override string
		want     string
	}{
		{"flag wins over document", named, "from-flag", "from-flag"},
		{"flag wins over basename", anon, "from-flag", "from-flag"},
		{"document wins over basename", named, "", "declared"},
		{"basename when no name declared", anon, "", filepath.Base(anon)},
		{"basename when no document", bare, "", filepath.Base(bare)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectNameFor(tt.dir, tt.override); got != tt.want {
				t.Errorf("projectNameFor(%q, %q) = %q, want %q", tt.dir, tt.override, got, tt.want)
			}
		})
	}
}

// TestCriProjectName_MatchesResolveProject pins up's path to the same answer
// the other commands get; they used to be separate copies of the same logic.
func TestCriProjectName_MatchesResolveProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nix-compose.yaml"),
		[]byte("name: shared\nservices:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := criProjectName(UpDeps{ProjectDir: dir}); got != "shared" {
		t.Errorf("criProjectName = %q, want %q", got, "shared")
	}
	if got := criProjectName(UpDeps{ProjectDir: dir, ProjectName: "override"}); got != "override" {
		t.Errorf("criProjectName with override = %q, want %q", got, "override")
	}
}
