package typing

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

// Registry manages Definition registrations. Unlike the infrastructure-tool,
// this is instance-based rather than using package globals.
type Registry struct {
	mu         sync.RWMutex
	registered map[DefinitionKey]Definition
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		registered: make(map[DefinitionKey]Definition),
	}
}

// Register adds a Definition. Panics on duplicate key.
func (reg *Registry) Register(t Definition) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	key := t.GetKey()
	if _, exists := reg.registered[key]; exists {
		panic("re-registered: " + key)
	}
	reg.registered[key] = t
	log.Printf("typing: registered '%s'", key)
}

// AllDefinitions returns all registered Definitions.
func (reg *Registry) AllDefinitions() []Definition {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	result := make([]Definition, 0, len(reg.registered))
	for _, entry := range reg.registered {
		result = append(result, entry)
	}
	return result
}

// GetDefinition looks up a Definition by key.
func (reg *Registry) GetDefinition(k DefinitionKey) (Definition, error) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	result, exists := reg.registered[k]
	if !exists {
		return nil, fmt.Errorf("%w: '%s'", ErrUnknownDefinition, k)
	}
	if result == nil {
		panic("no definition")
	}
	return result, nil
}

// Instantiate creates an Instance from a key and raw JSON body.
func (reg *Registry) Instantiate(k DefinitionKey, r json.RawMessage) (Instance, error) {
	d, err := reg.GetDefinition(k)
	if err != nil {
		return nil, fmt.Errorf("%w: no definition for %s", err, k)
	}
	instance, err := d.Instantiate(r)
	if err != nil {
		return nil, fmt.Errorf("instantiate %s: %w", k, err)
	}
	return instance, nil
}

// Delete delegates to the Definition's Delete method.
func (reg *Registry) Delete(r Reference) error {
	d, err := reg.GetDefinition(r.GetKey())
	if err != nil {
		return fmt.Errorf("%w: no definition for %s %s", err, r.GetKey(), r.GetId())
	}
	if err := d.Delete(r); err != nil {
		return fmt.Errorf("delete %s %s: %w", r.GetKey(), r.GetId(), err)
	}
	return nil
}

// LoadInstance loads an Instance from persisted JSON.
func (reg *Registry) LoadInstance(k DefinitionKey, r json.RawMessage) (Instance, error) {
	definition, err := reg.GetDefinition(k)
	if err != nil {
		return nil, fmt.Errorf("no definition %s: %w", k, err)
	}
	resource, err := definition.Load(r)
	if err != nil {
		return nil, fmt.Errorf("create resource for %s failed: %w", k, err)
	}

	if resource.GetId() == "" {
		log.Printf("invalid body for '%s': '%s'", k, string(r))
		return nil, fmt.Errorf("invalid body for %s", k)
	}

	return resource, nil
}

// Clear removes all registered definitions.
func (reg *Registry) Clear() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.registered = make(map[DefinitionKey]Definition)
}
