package convert

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ConditionMap maps resourceID → dependentID → condition string.
// For each resource, it records what conditions downstream dependents impose.
type ConditionMap map[string]map[string]string

// Bridge converts a convert.Result into a deploy.Deployment and extracts
// dependency conditions for use during synchronous apply.
func Bridge(result *Result, registry *typing.Registry) (*deploy.Deployment, ConditionMap, error) {
	// Build edge index: fromID → []Reference (dependency targets).
	edgeIndex := make(map[string]typing.ReferenceList)
	for _, edge := range result.Edges {
		fromID := edge.From.GetId()
		edgeIndex[fromID] = append(edgeIndex[fromID], edge.To)
	}

	// Build condition map: toID → fromID → condition.
	conditions := make(ConditionMap)
	for _, edge := range result.Edges {
		if edge.Condition == "" {
			continue
		}
		toID := edge.To.GetId()
		if conditions[toID] == nil {
			conditions[toID] = make(map[string]string)
		}
		conditions[toID][edge.From.GetId()] = edge.Condition
	}

	deployment := deploy.NewDeployment()

	for _, m := range result.Manifests {
		// Derive DefinitionKey from apiVersion/kind.
		key := typing.DefinitionKey(m.APIVersion + "/" + m.Kind)

		body, err := json.Marshal(m.Spec)
		if err != nil {
			return nil, nil, fmt.Errorf("marshaling spec for %s %s: %w", m.Kind, m.Metadata.Name, err)
		}

		instance, err := registry.Instantiate(key, body)
		if err != nil {
			return nil, nil, fmt.Errorf("instantiating %s %s: %w", m.Kind, m.Metadata.Name, err)
		}

		// Gather dependencies from edge index.
		deps := edgeIndex[m.Metadata.Name]

		deployment.AddCreation(instance, body, deps)
	}

	return deployment, conditions, nil
}
