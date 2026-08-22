package composeimport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// Suggestion is one service's answer to "could this stop coming from a
// registry?".
type Suggestion struct {
	Service  string
	Image    string // the reference as written
	Attr     string // the nixpkgs attribute, empty when there is no match
	Version  string
	MainProg bool // whether meta.mainProgram is set — if not, an entrypoint is needed

	// TagMajor and PkgMajor are the major versions of the image tag and of
	// the nixpkgs package. They differ often enough to matter: `redis:7` maps
	// to a nixpkgs redis 8, and taking the suggestion unread would be a major
	// upgrade nobody asked for.
	TagMajor string
	PkgMajor string
}

// MajorMismatch reports whether taking this suggestion would change the major
// version of the software being run.
func (s Suggestion) MajorMismatch() bool {
	return s.TagMajor != "" && s.PkgMajor != "" && s.TagMajor != s.PkgMajor
}

// majorOf returns the leading numeric component of a version-ish string.
// Tags carry all sorts of decoration (`7-alpine`, `16.2-bookworm`, `latest`);
// only the leading number is worth trusting, and only when there is one.
func majorOf(s string) string {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return ""
	}
	return s[:end]
}

// tagOf returns the tag portion of an image reference, if it has one.
func tagOf(image string) string {
	ref := image
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	colon := strings.LastIndex(ref, ":")
	if colon < 0 || colon < strings.LastIndex(ref, "/") {
		return ""
	}
	return ref[colon+1:]
}

// aliases maps image names to the nixpkgs attribute they are called instead.
// Without these the common cases all miss: nothing named `postgres` or `node`
// exists in nixpkgs, which would make `suggest` look useless on exactly the
// stacks people actually run.
var aliases = map[string][]string{
	"postgres":   {"postgresql"},
	"node":       {"nodejs"},
	"golang":     {"go"},
	"python":     {"python3"},
	"mysql":      {"mariadb"},
	"mongo":      {"mongodb"},
	"rabbitmq":   {"rabbitmq-server"},
	"nginx":      {"nginx"},
	"memcached":  {"memcached"},
	"traefik":    {"traefik"},
	"prometheus": {"prometheus"},
	"grafana":    {"grafana"},
	"httpd":      {"apacheHttpd"},
	"caddy":      {"caddy"},
}

// ImageCandidates returns the nixpkgs attribute names worth trying for an
// image reference, most likely first.
//
// A reference carries a registry, an organisation, a name and a tag, and only
// the name is a plausible package. `docker.io/library/postgres:16` is asking
// about `postgresql`.
func ImageCandidates(image string) []string {
	ref := image

	// Strip the digest and tag. A colon before the last slash is a registry
	// port, not a tag separator.
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		ref = ref[:colon]
	}

	// Keep only the final path element.
	if slash := strings.LastIndex(ref, "/"); slash >= 0 {
		ref = ref[slash+1:]
	}
	if ref == "" {
		return nil
	}

	candidates := []string{ref}
	for _, alias := range aliases[ref] {
		if alias != ref {
			candidates = append(candidates, alias)
		}
	}
	return candidates
}

// Lookup asks nixpkgs which of the candidate attributes exist. It answers for
// every name in one evaluation, because starting a nix process per service
// would make `suggest` slower than the thing it is advising about.
func Lookup(ctx context.Context, runner eval.CommandRunner, nixpkgsRef string, names []string) (map[string]Suggestion, error) {
	if len(names) == 0 {
		return map[string]Suggestion{}, nil
	}

	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}

	expr := fmt.Sprintf(`let
  nixpkgs = builtins.getFlake %q;
  pkgs = nixpkgs.legacyPackages.%s;
  probe = name:
    let drv = pkgs.${name};
    in if !(pkgs ? ${name}) then null
       else {
         version = drv.version or "";
         mainProgram = (drv.meta.mainProgram or null) != null;
       };
in builtins.listToAttrs (map (n: { name = n; value = probe n; }) [ %s ])`,
		nixpkgsRef, eval.NixSystem(), strings.Join(quoted, " "))

	stdout, stderr, err := runner.Run(ctx, "nix", "eval", "--impure", "--json", "--expr", expr)
	if err != nil {
		return nil, fmt.Errorf("querying nixpkgs: %s: %w", strings.TrimSpace(string(stderr)), err)
	}

	var raw map[string]*struct {
		Version     string `json:"version"`
		MainProgram bool   `json:"mainProgram"`
	}
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return nil, fmt.Errorf("parsing nixpkgs response: %w", err)
	}

	out := map[string]Suggestion{}
	for name, info := range raw {
		if info == nil {
			continue
		}
		out[name] = Suggestion{
			Attr:     name,
			Version:  info.Version,
			MainProg: info.MainProgram,
		}
	}
	return out, nil
}

// Suggest resolves every registry image in a document to a nixpkgs package
// where one exists. Services that already name a package are left out: there
// is nothing to suggest about them.
func Suggest(ctx context.Context, runner eval.CommandRunner, nixpkgsRef string, doc map[string]any) ([]Suggestion, error) {
	services, _ := doc["services"].(map[string]any)

	var ordered []Suggestion
	var wanted []string
	seen := map[string]bool{}

	for _, name := range sortedKeys(services) {
		svc, ok := services[name].(map[string]any)
		if !ok {
			continue
		}
		image, ok := svc["image"].(string)
		if !ok || image == "" {
			continue
		}

		ordered = append(ordered, Suggestion{Service: name, Image: image})
		for _, candidate := range ImageCandidates(image) {
			if !seen[candidate] {
				seen[candidate] = true
				wanted = append(wanted, candidate)
			}
		}
	}

	found, err := Lookup(ctx, runner, nixpkgsRef, wanted)
	if err != nil {
		return nil, err
	}

	for i, s := range ordered {
		for _, candidate := range ImageCandidates(s.Image) {
			if hit, ok := found[candidate]; ok {
				ordered[i].Attr = hit.Attr
				ordered[i].Version = hit.Version
				ordered[i].MainProg = hit.MainProg
				ordered[i].TagMajor = majorOf(tagOf(s.Image))
				ordered[i].PkgMajor = majorOf(hit.Version)
				break
			}
		}
	}

	return ordered, nil
}
