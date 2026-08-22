package composeimport

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func convert(t *testing.T, in string) *Result {
	t.Helper()
	res, err := Convert([]byte(in))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return res
}

func service(t *testing.T, res *Result, name string) map[string]any {
	t.Helper()
	services, ok := res.Doc["services"].(map[string]any)
	if !ok {
		t.Fatal("document has no services mapping")
	}
	svc, ok := services[name].(map[string]any)
	if !ok {
		t.Fatalf("no service %q in the converted document", name)
	}
	return svc
}

func noteFor(res *Result, service, key string) (Note, bool) {
	for _, n := range res.Notes {
		if n.Service == service && n.Key == key {
			return n, true
		}
	}
	return Note{}, false
}

func TestConvert_PassesSupportedFieldsThrough(t *testing.T) {
	res := convert(t, `
services:
  db:
    image: postgres:16
    restart: always
    working_dir: /srv
    labels:
      role: db
`)

	svc := service(t, res, "db")
	if svc["image"] != "postgres:16" {
		t.Errorf("image = %v", svc["image"])
	}
	if svc["restart"] != "always" {
		t.Errorf("restart = %v", svc["restart"])
	}
	if svc["working_dir"] != "/srv" {
		t.Errorf("working_dir = %v", svc["working_dir"])
	}
	if len(res.Notes) != 0 {
		t.Errorf("nothing should have been dropped, got: %v", res.Notes)
	}
}

// TestConvert_BuildIsReportedNotDropped covers the field the whole project is
// about. Losing it silently would leave a service that cannot start with no
// explanation of why.
func TestConvert_BuildIsReportedNotDropped(t *testing.T) {
	res := convert(t, `
services:
  web:
    build: .
    ports: ["8080:80"]
`)

	note, ok := noteFor(res, "web", "build")
	if !ok {
		t.Fatalf("no note about build, got: %v", res.Notes)
	}
	if !strings.Contains(note.Detail, "package:") {
		t.Errorf("the build note should name `package:`, got: %s", note.Detail)
	}

	if _, ok := noteFor(res, "web", ""); !ok {
		t.Error("a service left with nothing to run should be reported")
	}
	if len(res.NeedsImage) != 1 || res.NeedsImage[0] != "web" {
		t.Errorf("NeedsImage = %v, want [web]", res.NeedsImage)
	}
}

func TestConvert_EnvironmentListBecomesMapping(t *testing.T) {
	res := convert(t, `
services:
  web:
    image: nginx
    environment:
      - FOO=bar
      - EMPTY=
      - FROM_HOST
`)

	env, ok := service(t, res, "web")["environment"].(map[string]any)
	if !ok {
		t.Fatalf("environment did not become a mapping: %#v", service(t, res, "web")["environment"])
	}
	if env["FOO"] != "bar" {
		t.Errorf("FOO = %v, want bar", env["FOO"])
	}
	if env["EMPTY"] != "" {
		t.Errorf("EMPTY = %v, want an empty string", env["EMPTY"])
	}
	if _, present := env["FROM_HOST"]; present {
		t.Error("a host-inherited variable cannot be represented and must be dropped")
	}
	if note, ok := noteFor(res, "web", "environment"); !ok {
		t.Error("dropping FROM_HOST should be reported")
	} else if !strings.Contains(note.Detail, "FROM_HOST") {
		t.Errorf("the note should name the variable, got: %s", note.Detail)
	}
}

func TestConvert_PortSyntaxes(t *testing.T) {
	res := convert(t, `
services:
  web:
    image: nginx
    ports:
      - 8080:80
      - "8443:443"
      - target: 9090
        published: 9091
        protocol: udp
      - target: 7000
`)

	got, ok := service(t, res, "web")["ports"].([]any)
	if !ok {
		t.Fatalf("ports is not a list: %#v", service(t, res, "web")["ports"])
	}
	want := []string{"8080:80", "8443:443", "9091:9090/udp", "7000"}
	if len(got) != len(want) {
		t.Fatalf("got %d ports %v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("port %d = %v, want %s", i, got[i], w)
		}
	}
}

func TestConvert_VolumeSyntaxes(t *testing.T) {
	res := convert(t, `
services:
  web:
    image: nginx
    volumes:
      - ./html:/usr/share/nginx/html:ro
      - type: volume
        source: assets
        target: /srv/assets
      - type: bind
        source: /etc/conf
        target: /etc/conf
        read_only: true
      - type: tmpfs
        target: /scratch
`)

	got, _ := service(t, res, "web")["volumes"].([]any)
	want := []string{
		"./html:/usr/share/nginx/html:ro",
		"assets:/srv/assets",
		"/etc/conf:/etc/conf:ro",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("volume %d = %v, want %s", i, got[i], w)
		}
	}

	// tmpfs has its own key rather than a short mount form.
	if note, ok := noteFor(res, "web", "volumes"); !ok {
		t.Error("the tmpfs mount should be reported")
	} else if !strings.Contains(note.Detail, "tmpfs") {
		t.Errorf("the note should mention tmpfs, got: %s", note.Detail)
	}
}

func TestConvert_TopLevelKeys(t *testing.T) {
	res := convert(t, `
version: "3.8"
services:
  web:
    image: nginx
volumes:
  data: {}
networks:
  default: {}
secrets:
  api_key:
    file: ./key.txt
`)

	if _, ok := res.Doc["volumes"]; !ok {
		t.Error("volumes should carry over")
	}
	if _, ok := res.Doc["networks"]; !ok {
		t.Error("networks should carry over")
	}
	if _, ok := res.Doc["version"]; ok {
		t.Error("version should not carry over")
	}
	for _, key := range []string{"version", "secrets"} {
		if _, ok := noteFor(res, "", key); !ok {
			t.Errorf("dropping %q should be reported", key)
		}
	}
}

// TestConvert_OutputAlwaysLoads is the property that matters most: whatever
// goes in, what comes out is a document nix-compose itself accepts. Convert
// checks this internally, so a failure here is a failure to construct.
func TestConvertAndRender_OutputLoads(t *testing.T) {
	res := convert(t, `
version: "3.8"
services:
  web:
    image: nginx:1.27
    ports: [8080:80]
    environment:
      - A=b
    depends_on:
      db:
        condition: service_healthy
  db:
    image: postgres:16
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 10s
volumes:
  data: {}
`)

	rendered, err := Render(res, "docker-compose.yml")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var reloaded map[string]any
	if err := yaml.Unmarshal(rendered, &reloaded); err != nil {
		t.Fatalf("the rendered file is not valid YAML: %v\n%s", err, rendered)
	}
	if err := eval.ValidateDoc(reloaded, "nix-compose.yaml"); err != nil {
		t.Fatalf("the rendered file does not validate: %v\n%s", err, rendered)
	}

	// Ports must survive the round trip as strings. Bare `8080:80` is a
	// sexagesimal integer under YAML 1.1, so they are written quoted.
	if !strings.Contains(string(rendered), `"8080:80"`) {
		t.Errorf("ports should be written quoted:\n%s", rendered)
	}
}

func TestRender_MarksServicesWithNothingToRun(t *testing.T) {
	res := convert(t, `
services:
  web:
    build: .
`)

	rendered, err := Render(res, "docker-compose.yml")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(rendered), "FIXME") {
		t.Errorf("a service with nothing to run should be marked in the file:\n%s", rendered)
	}
}

func TestConvert_Rejections(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"no services", "version: \"3.8\"\n"},
		{"not yaml", "\tthis: [is: not\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Convert([]byte(tc.in)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
