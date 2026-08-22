// Package composeimport converts a docker-compose file into a nix-compose.yaml.
//
// The conversion is deliberately one-way and lossy, and says so. Most of a
// compose file passes straight through — nix-compose's service fields *are*
// compose's — so the interesting part is not what converts, it is what does
// not. Anything dropped is reported with the reason and, where one exists, the
// thing to write instead. A silent import that quietly loses `build:` would be
// worse than no import at all.
package composeimport

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// Note is something the reader needs to know about the conversion: a dropped
// key, a rewritten value, or a construct with no equivalent.
type Note struct {
	Service string // empty for a top-level note
	Key     string
	Detail  string
}

func (n Note) String() string {
	where := "top level"
	if n.Service != "" {
		where = "service " + strconv.Quote(n.Service)
	}
	if n.Key == "" {
		return fmt.Sprintf("%s: %s", where, n.Detail)
	}
	return fmt.Sprintf("%s: `%s` — %s", where, n.Key, n.Detail)
}

// Result is a converted document and everything the reader should know about
// how it got that way.
type Result struct {
	Doc      map[string]any
	Notes    []Note
	Services []string // in document order, for stable rendering

	// NeedsImage lists services left with nothing to run, usually because
	// they were built from a Dockerfile. They are marked in the output.
	NeedsImage []string
}

// topLevel maps compose's document keys to what the importer does with them.
var topLevelDropped = map[string]string{
	"version":  "obsolete in the compose spec and unused here; removed",
	"configs":  "no equivalent — mount the file with `volumes:` instead",
	"secrets":  "no equivalent — use `x-nix-compose.envFrom` or `volumes:`",
	"include":  "no equivalent — merge the files by hand",
	"profiles": "set profiles per service, not at the top level",
}

// Convert parses a compose document and returns its nix-compose equivalent.
func Convert(data []byte) (*Result, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing compose file: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("compose file is empty")
	}

	res := &Result{Doc: map[string]any{}}

	for _, key := range sortedKeys(doc) {
		switch key {
		case "services":
			continue
		case "networks", "volumes":
			res.Doc[key] = doc[key]
		default:
			detail, known := topLevelDropped[key]
			if !known {
				detail = "not a key nix-compose understands; dropped"
			}
			res.Note("", key, detail)
		}
	}

	rawServices, ok := doc["services"].(map[string]any)
	if !ok || len(rawServices) == 0 {
		return nil, fmt.Errorf("compose file has no services")
	}

	services := map[string]any{}
	for _, name := range sortedKeys(rawServices) {
		svc, ok := rawServices[name].(map[string]any)
		if !ok {
			res.Note(name, "", "service is not a mapping; skipped")
			continue
		}
		converted := res.convertService(name, svc)

		// Dropping `build:` leaves a service that names nothing to run. The
		// document is still structurally valid, so nothing downstream would
		// complain until `up` fails — say it here, and mark the spot in the
		// generated file.
		_, hasImage := converted["image"]
		_, hasPackage := converted["package"]
		if !hasImage && !hasPackage {
			res.Note(name, "", "nothing to run — add `package:` or `image:` "+
				"before this service can start")
			res.NeedsImage = append(res.NeedsImage, name)
		}

		services[name] = converted
		res.Services = append(res.Services, name)
	}
	res.Doc["services"] = services

	// The importer's output has to be something the loader accepts. If it is
	// not, that is a defect here rather than the user's problem.
	if err := eval.ValidateDoc(res.Doc, "generated nix-compose.yaml"); err != nil {
		return nil, fmt.Errorf("the conversion produced a document nix-compose "+
			"would reject, which is a bug — please report it:\n%w", err)
	}

	return res, nil
}

// Note records something about the conversion.
func (r *Result) Note(service, key, detail string) {
	r.Notes = append(r.Notes, Note{Service: service, Key: key, Detail: detail})
}

