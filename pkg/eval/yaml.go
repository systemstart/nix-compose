package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	nixsrc "github.com/systemstart/nix-compose/nix"
	"github.com/systemstart/nix-compose/pkg/nixerror"
	"github.com/systemstart/nix-compose/pkg/nixpins"
)

// YAMLFileNames are the project files that select YAML mode, in precedence
// order. The name is deliberately not compose.yaml: docker compose claims that
// one, and a file it would pick up and choke on is a worse first experience
// than a name that is obviously ours.
var YAMLFileNames = []string{"nix-compose.yaml", "nix-compose.yml"}

// topLevelKeys are the document keys a YAML project may set. Compose's own
// top-level keys are here because the composition is compose-shaped;
// `nixpkgs` is nix-compose's own.
var topLevelKeys = map[string]string{
	"services":              "",
	"networks":              "",
	"volumes":               "",
	"name":                  "",
	"nixpkgs":               "",
	"version":               "accepted and ignored — compose dropped it too",
	"x-nix-compose-microvm": "",
}

// projectNamePattern is compose's own constraint on a project name. The name
// becomes part of pod and container names, so it cannot be arbitrary.
var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// userError marks an error that is already phrased for the user and must not
// be reinterpreted on the way out. YAML mode produces two kinds of failure the
// generic nix-stderr handling would only make worse: document errors that
// never ran nix at all, and nix errors whose location points into a temporary
// directory.
type userError struct{ error }

func (u userError) Unwrap() error { return u.error }

