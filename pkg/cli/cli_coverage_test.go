package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// TestWarnUnreadableLog pins the message a user sees when the runtime's log
// file exists but is unreadable. It must not suggest `crictl logs`: crictl
// opens the same file as the same user and fails identically, so recommending
// it sends people in a circle.
func TestWarnUnreadableLog(t *testing.T) {
	out := captureStderr(t, func() {
		warnUnreadableLog(`cannot read logs for "web": permission denied`)
	})

	for _, want := range []string{"Warning:", `"web"`, "ps -a", "doctor", "root"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning should mention %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "crictl") {
		t.Errorf("must not suggest crictl — it fails the same way; got:\n%s", out)
	}
}

func TestResolveComposeSource(t *testing.T) {
	dir := t.TempDir()

	t.Run("explicit argument that exists", func(t *testing.T) {
		path := filepath.Join(dir, "given.yaml")
		if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveComposeSource(dir, []string{path})
		if err != nil {
			t.Fatalf("resolveComposeSource: %v", err)
		}
		if got != path {
			t.Errorf("got %q, want %q", got, path)
		}
	})

	t.Run("explicit argument that does not exist", func(t *testing.T) {
		_, err := resolveComposeSource(dir, []string{filepath.Join(dir, "absent.yaml")})
		if err == nil {
			t.Fatal("a named file that does not exist should be an error")
		}
		if !strings.Contains(err.Error(), "absent.yaml") {
			t.Errorf("the error should name the file, got: %v", err)
		}
	})

	t.Run("discovered in the project directory", func(t *testing.T) {
		found := t.TempDir()
		path := filepath.Join(found, composeFileNames[0])
		if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveComposeSource(found, nil)
		if err != nil {
			t.Fatalf("resolveComposeSource: %v", err)
		}
		if got != path {
			t.Errorf("got %q, want %q", got, path)
		}
	})

	t.Run("nothing to import", func(t *testing.T) {
		_, err := resolveComposeSource(t.TempDir(), nil)
		if err == nil {
			t.Fatal("an empty directory should be an error")
		}
		// The message has to say what it looked for, or the user cannot tell
		// whether their file is simply named something else.
		if !strings.Contains(err.Error(), composeFileNames[0]) {
			t.Errorf("the error should list the names tried, got: %v", err)
		}
	})
}

// TestIsDepRunning covers the guard `stop` uses before tearing a service down:
// a dependent that is still up must block the stop.
func TestIsDepRunning(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.bolt"))
	if err != nil {
		t.Fatalf("opening state: %v", err)
	}
	defer func() { _ = db.Close() }()

	tests := []struct {
		name   string
		status typing.RolloutStatusShort
		want   bool
	}{
		{"success counts as running", typing.RolloutStatusSuccess, true},
		{"running counts as running", typing.RolloutStatusRunning, true},
		{"failed does not", typing.RolloutStatusFailed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := "proj/" + tt.name
			r := &deploy.Rollout{
				InstanceId: id,
				Status:     &deploy.RolloutStatus{Short: tt.status},
			}
			if err := db.Save(state.RolloutsById, r); err != nil {
				t.Fatalf("saving rollout: %v", err)
			}
			if got := isDepRunning(db, id); got != tt.want {
				t.Errorf("isDepRunning(%v) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}

	// An id with no rollout behind it must not be treated as running, or a
	// stale reference would block every stop.
	if isDepRunning(db, "proj/never-deployed") {
		t.Error("an unknown dependency should not count as running")
	}
}
