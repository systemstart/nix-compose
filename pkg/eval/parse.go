package eval

import (
	"encoding/json"
	"fmt"
)

// Composition represents the top-level compose structure
// produced by nix eval of a nix-compose project.
type Composition struct {
	Services map[string]Service `json:"services"`
	Networks map[string]Network `json:"networks,omitempty"`
	Volumes  map[string]Volume  `json:"volumes,omitempty"`
	MicroVM  *MicroVMConfig     `json:"x-nix-compose-microvm,omitempty"`
}

// MicroVMConfig holds microVM parameters from Nix evaluation.
type MicroVMConfig struct {
	Kernel   string `json:"kernel,omitempty"`
	RootFS   string `json:"rootfs,omitempty"`
	VCPUs    int    `json:"vcpus,omitempty"`
	MemoryMB int    `json:"memoryMB,omitempty"`
	CID      uint32 `json:"cid,omitempty"`
}

// Service represents a single service definition.
type Service struct {
	Image       string              `json:"image,omitempty"`
	Build       *BuildConfig        `json:"build,omitempty"`
	Command     CommandValue        `json:"command,omitempty"`
	Ports       []string            `json:"ports,omitempty"`
	Environment map[string]string   `json:"environment,omitempty"`
	Volumes     []string            `json:"volumes,omitempty"`
	Tmpfs       []string            `json:"tmpfs,omitempty"`
	DependsOn   DependsOnValue      `json:"depends_on,omitempty"`
	Healthcheck *Healthcheck        `json:"healthcheck,omitempty"`
	Restart     string              `json:"restart,omitempty"`
	WorkingDir  string              `json:"working_dir,omitempty"`
	Entrypoint  CommandValue        `json:"entrypoint,omitempty"`
	User        string              `json:"user,omitempty"`
	Privileged  bool                `json:"privileged,omitempty"`
	Hostname    string              `json:"hostname,omitempty"`
	NetworkMode string              `json:"network_mode,omitempty"`
	Networks    ServiceNetworks     `json:"networks,omitempty"`
	ExtraHosts  []string            `json:"extra_hosts,omitempty"`
	Profiles    []string            `json:"profiles,omitempty"`
	Labels      map[string]string   `json:"labels,omitempty"`
	StopSignal  string              `json:"stop_signal,omitempty"`
	XNixCompose *NixComposeExtended `json:"x-nix-compose,omitempty"`
}

// BuildConfig represents a service build configuration.
type BuildConfig struct {
	Context    string            `json:"context,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
}

// ServiceNetworks handles both list-form and map-form per-service networks.
type ServiceNetworks struct {
	Entries map[string]ServiceNetworkConfig
}

// ServiceNetworkConfig holds per-network settings for a service.
type ServiceNetworkConfig struct {
	Aliases []string `json:"aliases,omitempty"`
}

func (n ServiceNetworks) MarshalJSON() ([]byte, error) {
	if len(n.Entries) == 0 {
		return []byte("null"), nil
	}
	data, err := json.Marshal(n.Entries)
	if err != nil {
		return nil, fmt.Errorf("marshaling service networks: %w", err)
	}
	return data, nil
}

func (n *ServiceNetworks) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	// Try as map form: {"net": {"aliases": [...]}}
	var m map[string]ServiceNetworkConfig
	if err := json.Unmarshal(data, &m); err == nil {
		n.Entries = m
		return nil
	}

	// Try as list form: ["net1", "net2"]
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		n.Entries = make(map[string]ServiceNetworkConfig, len(arr))
		for _, name := range arr {
			n.Entries[name] = ServiceNetworkConfig{}
		}
		return nil
	}

	return fmt.Errorf("networks: expected map or []string, got %s", string(data))
}

// IsEmpty returns true if no network entries are set.
func (n ServiceNetworks) IsEmpty() bool {
	return len(n.Entries) == 0
}

// Healthcheck represents a service health check configuration.
type Healthcheck struct {
	Test        CommandValue `json:"test"`
	Interval    string       `json:"interval,omitempty"`
	Timeout     string       `json:"timeout,omitempty"`
	Retries     int          `json:"retries,omitempty"`
	StartPeriod string       `json:"start_period,omitempty"`
}

// Volume represents a named volume definition.
type Volume struct {
	Driver     string            `json:"driver,omitempty"`
	DriverOpts map[string]string `json:"driver_opts,omitempty"`
	External   bool              `json:"external,omitempty"`
}

// Network represents a network definition.
type Network struct {
	Name     string `json:"name,omitempty"`
	Driver   string `json:"driver,omitempty"`
	External bool   `json:"external,omitempty"`
}

// NixComposeExtended holds the x-nix-compose extension fields.
type NixComposeExtended struct {
	ServiceInfo    *ServiceInfo    `json:"serviceInfo,omitempty"`
	UseHostStore   bool            `json:"useHostStore,omitempty"`
	NixStorePaths  []string        `json:"nixStorePaths,omitempty"`
	Profiles       []string        `json:"profiles,omitempty"`
	Resources      *Resources      `json:"resources,omitempty"`
	Probes         *Probes         `json:"probes,omitempty"`
	EnvFrom        []EnvFromSource `json:"envFrom,omitempty"`
	InitContainers []InitContainer `json:"initContainers,omitempty"`
	NamedPorts     []NamedPort     `json:"namedPorts,omitempty"`
	// ImageDrv is the derivation that produces this service's image, set by
	// mkComposition when the service names a package instead of a registry
	// tag. Evaluation yields the image's store path without building it, so
	// this is what RealiseImages hands to Nix to produce the bytes.
	ImageDrv string `json:"imageDrv,omitempty"`
}

// ServiceInfo holds nix-compose service metadata.
type ServiceInfo struct {
	DefaultExec []string `json:"defaultExec,omitempty"`
}

// ResourceSpec represents CPU and memory resource specifications.
type ResourceSpec struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// Resources represents Kubernetes-style resource requests and limits.
type Resources struct {
	Limits   *ResourceSpec `json:"limits,omitempty"`
	Requests *ResourceSpec `json:"requests,omitempty"`
}

// ProbeHTTPGet represents an HTTP GET probe action.
type ProbeHTTPGet struct {
	Path   string `json:"path"`
	Port   int    `json:"port"`
	Scheme string `json:"scheme,omitempty"`
}

// ProbeExec represents an exec probe action.
type ProbeExec struct {
	Command []string `json:"command"`
}

// Probe represents a Kubernetes-style probe configuration.
type Probe struct {
	HTTPGet             *ProbeHTTPGet `json:"httpGet,omitempty"`
	Exec                *ProbeExec    `json:"exec,omitempty"`
	InitialDelaySeconds int           `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int           `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int           `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int           `json:"failureThreshold,omitempty"`
}