// FindYAMLFile returns the YAML project file in dir, or "" if there is none.
func FindYAMLFile(dir string) string {
	for _, name := range YAMLFileNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// LoadYAML reads and validates a YAML project document, returning it as the
// JSON that both evaluation paths consume.
func LoadYAML(path string) (doc map[string]any, usesNix bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	if doc == nil {
		return nil, false, fmt.Errorf("%s is empty", filepath.Base(path))
	}

	if err := validateYAML(doc, filepath.Base(path)); err != nil {
		return nil, false, err
	}

	return doc, yamlUsesNix(doc), nil
}

// yamlUsesNix reports whether the document needs a Nix evaluation at all. A
// project that only names registry images is pure data: it can go straight to
// a Composition without nix ever being executed, which is most of what makes
// YAML mode feel different from the Nix front-ends.
func yamlUsesNix(doc map[string]any) bool {
	services, _ := doc["services"].(map[string]any)
	for _, raw := range services {
		if svc, ok := raw.(map[string]any); ok {
			if _, has := svc["package"]; has {
				return true
			}
		}
	}
	return false
}

// projectNameProblems validates the optional `name:` key. The name ends up in
// pod and container names, so a bad one fails much later and much less legibly
// than it does here.
func projectNameProblems(doc map[string]any) []string {
	raw, present := doc["name"]
	if !present {
		return nil
	}
	name, ok := raw.(string)
	switch {
	case !ok:
		return []string{fmt.Sprintf("`name:` must be a string, got %T", raw)}
	case name == "":
		return []string{"`name:` is empty — remove it to use the directory name"}
	case !projectNamePattern.MatchString(name):
		return []string{fmt.Sprintf(
			"`name: %s` is not a valid project name — use lowercase letters, digits, "+
				"`-` and `_`, starting with a letter or digit", name)}
	}
	return nil
}

// validateYAML rejects keys nix-compose does not understand. Compose silently
// ignores unknown keys, which is how a typo becomes an hour of debugging; the
// error here follows the `build:` message's shape — name the key, say what to
// write instead.
func validateYAML(doc map[string]any, filename string) error {
	var problems []string

	for key := range doc {
		if _, ok := topLevelKeys[key]; !ok {
			problems = append(problems, fmt.Sprintf(
				"unknown top-level key %q (expected one of: %s)",
				key, strings.Join(sortedKeys(topLevelKeys), ", ")))
		}
	}

	problems = append(problems, projectNameProblems(doc)...)

	// A missing `services:` is reported alongside the unknown keys rather than
	// instead of them: when the cause is a misspelled `servcies:`, the unknown
	// key is the only part of the message that points at the fix.
	services, ok := doc["services"].(map[string]any)
	switch {
	case doc["services"] == nil:
		problems = append(problems, "no `services:` — there is nothing to run")
	case !ok:
		problems = append(problems, "`services:` must be a mapping of name to service")
	case len(services) == 0:
		problems = append(problems, "`services:` is empty")
	}
	if len(problems) > 0 && len(services) == 0 {
		return fmt.Errorf("%s:\n  - %s", filename, strings.Join(problems, "\n  - "))
	}

	known := serviceKeys()
	for _, name := range sortedKeys(services) {
		svc, ok := services[name].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("service %q must be a mapping", name))
			continue
		}
		for _, key := range sortedKeys(svc) {
			if !known[key] {
				problems = append(problems, fmt.Sprintf(
					"service %q: unknown key %q", name, key))
			}
		}
		if _, hasImage := svc["image"]; hasImage {
			if _, hasPkg := svc["package"]; hasPkg {
				problems = append(problems, fmt.Sprintf(
					"service %q sets both `image` and `package` — pick one", name))
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s:\n  - %s", filename, strings.Join(problems, "\n  - "))
}

// ServiceKeys is the set of keys a service may carry: every compose field the
// Composition understands, plus `package`. Deriving it from the struct rather
// than a hand-written list means a new field cannot be supported by the
// backend and rejected by the parser at the same time.
//
// Exported because the compose importer decides what survives a conversion by
// asking this, rather than keeping its own idea of what is supported.
func ServiceKeys() map[string]bool {
	return serviceKeys()
}

// ValidateDoc reports the problems in an already-parsed document. The importer
// uses it to check its own output before writing: a generated file that the
// loader would reject is a bug worth catching at the source.
func ValidateDoc(doc map[string]any, filename string) error {
	return validateYAML(doc, filename)
}

func serviceKeys() map[string]bool {
	keys := map[string]bool{"package": true}
	t := reflect.TypeOf(Service{})
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// evalYAML turns a nix-compose.yaml into the same JSON a flake evaluation
// produces, so everything downstream is unable to tell the difference.
func (e *Evaluator) evalYAML(ctx context.Context) ([]byte, []byte, error) {
	path := FindYAMLFile(e.ProjectDir)
	if path == "" {
		return nil, nil, fmt.Errorf("no %s in %s", YAMLFileNames[0], e.ProjectDir)
	}

	doc, usesNix, err := LoadYAML(path)
	if err != nil {
		return nil, nil, userError{err}
	}

	nixpkgsRef := NixpkgsRefFor(doc)

	// Strip nix-compose's own keys; what remains is already a composition.
	// `name` is addressing metadata, not part of the composition — it is read
	// back by ProjectNameFromDir where a project has to be located.
	delete(doc, "nixpkgs")
	delete(doc, "version")
	delete(doc, "name")

	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("converting %s to JSON: %w", filepath.Base(path), err)
	}

	// No `package:` anywhere means nothing to build, so nix is never run. A
	// registry-only project evaluates in microseconds and works on a machine
	// with no Nix installed at all.
	if !usesNix {
		return raw, nil, nil
	}

	return e.resolveYAMLWithNix(ctx, nixpkgsRef, raw)
}

// resolveYAMLWithNix evaluates the document against nixpkgs so `package:`
// becomes a built image, via the same nix/lib.nix a flake project uses.
func (e *Evaluator) resolveYAMLWithNix(ctx context.Context, nixpkgsRef string, raw []byte) ([]byte, []byte, error) {
	dir, err := os.MkdirTemp("", "nix-compose-yaml-")
	if err != nil {
		return nil, nil, fmt.Errorf("creating evaluation directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	for _, name := range []string{"lib.nix", "yaml.nix"} {
		src, err := nixsrc.Files.ReadFile(name)
		if err != nil {
			return nil, nil, fmt.Errorf("reading embedded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing %s: %w", name, err)
		}
	}

	dataPath := filepath.Join(dir, "data.json")
	if err := os.WriteFile(dataPath, raw, 0o644); err != nil {
		return nil, nil, fmt.Errorf("writing evaluation input: %w", err)
	}

	expr := yamlExpr(dir, dataPath, nixpkgsRef, NixSystem())

	// --impure is required whichever way this goes: the embedded sources are
	// read from an absolute path, and a `nixpkgs:` override naming a branch
	// rather than a revision could not be fetched without it. Reproducibility
	// comes from the pins in pkg/nixpins, not from the evaluation mode.
	stdout, stderr, err := e.Runner.Run(ctx, "nix", "eval", "--impure", "--json", "--expr", expr)
	if err != nil {
		return stdout, nil, userError{yamlEvalError(string(stderr), exitCode(err), dir)}
	}
	return stdout, stderr, nil
}

// yamlEvalError turns nix's stderr into something a YAML user can act on. The
// location nix reports is inside the temporary directory the embedded sources
// were written to — an implementation detail that would send the reader
// looking for a file that no longer exists — so it is dropped, leaving the
// message, which is written for them.
func yamlEvalError(stderr string, code int, tmpDir string) error {
	nixErr := nixerror.ParseStderr(stderr, code)
	if strings.HasPrefix(nixErr.File, tmpDir) {
		nixErr.File = ""
	}
	return nixErr
}

// NixpkgsRefFor returns the flake reference a document resolves `package:`
// against: its own `nixpkgs:` if it sets one, otherwise the pin this binary
// carries.
func NixpkgsRefFor(doc map[string]any) string {
	if ref, ok := doc["nixpkgs"].(string); ok && ref != "" {
		return ref
	}
	return nixpins.NixpkgsRef()
}

// ProjectNameFor returns the `name:` a document declares, or "" if it declares
// none. Callers fall back to the directory basename, which is compose's
// default too — and which quietly collides whenever two projects live in
// like-named directories (`*/test/integration/` is a common one).
func ProjectNameFor(doc map[string]any) string {
	name, _ := doc["name"].(string)
	return name
}

// ProjectNameFromDir reads just the project name out of the YAML document in
// dir, for the commands that need to address an existing project (`ps`,
// `logs`, `down`) without evaluating it. Any problem reading or parsing the
// file yields "" — those commands have their own, better errors for a
// document that does not load, and this must not pre-empt them.
func ProjectNameFromDir(dir string) string {
	path := FindYAMLFile(dir)
	if path == "" {
		return ""
	}
	doc, _, err := LoadYAML(path)
	if err != nil {
		return ""
	}
	return ProjectNameFor(doc)
}

func yamlExpr(dir, dataPath, nixpkgsRef, system string) string {
	return fmt.Sprintf(`let
  nixpkgs = builtins.getFlake %q;
  nix-oci = builtins.getFlake %q;
  pkgs = nixpkgs.legacyPackages.%s;
  nclib = import %s/lib.nix {
    inherit (pkgs) lib;
    nix-oci = nix-oci.legacyPackages.%s;
  };
  composition = import %s/yaml.nix {
    inherit pkgs nclib;
    data = builtins.fromJSON (builtins.readFile %s);
  };
in composition.config.out.dockerComposeYamlAttrs`,
		nixpkgsRef, nixpins.NixOCIRef(), system, dir, system, dir, dataPath)
}

// NixSystem returns the Nix system double for the host, e.g. x86_64-linux.
func NixSystem() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "i686"
	}
	return arch + "-" + runtime.GOOS
}
