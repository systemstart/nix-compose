package deploy

import (
	"fmt"
	"sync"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// Provider is the interface that resource providers must implement.
type Provider interface {
	GetReferencesTo(r typing.Reference) ([]typing.Rollout, error)
	Remove(l typing.Reference) error
	GetStatus(r typing.Reference) (typing.Status, error)
	GetDefinitions() []typing.Definition
}

// ProviderRegistry manages named providers and routes definition keys.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	routing   map[typing.DefinitionKey]Provider
	registry  *typing.Registry
}

// NewProviderRegistry creates a ProviderRegistry backed by the given typing.Registry.
func NewProviderRegistry(reg *typing.Registry) *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]Provider),
		routing:   make(map[typing.DefinitionKey]Provider),
		registry:  reg,
	}
}

// Register adds a named provider. Each provider's definitions are registered
// in the typing registry and mapped for routing.
func (pr *ProviderRegistry) Register(name string, p Provider) {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	if _, exists := pr.providers[name]; exists {
		panic("re-registering provider: " + name)
	}

	for _, d := range p.GetDefinitions() {
		key := d.GetKey()
		if _, exists := pr.routing[key]; exists {
			panic(fmt.Sprintf("definition %s already owned by another provider", key))
		}
		pr.registry.Register(d)
		pr.routing[key] = p
	}
	pr.providers[name] = p
}

// ForKey returns the provider that owns a given definition key.
func (pr *ProviderRegistry) ForKey(key typing.DefinitionKey) (Provider, error) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()

	p, ok := pr.routing[key]
	if !ok {
		return nil, fmt.Errorf("no provider registered for %s", key)
	}
	return p, nil
}

// All returns every registered provider.
func (pr *ProviderRegistry) All() []Provider {
	pr.mu.RLock()
	defer pr.mu.RUnlock()

	result := make([]Provider, 0, len(pr.providers))
	for _, p := range pr.providers {
		result = append(result, p)
	}
	return result
}

// Clear resets the registry, removing all providers and definitions.
func (pr *ProviderRegistry) Clear() {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	pr.providers = make(map[string]Provider)
	pr.routing = make(map[typing.DefinitionKey]Provider)
	pr.registry.Clear()
}