// Probes holds liveness and readiness probe configurations.
type Probes struct {
	Liveness  *Probe `json:"liveness,omitempty"`
	Readiness *Probe `json:"readiness,omitempty"`
}

// EnvFromSource represents a source for environment variables.
type EnvFromSource struct {
	SecretFile string `json:"secretFile,omitempty"`
	SopsFile   string `json:"sopsFile,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
}

// InitContainer represents an init container that runs before the main service.
type InitContainer struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Command     CommandValue      `json:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Volumes     []string          `json:"volumes,omitempty"`
}

// NamedPort represents a named port mapping with protocol support.
type NamedPort struct {
	Name          string `json:"name"`
	ContainerPort int    `json:"containerPort"`
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      int    `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

// CommandValue handles both string and []string for command/entrypoint fields.
type CommandValue struct {
	Parts []string
}

func (c CommandValue) MarshalJSON() ([]byte, error) {
	if len(c.Parts) == 0 {
		return []byte("null"), nil
	}
	data, err := json.Marshal(c.Parts)
	if err != nil {
		return nil, fmt.Errorf("marshaling command parts: %w", err)
	}
	return data, nil
}

func (c *CommandValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	// Try as string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.Parts = []string{s}
		return nil
	}

	// Try as []string.
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		c.Parts = arr
		return nil
	}

	return fmt.Errorf("command: expected string or []string, got %s", string(data))
}

// IsEmpty returns true if no command parts are set.
func (c CommandValue) IsEmpty() bool {
	return len(c.Parts) == 0
}

// DependsOnEntry represents a single depends_on entry with a condition.
type DependsOnEntry struct {
	Condition string `json:"condition,omitempty"`
}

// DependsOnValue handles both list-form and map-form depends_on.
type DependsOnValue struct {
	Entries map[string]DependsOnEntry
}

func (d DependsOnValue) MarshalJSON() ([]byte, error) {
	if len(d.Entries) == 0 {
		return []byte("null"), nil
	}
	data, err := json.Marshal(d.Entries)
	if err != nil {
		return nil, fmt.Errorf("marshaling depends_on entries: %w", err)
	}
	return data, nil
}

func (d *DependsOnValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	// Try as map form: {"svc": {"condition": "..."}}
	var m map[string]DependsOnEntry
	if err := json.Unmarshal(data, &m); err == nil {
		d.Entries = m
		return nil
	}

	// Try as list form: ["svc1", "svc2"]
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		d.Entries = make(map[string]DependsOnEntry, len(arr))
		for _, name := range arr {
			d.Entries[name] = DependsOnEntry{Condition: "service_started"}
		}
		return nil
	}

	return fmt.Errorf("depends_on: expected map or []string, got %s", string(data))
}

// IsEmpty returns true if no depends_on entries are set.
func (d DependsOnValue) IsEmpty() bool {
	return len(d.Entries) == 0
}

// ParseComposition parses JSON bytes into a Composition.
func ParseComposition(data []byte) (*Composition, error) {
	var comp Composition
	if err := json.Unmarshal(data, &comp); err != nil {
		return nil, fmt.Errorf("parsing composition: %w", err)
	}
	return &comp, nil
}
