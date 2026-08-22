package cri

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ParseStopSignal converts a compose-style signal name (e.g. "SIGTERM", "SIGKILL",
// "SIGUSR1") to a CRI Signal enum value. The match is case-insensitive and the "SIG"
// prefix is optional. Returns RUNTIME_DEFAULT for empty or unrecognised strings.
func ParseStopSignal(s string) runtimev1.Signal {
	if s == "" {
		return runtimev1.Signal_RUNTIME_DEFAULT
	}
	upper := strings.ToUpper(s)
	if !strings.HasPrefix(upper, "SIG") {
		upper = "SIG" + upper
	}
	if v, ok := runtimev1.Signal_value[upper]; ok {
		return runtimev1.Signal(v)
	}
	return runtimev1.Signal_RUNTIME_DEFAULT
}

// VolumeResolver maps a (project, volume-name) to a host directory path.
// Used to decouple the convert layer from the volumes.Store.
type VolumeResolver func(project, name string) (string, error)

// PodNetworkMode controls whether a pod uses CNI networking or host networking.
type PodNetworkMode int

const (
	// PodNetworkCNI uses NamespaceMode_POD — each pod gets its own network namespace.
	PodNetworkCNI PodNetworkMode = iota
	// PodNetworkHost uses NamespaceMode_NODE — the pod shares the host network.
	PodNetworkHost
)

// Label constants used to tag CRI pods and containers.
const (
	LabelProject = "nix-compose.project"
	LabelService = "nix-compose.service"
	LabelVersion = "nix-compose.version"
)

// ServiceLabels returns the standard label set for a service.
func ServiceLabels(project, service, version string) map[string]string {
	return map[string]string{
		LabelProject: project,
		LabelService: service,
		LabelVersion: version,
	}
}

// ParsePort parses a compose-style port string into a CRI PortMapping.
// Supported formats:
//
//	"80"                   → container 80
//	"8080:80"              → host 8080, container 80
//	"8080:80/udp"          → host 8080, container 80, UDP
//	"127.0.0.1:8080:80"   → host IP 127.0.0.1, host 8080, container 80
func ParsePort(s string) (*runtimev1.PortMapping, error) {
	pm := &runtimev1.PortMapping{Protocol: runtimev1.Protocol_TCP}

	// Split off protocol suffix.
	proto, s := splitProto(s)
	p, err := parseProto(proto)
	if err != nil {
		return nil, err
	}
	pm.Protocol = p

	if err := parsePortParts(pm, strings.Split(s, ":")); err != nil {
		return nil, err
	}
	return pm, nil
}

func splitProto(s string) (string, string) {
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		return strings.ToLower(s[idx+1:]), s[:idx]
	}
	return "tcp", s
}

func parseProto(proto string) (runtimev1.Protocol, error) {
	switch proto {
	case "tcp":
		return runtimev1.Protocol_TCP, nil
	case "udp":
		return runtimev1.Protocol_UDP, nil
	case "sctp":
		return runtimev1.Protocol_SCTP, nil
	default:
		return 0, fmt.Errorf("unknown protocol %q", proto)
	}
}

func parsePortParts(pm *runtimev1.PortMapping, parts []string) error {
	switch len(parts) {
	case 1:
		p, err := strconv.ParseInt(parts[0], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid port %q: %w", parts[0], err)
		}
		pm.ContainerPort = int32(p)
	case 2:
		hp, err := strconv.ParseInt(parts[0], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid host port %q: %w", parts[0], err)
		}
		cp, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid container port %q: %w", parts[1], err)
		}
		pm.HostPort = int32(hp)
		pm.ContainerPort = int32(cp)
	case 3:
		pm.HostIp = parts[0]
		hp, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid host port %q: %w", parts[1], err)
		}
		cp, err := strconv.ParseInt(parts[2], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid container port %q: %w", parts[2], err)
		}
		pm.HostPort = int32(hp)
		pm.ContainerPort = int32(cp)
	default:
		return fmt.Errorf("invalid port format %q", strings.Join(parts, ":"))
	}
	return nil
}

// ParsePorts parses compose-style port strings and named ports into CRI PortMappings.
func ParsePorts(ports []string, namedPorts []eval.NamedPort) ([]*runtimev1.PortMapping, error) {
	var result []*runtimev1.PortMapping

	for _, s := range ports {
		pm, err := ParsePort(s)
		if err != nil {
			return nil, err
		}
		result = append(result, pm)
	}

	for _, np := range namedPorts {
		pm := &runtimev1.PortMapping{
			ContainerPort: clampPort(np.ContainerPort),
			Protocol:      runtimev1.Protocol_TCP,
		}
		if np.HostPort > 0 {
			pm.HostPort = clampPort(np.HostPort)
		}
		if np.HostIP != "" {
			pm.HostIp = np.HostIP
		}
		switch strings.ToLower(np.Protocol) {
		case "udp":
			pm.Protocol = runtimev1.Protocol_UDP
		case "sctp":
			pm.Protocol = runtimev1.Protocol_SCTP
		}
		result = append(result, pm)
	}

	return result, nil
}

