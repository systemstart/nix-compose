package watch

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// DiffResult describes changes between two compositions.
type DiffResult struct {
	Added   []string
	Removed []string
	Changed []string
}

// IsEmpty returns true if there are no changes.
func (d *DiffResult) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// DiffCompositions compares two compositions and returns the services that
// were added, removed, or changed. Services are compared by JSON-marshaling
// each eval.Service and comparing the resulting bytes.
func DiffCompositions(old, new *eval.Composition) (*DiffResult, error) {
	result := &DiffResult{}

	oldJSON, err := marshalServices(old.Services)
	if err != nil {
		return nil, fmt.Errorf("marshaling old services: %w", err)
	}

	newJSON, err := marshalServices(new.Services)
	if err != nil {
		return nil, fmt.Errorf("marshaling new services: %w", err)
	}

	for name := range newJSON {
		oldData, exists := oldJSON[name]
		if !exists {
			result.Added = append(result.Added, name)
			continue
		}
		if string(oldData) != string(newJSON[name]) {
			result.Changed = append(result.Changed, name)
		}
	}

	for name := range oldJSON {
		if _, exists := newJSON[name]; !exists {
			result.Removed = append(result.Removed, name)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Removed)
	sort.Strings(result.Changed)

	return result, nil
}

func marshalServices(services map[string]eval.Service) (map[string][]byte, error) {
	m := make(map[string][]byte, len(services))
	for name, svc := range services {
		data, err := json.Marshal(svc)
		if err != nil {
			return nil, fmt.Errorf("marshaling service %q: %w", name, err)
		}
		m[name] = data
	}
	return m, nil
}