func (r *Result) convertService(name string, svc map[string]any) map[string]any {
	known := eval.ServiceKeys()
	out := map[string]any{}

	for _, key := range sortedKeys(svc) {
		value := svc[key]

		switch key {
		case "build":
			// The one worth spelling out: it is why the project exists.
			r.Note(name, "build", "CRI has no build API. Replace it with "+
				"`package: <nixpkgs attribute>` to build the image from a Nix "+
				"closure, or keep a prebuilt `image:`")
			continue
		case "package":
			// A compose file cannot legitimately have this.
			r.Note(name, "package", "already a nix-compose key; kept as written")
			out[key] = value
			continue
		case "environment":
			out[key] = r.convertEnvironment(name, value)
			continue
		case "ports":
			out[key] = r.convertPorts(name, value)
			continue
		case "volumes":
			out[key] = r.convertVolumes(name, value)
			continue
		}

		if !known[key] {
			r.Note(name, key, "no equivalent in nix-compose; dropped")
			continue
		}
		out[key] = value
	}

	return out
}

// convertEnvironment normalises compose's two spellings into the mapping form.
// `- FOO` with no value means "inherit from the host environment", which a
// declarative composition cannot express, so it is reported rather than guessed
// at.
func (r *Result) convertEnvironment(svc string, value any) any {
	list, ok := value.([]any)
	if !ok {
		return value // already a mapping
	}

	out := map[string]any{}
	for _, item := range list {
		entry, ok := item.(string)
		if !ok {
			r.Note(svc, "environment", fmt.Sprintf("entry %v is not a string; dropped", item))
			continue
		}
		key, val, found := strings.Cut(entry, "=")
		if !found {
			r.Note(svc, "environment", fmt.Sprintf(
				"`%s` takes its value from the host environment, which a "+
					"composition cannot express; set it explicitly", key))
			continue
		}
		out[key] = val
	}
	return out
}

// convertPorts turns every compose port spelling into the short string form.
func (r *Result) convertPorts(svc string, value any) any {
	list, ok := value.([]any)
	if !ok {
		return value
	}

	var out []any
	for _, item := range list {
		switch p := item.(type) {
		case string:
			out = append(out, p)
		case int:
			// A bare `- 8080` is a container port compose publishes randomly.
			out = append(out, strconv.Itoa(p))
		case map[string]any:
			short, err := longPortToShort(p)
			if err != nil {
				r.Note(svc, "ports", fmt.Sprintf("%v — %s; dropped", p, err))
				continue
			}
			out = append(out, short)
		default:
			r.Note(svc, "ports", fmt.Sprintf("entry %v has an unexpected type; dropped", item))
		}
	}
	return out
}

func longPortToShort(p map[string]any) (string, error) {
	target, ok := scalarString(p["target"])
	if !ok {
		return "", fmt.Errorf("long syntax without a `target`")
	}

	short := target
	if published, ok := scalarString(p["published"]); ok {
		short = published + ":" + target
	}
	if proto, ok := p["protocol"].(string); ok && proto != "" && proto != "tcp" {
		short += "/" + proto
	}
	if mode, ok := p["mode"].(string); ok && mode == "host" {
		return short, nil
	}
	return short, nil
}

// convertVolumes reduces long-syntax mounts to the short form where they have
// one, and reports the mount types that do not.
func (r *Result) convertVolumes(svc string, value any) any {
	list, ok := value.([]any)
	if !ok {
		return value
	}

	var out []any
	for _, item := range list {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			typ, _ := v["type"].(string)
			target, hasTarget := scalarString(v["target"])
			source, hasSource := scalarString(v["source"])

			if !hasTarget {
				r.Note(svc, "volumes", fmt.Sprintf("%v — long syntax without a `target`; dropped", v))
				continue
			}
			switch typ {
			case "", "volume", "bind":
				if !hasSource {
					out = append(out, target)
					continue
				}
				mount := source + ":" + target
				if ro, ok := v["read_only"].(bool); ok && ro {
					mount += ":ro"
				}
				out = append(out, mount)
			case "tmpfs":
				r.Note(svc, "volumes", fmt.Sprintf(
					"%s is a tmpfs mount; move it to `tmpfs: [\"%s\"]`", target, target))
			default:
				r.Note(svc, "volumes", fmt.Sprintf("mount type %q has no short form; dropped", typ))
			}
		default:
			r.Note(svc, "volumes", fmt.Sprintf("entry %v has an unexpected type; dropped", item))
		}
	}
	return out
}

func scalarString(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		if s == "" {
			return "", false
		}
		return s, true
	case int:
		return strconv.Itoa(s), true
	default:
		return "", false
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