// clampPort safely converts a port number to int32, clamping to the valid range.
func clampPort(p int) int32 {
	if p > 65535 {
		p = 65535
	}
	if p < 0 {
		p = 0
	}
	return int32(p) //nolint:gosec // clamped above
}

// EnvsToKeyValues converts an environment map to CRI KeyValue pairs.
func EnvsToKeyValues(env map[string]string) []*runtimev1.KeyValue {
	if len(env) == 0 {
		return nil
	}
	kvs := make([]*runtimev1.KeyValue, 0, len(env))
	for k, v := range env {
		kvs = append(kvs, &runtimev1.KeyValue{Key: k, Value: []byte(v)})
	}
	return kvs
}

// randomUID generates a short random UID for pod sandbox metadata.
func randomUID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// BuildPodConfig builds a PodSandboxConfig for a service.
func BuildPodConfig(project, service string, svc eval.Service, version string, networkMode PodNetworkMode) *runtimev1.PodSandboxConfig {
	labels := ServiceLabels(project, service, version)

	var namedPorts []eval.NamedPort
	if svc.XNixCompose != nil {
		namedPorts = svc.XNixCompose.NamedPorts
	}
	portMappings, _ := ParsePorts(svc.Ports, namedPorts)

	// A pod sharing the host's network shares its UTS namespace too, so it
	// cannot have a hostname of its own — runc rejects the attempt outright.
	// It already answers to the host's name.
	hostname := ""
	if networkMode != PodNetworkHost {
		hostname = svc.Hostname
		if hostname == "" {
			hostname = service
		}
	}

	logDir := fmt.Sprintf("/tmp/nix-compose-logs/%s/%s", project, service)

	nsMode := runtimev1.NamespaceMode_POD
	if networkMode == PodNetworkHost {
		nsMode = runtimev1.NamespaceMode_NODE
	}

	return &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{
			Name:      fmt.Sprintf("%s-%s", project, service),
			Uid:       randomUID(),
			Namespace: "nix-compose",
		},
		Hostname:     hostname,
		LogDirectory: logDir,
		Labels:       labels,
		PortMappings: portMappings,
		Linux: &runtimev1.LinuxPodSandboxConfig{
			SecurityContext: &runtimev1.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtimev1.NamespaceOption{
					Network: nsMode,
				},
			},
		},
	}
}

