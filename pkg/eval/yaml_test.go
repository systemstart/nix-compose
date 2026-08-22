package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/nixpins"
)

func writeYAML(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func TestDetectMode_YAML(t *testing.T) {
	for _, name := range YAMLFileNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeYAML(t, dir, name, "services:\n  web:\n    image: nginx\n")

			mode, err := DetectMode(dir)
			if err != nil {
				t.Fatalf("DetectMode: %v", err)
			}
			if mode != ModeYAML {
				t.Errorf("mode = %v, want ModeYAML", mode)
			}
		})
	}
}

// TestDetectMode_NixWinsOverYAML pins the precedence down. Dropping a
// nix-compose.yaml next to an existing flake.nix must not quietly change how
// that project is evaluated.
func TestDetectMode_NixWinsOverYAML(t *testing.T) {
	for _, nixFile := range []string{"flake.nix", "compose.nix"} {
		t.Run(nixFile, func(t *testing.T) {
			dir := t.TempDir()
			writeYAML(t, dir, nixFile, "{}")
			writeYAML(t, dir, "nix-compose.yaml", "services:\n  web:\n    image: nginx\n")

			mode, err := DetectMode(dir)
			if err != nil {
				t.Fatalf("DetectMode: %v", err)
			}
			if mode == ModeYAML {
				t.Errorf("%s should take precedence over nix-compose.yaml", nixFile)
			}
		})
	}
}

func TestDetectMode_NoneMentionsYAML(t *testing.T) {
	_, err := DetectMode(t.TempDir())
	if err == nil {
		t.Fatal("expected an error for an empty directory")
	}
	if !strings.Contains(err.Error(), "nix-compose.yaml") {
		t.Errorf("error should name the YAML file, got: %v", err)
	}
}

func TestLoadYAML_UsesNix(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{
			name: "registry images only",
			yaml: "services:\n  web:\n    image: nginx:1.27\n",
			want: false,
		},
		{
			name: "one package",
			yaml: "services:\n  web:\n    image: nginx\n  cli:\n    package: hello\n",
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeYAML(t, dir, "nix-compose.yaml", tc.yaml)

			_, usesNix, err := LoadYAML(path)
			if err != nil {
				t.Fatalf("LoadYAML: %v", err)
			}
			if usesNix != tc.want {
				t.Errorf("usesNix = %v, want %v", usesNix, tc.want)
			}
		})
	}
}

func TestValidateYAML_Rejections(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "misspelled service key",
			yaml: "services:\n  web:\n    image: nginx\n    enviroment:\n      A: b\n",
			want: []string{`service "web"`, `unknown key "enviroment"`},
		},
		{
			name: "unknown top-level key",
			yaml: "servcies:\n  web:\n    image: nginx\n",
			want: []string{`unknown top-level key "servcies"`},
		},
		{
			name: "image and package together",
			yaml: "services:\n  web:\n    image: nginx\n    package: nginx\n",
			want: []string{"pick one"},
		},
		{
			name: "no services",
			yaml: "networks:\n  default: {}\n",
			want: []string{"no `services:`"},
		},
		{
			// The unknown key is the useful half of this message: without it
			// the reader is told there are no services while looking at a
			// file that plainly has some.
			name: "misspelled services key reports both",
			yaml: "servcies:\n  web:\n    image: nginx\n",
			want: []string{`unknown top-level key "servcies"`, "no `services:`"},
		},
		{
			name: "service is not a mapping",
			yaml: "services:\n  web: nginx\n",
			want: []string{`service "web" must be a mapping`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeYAML(t, dir, "nix-compose.yaml", tc.yaml)

			_, _, err := LoadYAML(path)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error is missing %q:\n%v", want, err)
				}
			}
		})
	}
}

// TestValidateYAML_AcceptsComposeFields guards the reflection in serviceKeys:
// every field the backend understands must be writable in YAML, or the two
// halves of the tool disagree about what a service is.
func TestValidateYAML_AcceptsComposeFields(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, "nix-compose.yaml", `
services:
  web:
    image: nginx:1.27
    command: ["nginx", "-g", "daemon off;"]
    entrypoint: /bin/sh
    ports: ["8080:80"]
    environment:
      NGINX_HOST: localhost
    volumes: ["data:/var/lib/data"]
    depends_on: [db]
    restart: always
    working_dir: /srv
    user: "1000:1000"
    hostname: web
    network_mode: host
    extra_hosts: ["a:127.0.0.1"]
    profiles: [frontend]
    labels:
      role: web
    stop_signal: SIGTERM
    tmpfs: ["/tmp"]
    privileged: false
    healthcheck:
      test: ["CMD", "true"]
  db:
    image: postgres:16
volumes:
  data: {}
networks:
  default: {}
version: "3.8"
`)

	if _, _, err := LoadYAML(path); err != nil {
		t.Fatalf("compose fields should be accepted: %v", err)
	}
}

