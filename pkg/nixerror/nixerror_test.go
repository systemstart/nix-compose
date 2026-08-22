package nixerror

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdataPath(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "testdata", name)
}

func TestParseStderr_FixtureError(t *testing.T) {
	data, err := os.ReadFile(testdataPath("nix-eval-error.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	e := ParseStderr(string(data), 1)

	if e.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", e.ExitCode)
	}

	// Should pick up the last location.
	if e.File != "/home/user/project/compose.nix" {
		t.Errorf("file = %q, want /home/user/project/compose.nix", e.File)
	}
	if e.Line != 12 {
		t.Errorf("line = %d, want 12", e.Line)
	}
	if e.Column != 15 {
		t.Errorf("column = %d, want 15", e.Column)
	}

	if e.Message != "attribute 'missingPackage' missing" {
		t.Errorf("message = %q, want %q", e.Message, "attribute 'missingPackage' missing")
	}

	// Error() should produce file:line:col: message.
	expected := "/home/user/project/compose.nix:12:15: attribute 'missingPackage' missing"
	if e.Error() != expected {
		t.Errorf("Error() = %q, want %q", e.Error(), expected)
	}
}

func TestParseStderr_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		exitCode int
		wantFile string
		wantLine int
		wantCol  int
		wantMsg  string
	}{
		{
			name:     "simple error with location",
			stderr:   "error: undefined variable 'foo'\n       at /tmp/test.nix:5:3:\n",
			exitCode: 1,
			wantFile: "/tmp/test.nix",
			wantLine: 5,
			wantCol:  3,
			wantMsg:  "undefined variable 'foo'",
		},
		{
			name:     "error without location",
			stderr:   "error: flake 'git+file:///tmp' does not provide attribute\n",
			exitCode: 1,
			wantFile: "",
			wantLine: 0,
			wantCol:  0,
			wantMsg:  "flake 'git+file:///tmp' does not provide attribute",
		},
		{
			name:     "no error prefix",
			stderr:   "some nix output without error prefix\n",
			exitCode: 2,
			wantFile: "",
			wantLine: 0,
			wantCol:  0,
			wantMsg:  "some nix output without error prefix",
		},
		{
			name:     "whitespace only stderr",
			stderr:   "  \n  \n  ",
			exitCode: 1,
			wantFile: "",
			wantLine: 0,
			wantCol:  0,
			wantMsg:  "nix evaluation failed",
		},
		{
			name:     "empty stderr",
			stderr:   "",
			exitCode: 1,
			wantFile: "",
			wantLine: 0,
			wantCol:  0,
			wantMsg:  "nix evaluation failed",
		},
		{
			name:     "multiple locations picks last",
			stderr:   "error: first error\n       at /a.nix:1:1:\n       error: second error\n       at /b.nix:10:20:\n",
			exitCode: 1,
			wantFile: "/b.nix",
			wantLine: 10,
			wantCol:  20,
			wantMsg:  "second error",
		},
	}

	// Verify Error() format without file.
	noFileErr := ParseStderr("error: some problem\n", 1)
	if noFileErr.Error() != "some problem" {
		t.Errorf("Error() without file = %q, want %q", noFileErr.Error(), "some problem")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ParseStderr(tt.stderr, tt.exitCode)

			if e.File != tt.wantFile {
				t.Errorf("file = %q, want %q", e.File, tt.wantFile)
			}
			if e.Line != tt.wantLine {
				t.Errorf("line = %d, want %d", e.Line, tt.wantLine)
			}
			if e.Column != tt.wantCol {
				t.Errorf("column = %d, want %d", e.Column, tt.wantCol)
			}
			if e.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", e.Message, tt.wantMsg)
			}
			if e.ExitCode != tt.exitCode {
				t.Errorf("exitCode = %d, want %d", e.ExitCode, tt.exitCode)
			}
			if e.Raw != tt.stderr {
				t.Errorf("raw not preserved")
			}
		})
	}
}

// TestParseStderr_MultiLineThrow covers nix-compose's own `throw`s, which run
// to several lines: the first line names the problem and the rest say what to
// write instead. Reporting only the first line drops the useful half.
func TestParseStderr_MultiLineThrow(t *testing.T) {
	stderr := "error:\n" +
		"       … while calling the 'throw' builtin\n" +
		"\n" +
		"       error: nix-compose: service 'web' sets `package: nope`, but there is\n" +
		"       no `nope` in the pinned nixpkgs.\n" +
		"\n" +
		"       Search for the right name at:\n" +
		"\n" +
		"           https://search.nixos.org/packages\n" +
		"       at /tmp/x/yaml.nix:12:5:\n"

	got := ParseStderr(stderr, 1)

	for _, want := range []string{
		"service 'web' sets `package: nope`",
		"no `nope` in the pinned nixpkgs.",
		"https://search.nixos.org/packages",
	} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message is missing %q:\n%s", want, got.Message)
		}
	}
	if strings.Contains(got.Message, "at /tmp/x/yaml.nix") {
		t.Errorf("message should not include the location trailer:\n%s", got.Message)
	}
	if got.File != "/tmp/x/yaml.nix" || got.Line != 12 {
		t.Errorf("location = %s:%d, want /tmp/x/yaml.nix:12", got.File, got.Line)
	}
	// Indentation nix added must be stripped, but the relative indent of the
	// URL line is part of the message and has to survive.
	if !strings.Contains(got.Message, "\n    https://search.nixos.org/packages") {
		t.Errorf("relative indentation was not preserved:\n%q", got.Message)
	}
}
