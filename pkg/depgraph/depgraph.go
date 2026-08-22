package depgraph

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// Validate checks the service dependency graph for missing references and cycles.
// Returns a slice of errors (empty if the graph is valid).
func Validate(comp *eval.Composition) []error {
	var errs []error
	errs = append(errs, validateReferences(comp.Services)...)
	errs = append(errs, detectCycles(comp.Services)...)
	return errs
}

// validateReferences checks that all depends_on targets exist as services.
func validateReferences(services map[string]eval.Service) []error {
	var errs []error
	for name, svc := range services {
		for dep := range svc.DependsOn.Entries {
			if _, ok := services[dep]; !ok {
				errs = append(errs, fmt.Errorf("service %q depends on %q, which does not exist", name, dep))
			}
		}
	}
	return errs
}

// color constants for DFS cycle detection.
const (
	white = 0 // unvisited
	gray  = 1 // in current path
	black = 2 // fully visited
)

// detectCycles uses DFS with white/gray/black coloring to find dependency cycles.
func detectCycles(services map[string]eval.Service) []error {
	colors := make(map[string]int, len(services))
	for name := range services {
		colors[name] = white
	}

	var errs []error
	for name := range services {
		if colors[name] == white {
			if err := dfsVisit(name, services, colors); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// dfsVisit performs a depth-first visit of the dependency graph from the given node.
func dfsVisit(node string, services map[string]eval.Service, colors map[string]int) error {
	colors[node] = gray
	svc, ok := services[node]
	if ok {
		for dep := range svc.DependsOn.Entries {
			if colors[dep] == gray {
				return fmt.Errorf("dependency cycle detected involving %q → %q", node, dep)
			}
			if colors[dep] == white {
				if err := dfsVisit(dep, services, colors); err != nil {
					return err
				}
			}
		}
	}
	colors[node] = black
	return nil
}