// TestEvalYAML_RegistryOnlySkipsNix is the property that makes YAML mode worth
// having as more than a syntax: a project that names no packages needs no Nix
// evaluation, so nix is never executed.
func TestEvalYAML_RegistryOnlySkipsNix(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "nix-compose.yaml", `
services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
  cache:
    image: redis:7
    depends_on: [web]
`)

	runner := &recordingRunner{}
	e := &Evaluator{Runner: runner, ProjectDir: dir}

	comp, _, err := e.Eval(context.Background())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("nix should not have been run, got calls: %v", runner.calls)
	}

	if len(comp.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(comp.Services))
	}
	if got := comp.Services["web"].Image; got != "nginx:1.27" {
		t.Errorf("web image = %q, want nginx:1.27", got)
	}
	if got := comp.Services["web"].Ports; len(got) != 1 || got[0] != "8080:80" {
		t.Errorf("web ports = %v, want [8080:80]", got)
	}
	if _, ok := comp.Services["cache"].DependsOn.Entries["web"]; !ok {
		t.Errorf("cache should depend on web, got %v", comp.Services["cache"].DependsOn.Entries)
	}
}

func TestEvalYAML_ValidationErrorIsNotWrappedAsNix(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "nix-compose.yaml", "services:\n  web:\n    imagge: nginx\n")

	e := &Evaluator{Runner: &recordingRunner{}, ProjectDir: dir}

	_, _, err := e.Eval(context.Background())
	if err == nil {
		t.Fatal("expected a validation error")
	}
	if strings.Contains(err.Error(), "nix eval failed") {
		t.Errorf("a document error should not be reported as a nix failure: %v", err)
	}
	if !strings.Contains(err.Error(), `unknown key "imagge"`) {
		t.Errorf("error should name the bad key, got: %v", err)
	}
}

func TestNixpkgsRefFor(t *testing.T) {
	if got := NixpkgsRefFor(map[string]any{}); got != nixpins.NixpkgsRef() {
		t.Errorf("default ref = %q, want the embedded pin %q", got, nixpins.NixpkgsRef())
	}

	doc := map[string]any{"nixpkgs": "github:NixOS/nixpkgs/nixos-24.11"}
	if got := NixpkgsRefFor(doc); got != "github:NixOS/nixpkgs/nixos-24.11" {
		t.Errorf("override ref = %q", got)
	}

	// An empty override is the same as none: a stray `nixpkgs:` with nothing
	// after it must not resolve packages against the empty flake reference.
	if got := NixpkgsRefFor(map[string]any{"nixpkgs": ""}); got != nixpins.NixpkgsRef() {
		t.Errorf("empty override ref = %q, want the embedded pin", got)
	}
}

func TestYAMLExpr(t *testing.T) {
	expr := yamlExpr("/tmp/eval", "/tmp/eval/data.json", "github:NixOS/nixpkgs/abc", "x86_64-linux")

	for _, want := range []string{
		`builtins.getFlake "github:NixOS/nixpkgs/abc"`,
		`builtins.getFlake "` + nixpins.NixOCIRef() + `"`,
		"nixpkgs.legacyPackages.x86_64-linux",
		"import /tmp/eval/lib.nix",
		"import /tmp/eval/yaml.nix",
		"builtins.readFile /tmp/eval/data.json",
		"config.out.dockerComposeYamlAttrs",
	} {
		if !strings.Contains(expr, want) {
			t.Errorf("expression is missing %q:\n%s", want, expr)
		}
	}
}

func TestNixSystem(t *testing.T) {
	got := NixSystem()
	if !strings.Contains(got, "-") {
		t.Fatalf("NixSystem() = %q, want an arch-os double", got)
	}
	for _, bad := range []string{"amd64", "arm64"} {
		if strings.HasPrefix(got, bad) {
			t.Errorf("NixSystem() = %q, want a Nix arch name, not the Go one", got)
		}
	}
}
