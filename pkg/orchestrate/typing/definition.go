package typing

import (
	"encoding/json"
	"strings"
)

const (
	DefinitionKeySeparator = "/"
)

// Definition describes a resource type and its lifecycle operations.
type Definition interface {
	GetKey() DefinitionKey
	GetMappings() []Mapping
	Instantiate(r json.RawMessage) (Instance, error)
	Load(r json.RawMessage) (Instance, error)
	Delete(r Reference) error
	GetStatus(r Reference) (Status, error)
	GetProviderStatus(r Reference) (Status, error)
}

// Defined is implemented by anything that carries a DefinitionKey.
type Defined interface {
	GetKey() DefinitionKey
}

// Mapping describes a provider-specific mapping field.
type Mapping interface {
	GetName() string
}

// DefinitionKey is a group/version/kind string identifying a resource type.
type DefinitionKey string

// GetGVK splits the key into Group, Version, Kind.
func (di DefinitionKey) GetGVK() *GVK {
	tmp := strings.Split(string(di), DefinitionKeySeparator)
	if len(tmp) != 3 {
		panic("bad id for GVK: " + di)
	}
	return &GVK{
		Group: tmp[0], Version: tmp[1], Kind: tmp[2],
	}
}

// CreateDefinitionKey joins path segments into a DefinitionKey.
func CreateDefinitionKey(path ...string) DefinitionKey {
	for _, p := range path {
		if strings.Contains(p, DefinitionKeySeparator) {
			panic("must not contain '/': " + DefinitionKeySeparator)
		}
	}
	return DefinitionKey(strings.Join(path, DefinitionKeySeparator))
}

// GVK represents a Group/Version/Kind triple.
type GVK struct {
	Group   string
	Version string
	Kind    string
}

// GetKey returns the DefinitionKey for this GVK.
func (g *GVK) GetKey() DefinitionKey {
	return CreateDefinitionKey(g.Group, g.Version, g.Kind)
}
