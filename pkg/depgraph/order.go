package depgraph

import (
	"fmt"
	"sort"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// buildGraph constructs in-degree map and adjacency list from composition.
func buildGraph(comp *eval.Composition) (map[string]int, map[string][]string) {
	inDegree := make(map[string]int, len(comp.Services))
	dependents := make(map[string][]string, len(comp.Services))

	for name := range comp.Services {
		inDegree[name] = 0
	}
	for name, svc := range comp.Services {
		for dep := range svc.DependsOn.Entries {
			inDegree[name]++
			dependents[dep] = append(dependents[dep], name)
		}
	}
	return inDegree, dependents
}

// StartOrder returns services grouped into levels for dependency-ordered startup.
// Level 0 = leaves (no deps), level N = depends on services in levels < N.
// Returns error if a cycle is detected.
func StartOrder(comp *eval.Composition) ([][]string, error) {
	inDegree, dependents := buildGraph(comp)

	// Kahn's algorithm: BFS from zero in-degree nodes, grouped by round.
	var levels [][]string
	for {
		var level []string
		for name, deg := range inDegree {
			if deg == 0 {
				level = append(level, name)
			}
		}
		if len(level) == 0 {
			break
		}
		sort.Strings(level)

		for _, name := range level {
			delete(inDegree, name)
			for _, dep := range dependents[name] {
				inDegree[dep]--
			}
		}
		levels = append(levels, level)
	}

	// If nodes remain, there is a cycle.
	if len(inDegree) > 0 {
		return nil, fmt.Errorf("dependency cycle detected")
	}

	return levels, nil
}

// StopOrder returns services in reverse dependency order for shutdown.
// Services that depend on others are stopped first.
func StopOrder(comp *eval.Composition) ([][]string, error) {
	levels, err := StartOrder(comp)
	if err != nil {
		return nil, err
	}

	// Reverse the levels.
	for i, j := 0, len(levels)-1; i < j; i, j = i+1, j-1 {
		levels[i], levels[j] = levels[j], levels[i]
	}

	return levels, nil
}
