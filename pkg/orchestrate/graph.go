package orchestrate

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// GraphNode represents a resource in the dependency graph.
type GraphNode struct {
	ID     string
	Kind   string
	Status string
}

// GraphEdge represents a dependency link.
type GraphEdge struct {
	SourceID string
	TargetID string
}

// Graph returns all nodes (rollouts) and edges (links) in the DAG.
func (e *Engine) Graph() ([]GraphNode, []GraphEdge, error) {
	rollouts, err := e.State()
	if err != nil {
		return nil, nil, fmt.Errorf("loading state: %w", err)
	}

	nodes := make([]GraphNode, 0, len(rollouts))
	for _, r := range rollouts {
		nodes = append(nodes, rolloutToNode(r))
	}

	links, err := e.loadLinks()
	if err != nil {
		return nil, nil, fmt.Errorf("loading links: %w", err)
	}

	edges := make([]GraphEdge, 0, len(links))
	for _, l := range links {
		edges = append(edges, GraphEdge{
			SourceID: l.Source.GetId(),
			TargetID: l.Target.GetId(),
		})
	}

	return nodes, edges, nil
}

// TransitiveDeps returns all transitive dependencies of a resource (BFS over GetDependencies).
func (e *Engine) TransitiveDeps(resourceID string) ([]GraphNode, error) {
	return e.bfsTraverse(resourceID, func(ref typing.Reference) (typing.ReferenceList, error) {
		return e.db.GetDependencies(ref)
	})
}

// TransitiveDependents returns all transitive dependents of a resource (BFS over GetDepending).
func (e *Engine) TransitiveDependents(resourceID string) ([]GraphNode, error) {
	return e.bfsTraverse(resourceID, func(ref typing.Reference) (typing.ReferenceList, error) {
		return e.db.GetDepending(ref)
	})
}

// bfsTraverse performs a breadth-first traversal starting from resourceID,
// using the provided neighbor function to discover adjacent nodes.
func (e *Engine) bfsTraverse(resourceID string, neighbors func(typing.Reference) (typing.ReferenceList, error)) ([]GraphNode, error) {
	visited := map[string]bool{resourceID: true}
	queue := []string{resourceID}
	var result []GraphNode

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		ref := typing.NewReference(current, "")
		refs, err := neighbors(ref)
		if err != nil {
			continue
		}

		for _, r := range refs {
			id := r.GetId()
			if visited[id] {
				continue
			}
			visited[id] = true
			queue = append(queue, id)

			node, err := e.lookupNode(id)
			if err != nil {
				// Node may not have a rollout (e.g. external dependency).
				node = GraphNode{
					ID:     id,
					Kind:   string(r.GetKey()),
					Status: "UNKNOWN",
				}
			}
			result = append(result, node)
		}
	}

	return result, nil
}

// lookupNode loads a rollout from the DB and converts it to a GraphNode.
func (e *Engine) lookupNode(id string) (GraphNode, error) {
	rollout, err := deploy.LoadRollout(e.db, id)
	if err != nil || rollout == nil {
		return GraphNode{}, fmt.Errorf("rollout %s not found", id)
	}
	return rolloutToNode(rollout), nil
}

// rolloutToNode converts a Rollout to a GraphNode.
func rolloutToNode(r *deploy.Rollout) GraphNode {
	status := "UNKNOWN"
	if r.Status != nil {
		status = string(r.Status.GetShort())
	}
	kind := kindFromKey(r.InstanceKey)
	return GraphNode{
		ID:     r.InstanceId,
		Kind:   kind,
		Status: status,
	}
}

// kindFromKey extracts the Kind part from a DefinitionKey (group/version/kind).
func kindFromKey(key typing.DefinitionKey) string {
	gvk := key.GetGVK()
	return gvk.Kind
}
