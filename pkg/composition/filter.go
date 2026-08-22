package composition

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// FilterByProfiles returns a new Composition containing only services that
// match the given profiles. An empty profiles list means all services are
// included. Services with no profiles are always included.
//
// When a profiled service is activated, its depends_on targets are
// transitively included regardless of their own profile tags (matching
// Docker Compose spec behaviour).
//
// After filtering, dangling depends_on references are pruned.
func FilterByProfiles(comp *eval.Composition, profiles []string) *eval.Composition {
	if len(profiles) == 0 {
		return comp
	}

	// First pass: collect directly matched services.
	matched := make(map[string]bool, len(comp.Services))
	for name, svc := range comp.Services {
		if serviceMatchesProfile(svc, profiles) {
			matched[name] = true
		}
	}

	// Second pass: transitively include depends_on targets.
	includeDepsTransitive(comp, matched)

	filtered := &eval.Composition{
		Services: make(map[string]eval.Service, len(matched)),
		Networks: comp.Networks,
		Volumes:  comp.Volumes,
	}

	for name := range matched {
		filtered.Services[name] = comp.Services[name]
	}

	pruneDanglingDeps(filtered)
	return filtered
}

// includeDepsTransitive walks the depends_on graph from the matched set
// and includes all transitive dependencies.
func includeDepsTransitive(comp *eval.Composition, matched map[string]bool) {
	queue := make([]string, 0, len(matched))
	for name := range matched {
		queue = append(queue, name)
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		svc, ok := comp.Services[name]
		if !ok {
			continue
		}
		for dep := range svc.DependsOn.Entries {
			if !matched[dep] {
				if _, exists := comp.Services[dep]; exists {
					matched[dep] = true
					queue = append(queue, dep)
				}
			}
		}
	}
}

// serviceProfiles returns the effective profiles for a service.
// Top-level profiles take precedence. If only x-nix-compose.profiles is set,
// a deprecation warning is printed.
func serviceProfiles(svc eval.Service) []string {
	if len(svc.Profiles) > 0 {
		return svc.Profiles
	}
	if svc.XNixCompose != nil && len(svc.XNixCompose.Profiles) > 0 {
		return svc.XNixCompose.Profiles
	}
	return nil
}

// WarnDeprecatedProfiles prints a warning for each service that still uses
// x-nix-compose.profiles instead of top-level profiles.
func WarnDeprecatedProfiles(comp *eval.Composition) {
	for name, svc := range comp.Services {
		if len(svc.Profiles) == 0 && svc.XNixCompose != nil && len(svc.XNixCompose.Profiles) > 0 {
			fmt.Printf("Warning: service %q uses x-nix-compose.profiles (deprecated); move to top-level profiles\n", name)
		}
	}
}

// serviceMatchesProfile returns true if the service should be included given
// the active profiles. Services with no profiles are always included. Match
// is OR across profiles.
func serviceMatchesProfile(svc eval.Service, profiles []string) bool {
	sp := serviceProfiles(svc)
	if len(sp) == 0 {
		return true
	}

	for _, s := range sp {
		for _, p := range profiles {
			if s == p {
				return true
			}
		}
	}
	return false
}

// pruneDanglingDeps removes depends_on entries that reference services not
// present in the filtered composition.
func pruneDanglingDeps(comp *eval.Composition) {
	for name, svc := range comp.Services {
		if svc.DependsOn.IsEmpty() {
			continue
		}
		pruned := make(map[string]eval.DependsOnEntry, len(svc.DependsOn.Entries))
		for dep, entry := range svc.DependsOn.Entries {
			if _, ok := comp.Services[dep]; ok {
				pruned[dep] = entry
			}
		}
		svc.DependsOn = eval.DependsOnValue{Entries: pruned}
		if len(pruned) == 0 {
			svc.DependsOn = eval.DependsOnValue{}
		}
		comp.Services[name] = svc
	}
}