// isHostPath returns true if source looks like a host filesystem path.
func isHostPath(source string) bool {
	return strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

// isNamedVolume returns true if source refers to a named volume.
func isNamedVolume(source string, compVolumes map[string]eval.Volume) bool {
	if _, ok := compVolumes[source]; ok {
		return true
	}
	return !strings.Contains(source, "/")
}

// ParseVolumeMount parses a compose-style volume string into a CRI Mount.
// Formats: "source:dest", "source:dest:ro", "dest" (anonymous — returns nil).
func ParseVolumeMount(vol, project string, compVolumes map[string]eval.Volume, resolve VolumeResolver) (*runtimev1.Mount, error) {
	parts := strings.SplitN(vol, ":", 3)

	// Single part (just a container path) → anonymous volume, skip.
	if len(parts) == 1 {
		return nil, nil
	}

	source := parts[0]
	dest := parts[1]
	readonly := len(parts) == 3 && parts[2] == "ro"

	switch {
	case isHostPath(source):
		abs, err := filepath.Abs(source)
		if err != nil {
			return nil, fmt.Errorf("resolving path %q: %w", source, err)
		}
		return &runtimev1.Mount{ContainerPath: dest, HostPath: abs, Readonly: readonly}, nil

	case isNamedVolume(source, compVolumes):
		if resolve == nil {
			return nil, fmt.Errorf("no volume resolver for named volume %q", source)
		}
		hostPath, err := resolve(project, source)
		if err != nil {
			return nil, fmt.Errorf("resolving named volume %q: %w", source, err)
		}
		return &runtimev1.Mount{ContainerPath: dest, HostPath: hostPath, Readonly: readonly}, nil
	}

	return nil, nil
}

// BuildMounts converts service volumes, tmpfs, and nix store paths to CRI Mounts.
func BuildMounts(svc eval.Service, project string, compVolumes map[string]eval.Volume, resolve VolumeResolver) ([]*runtimev1.Mount, error) {
	var mounts []*runtimev1.Mount

	for _, vol := range svc.Volumes {
		m, err := ParseVolumeMount(vol, project, compVolumes, resolve)
		if err != nil {
			return nil, err
		}
		if m != nil {
			mounts = append(mounts, m)
		}
	}

	for _, path := range svc.Tmpfs {
		mounts = append(mounts, &runtimev1.Mount{
			ContainerPath: path,
			HostPath:      "tmpfs",
		})
	}

	if svc.XNixCompose != nil && svc.XNixCompose.UseHostStore {
		for _, p := range svc.XNixCompose.NixStorePaths {
			mounts = append(mounts, &runtimev1.Mount{
				ContainerPath: p,
				HostPath:      p,
				Readonly:      true,
			})
		}
	}

	return mounts, nil
}

// BuildContainerConfig builds a ContainerConfig for a service.
func BuildContainerConfig(service string, svc eval.Service, project, version string, mounts []*runtimev1.Mount) *runtimev1.ContainerConfig {
	labels := ServiceLabels(project, service, version)

	cfg := &runtimev1.ContainerConfig{
		Metadata: &runtimev1.ContainerMetadata{
			Name: service,
		},
		Image: &runtimev1.ImageSpec{
			Image: svc.Image,
		},
		Labels:     labels,
		LogPath:    "0.log",
		WorkingDir: svc.WorkingDir,
		Envs:       EnvsToKeyValues(svc.Environment),
		Mounts:     mounts,
	}

	// Entrypoint → Command, Command → Args (matching Docker/OCI semantics).
	if !svc.Entrypoint.IsEmpty() {
		cfg.Command = svc.Entrypoint.Parts
	}
	if !svc.Command.IsEmpty() {
		cfg.Args = svc.Command.Parts
	}

	// Stop signal.
	if sig := ParseStopSignal(svc.StopSignal); sig != runtimev1.Signal_RUNTIME_DEFAULT {
		cfg.StopSignal = sig
	}

	// Linux security context.
	//
	// Pid must be set explicitly. NamespaceMode_POD is the zero value, so
	// leaving NamespaceOptions nil silently opts every container into the
	// sandbox's PID namespace, where the pause process is PID 1 and the
	// service's entrypoint is not. Compose gives each service its own PID
	// namespace, and images built on s6-overlay (paperless-ngx, the whole
	// linuxserver.io family) refuse to start when they are not PID 1:
	//
	//     s6-overlay-suexec: fatal: can only run as pid 1
	linuxCfg := &runtimev1.LinuxContainerConfig{
		SecurityContext: &runtimev1.LinuxContainerSecurityContext{
			NamespaceOptions: &runtimev1.NamespaceOption{
				Pid: runtimev1.NamespaceMode_CONTAINER,
			},
		},
	}
	if svc.User != "" {
		applyUser(linuxCfg.SecurityContext, svc.User)
	}
	if svc.Privileged {
		linuxCfg.SecurityContext.Privileged = true
	}
	cfg.Linux = linuxCfg

	return cfg
}

// applyUser fills the security context from a compose `user:` value, which is
// `<user>` or `<user>:<group>`, and where either side may be a name or a
// numeric id.
//
// CRI splits what compose keeps in one string: a numeric uid goes to
// RunAsUser, a name to RunAsUsername (the runtime resolves it against the
// image's /etc/passwd), and the two are mutually exclusive. A group is only
// legal alongside one of them — the spec says the runtime MUST error on a bare
// RunAsGroup — so a malformed left-hand side drops the group with it.
//
// Anything unparseable is left unset rather than guessed, which means the
// container runs as the image's default user.
func applyUser(sc *runtimev1.LinuxContainerSecurityContext, user string) {
	name, group, hasGroup := strings.Cut(user, ":")

	if name == "" {
		// ":gid" or ":" — no user to attach a group to.
		return
	}
	if uid, err := strconv.ParseInt(name, 10, 64); err == nil {
		sc.RunAsUser = &runtimev1.Int64Value{Value: uid}
	} else {
		sc.RunAsUsername = name
	}

	if hasGroup && group != "" {
		// RunAsGroup is numeric-only in CRI; there is no RunAsGroupname to
		// fall back to, so a named group cannot be honoured here.
		if gid, err := strconv.ParseInt(group, 10, 64); err == nil {
			sc.RunAsGroup = &runtimev1.Int64Value{Value: gid}
		}
	}
}
