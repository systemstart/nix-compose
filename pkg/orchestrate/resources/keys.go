package resources

import "github.com/systemstart/nix-compose/pkg/orchestrate/typing"

const (
	group   = "cri.orchestrator.io"
	version = "v1"
)

var (
	ImageKey     = typing.CreateDefinitionKey(group, version, "Image")
	NetworkKey   = typing.CreateDefinitionKey(group, version, "Network")
	VolumeKey    = typing.CreateDefinitionKey(group, version, "Volume")
	ContainerKey = typing.CreateDefinitionKey(group, version, "Container")
	ServiceKey   = typing.CreateDefinitionKey(group, version, "Service")
	ProjectKey   = typing.CreateDefinitionKey(group, version, "Project")
)
