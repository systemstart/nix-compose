package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/manifest"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

const apiVersion = "cri.orchestrator.io/v1"

// Options configures the conversion.
type Options struct {
	Project string // required
	UseCNI  bool   // whether CNI networking is available
}

// Result holds the conversion output.
type Result struct {
	Manifests []manifest.Manifest
	Edges     []Edge
}

// Edge represents a dependency between two resources.
type Edge struct {
	From      typing.Reference
	To        typing.Reference
	Condition string // "started", "healthy", "completed", "" (= started)
}

// Convert transforms an eval.Composition into orchestrate manifests and dependency edges.
func Convert(comp *eval.Composition, opts Options) (*Result, error) {
	if opts.Project == "" {
		return nil, fmt.Errorf("project name is required")
	}

	var manifests []manifest.Manifest
	var edges []Edge

	// 1. Project manifest (always present).
	projectRef := typing.NewReference(opts.Project, resources.ProjectKey)
	manifests = append(manifests, manifest.Manifest{
		APIVersion: apiVersion,
		Kind:       "Project",
		Metadata:   manifest.Metadata{Name: opts.Project},
		Spec:       resources.ProjectSpec{Name: opts.Project},
	})

	// 2. Network manifest.
	networkRef := convertNetwork(comp, opts, &manifests)

	// 3. Image manifests.
	serviceNames := sortedKeys(comp.Services)
	imageRefs := convertImages(comp, serviceNames, &manifests)

	// 4. Volume manifests.
	volumeRefs := convertVolumes(comp, opts, &manifests)

	// 5. Service manifests and edges.
	for _, name := range serviceNames {
		svc := comp.Services[name]
		svcManifest, svcEdges := convertService(name, svc, comp, opts, projectRef, networkRef, imageRefs, volumeRefs)
		manifests = append(manifests, svcManifest)
		edges = append(edges, svcEdges...)
	}

	return &Result{Manifests: manifests, Edges: edges}, nil
}

// convertNetwork adds a Network manifest if any service needs it, returning the reference.
func convertNetwork(comp *eval.Composition, opts Options, manifests *[]manifest.Manifest) typing.Reference {
	for _, svc := range comp.Services {
		if svc.NetworkMode != "host" {
			ref := typing.NewReference(opts.Project, resources.NetworkKey)
			*manifests = append(*manifests, manifest.Manifest{
				APIVersion: apiVersion,
				Kind:       "Network",
				Metadata:   manifest.Metadata{Name: opts.Project},
				Spec:       resources.NetworkSpec{Project: opts.Project},
			})
			return ref
		}
	}
	return nil
}

// convertImages builds Image manifests for unique images across services.
func convertImages(comp *eval.Composition, serviceNames []string, manifests *[]manifest.Manifest) map[string]typing.Reference {
	imageSet := make(map[string]bool)
	for _, name := range serviceNames {
		if img := comp.Services[name].Image; img != "" {
			imageSet[img] = true
		}
	}

	imageNames := make([]string, 0, len(imageSet))
	for img := range imageSet {
		imageNames = append(imageNames, img)
	}
	sort.Strings(imageNames)

	imageRefs := make(map[string]typing.Reference, len(imageNames))
	for _, img := range imageNames {
		// Resources are keyed by the reference the runtime knows, which for a
		// local artifact is not the store path the service declared. The spec
		// keeps the declared value — that is what gets imported.
		id := cri.ResolvedImageRef(img)
		imageRefs[img] = typing.NewReference(id, resources.ImageKey)
		*manifests = append(*manifests, manifest.Manifest{
			APIVersion: apiVersion,
			Kind:       "Image",
			Metadata:   manifest.Metadata{Name: id},
			Spec:       resources.ImageSpec{Image: img},
		})
	}
	return imageRefs
}

// convertVolumes builds Volume manifests for named volumes.
func convertVolumes(comp *eval.Composition, opts Options, manifests *[]manifest.Manifest) map[string]typing.Reference {
	volumeNames := sortedKeys(comp.Volumes)
	volumeRefs := make(map[string]typing.Reference, len(volumeNames))
	for _, name := range volumeNames {
		id := fmt.Sprintf("%s/%s", opts.Project, name)
		volumeRefs[name] = typing.NewReference(id, resources.VolumeKey)
		*manifests = append(*manifests, manifest.Manifest{
			APIVersion: apiVersion,
			Kind:       "Volume",
			Metadata:   manifest.Metadata{Name: id},
			Spec:       resources.VolumeSpec{Project: opts.Project, Name: name},
		})
	}
	return volumeRefs
}

// convertService builds a Service manifest and its dependency edges.
func convertService(
	name string, svc eval.Service, comp *eval.Composition, opts Options,
	projectRef, networkRef typing.Reference,
	imageRefs, volumeRefs map[string]typing.Reference,
) (manifest.Manifest, []Edge) {
	svcID := fmt.Sprintf("%s/%s", opts.Project, name)
	svcRef := typing.NewReference(svcID, resources.ServiceKey)

	cs := buildContainerSpec(name, svc, comp, opts)
	m := manifest.Manifest{
		APIVersion: apiVersion,
		Kind:       "Service",
		Metadata:   manifest.Metadata{Name: svcID},
		Spec:       resources.ServiceSpec{Container: cs},
	}

	edges := buildServiceEdges(svc, svcRef, opts, projectRef, networkRef, imageRefs, volumeRefs)
	return m, edges
}

// buildContainerSpec constructs a ContainerSpec from service configuration.
func buildContainerSpec(name string, svc eval.Service, comp *eval.Composition, opts Options) resources.ContainerSpec {
	ports := buildPorts(svc)
	vols := buildVolumes(svc)

	return resources.ContainerSpec{
		Project:     opts.Project,
		Service:     name,
		Version:     computeVersion(svc),
		Image:       svc.Image,
		Command:     svc.Command,
		Entrypoint:  svc.Entrypoint,
		Environment: svc.Environment,
		Ports:       ports,
		Volumes:     vols,
		CompVolumes: comp.Volumes,
		NetworkMode: svc.NetworkMode,
		WorkingDir:  svc.WorkingDir,
		User:        svc.User,
		Privileged:  svc.Privileged,
		Tmpfs:       svc.Tmpfs,
		StopSignal:  svc.StopSignal,
		Hostname:    svc.Hostname,
		UseCNI:      opts.UseCNI && svc.NetworkMode != "host",
		Healthcheck: svc.Healthcheck,
	}
}

// buildPorts constructs the ports list from service ports and named ports.
func buildPorts(svc eval.Service) []string {
	ports := append([]string{}, svc.Ports...)
	if svc.XNixCompose != nil {
		for _, np := range svc.XNixCompose.NamedPorts {
			ports = append(ports, formatNamedPort(np))
		}
	}
	if len(ports) == 0 {
		return nil
	}
	return ports
}

// buildVolumes constructs the volumes list from service volumes and nix store bind mounts.
func buildVolumes(svc eval.Service) []string {
	vols := append([]string{}, svc.Volumes...)
	if svc.XNixCompose != nil && svc.XNixCompose.UseHostStore {
		for _, p := range svc.XNixCompose.NixStorePaths {
			vols = append(vols, fmt.Sprintf("%s:%s:ro", p, p))
		}
	}
	if len(vols) == 0 {
		return nil
	}
	return vols
}

// buildServiceEdges constructs all dependency edges for a service.
func buildServiceEdges(
	svc eval.Service, svcRef typing.Reference, opts Options,
	projectRef, networkRef typing.Reference,
	imageRefs, volumeRefs map[string]typing.Reference,
) []Edge {
	var edges []Edge

	edges = append(edges, Edge{From: svcRef, To: projectRef})

	if svc.Image != "" {
		if ref, ok := imageRefs[svc.Image]; ok {
			edges = append(edges, Edge{From: svcRef, To: ref})
		}
	}

	if networkRef != nil && svc.NetworkMode != "host" {
		edges = append(edges, Edge{From: svcRef, To: networkRef})
	}

	for _, volMount := range svc.Volumes {
		if volName := extractVolumeName(volMount); volName != "" {
			if ref, ok := volumeRefs[volName]; ok {
				edges = append(edges, Edge{From: svcRef, To: ref})
			}
		}
	}

	if !svc.DependsOn.IsEmpty() {
		depNames := sortedKeys(svc.DependsOn.Entries)
		for _, depName := range depNames {
			dep := svc.DependsOn.Entries[depName]
			depID := fmt.Sprintf("%s/%s", opts.Project, depName)
			depRef := typing.NewReference(depID, resources.ServiceKey)
			edges = append(edges, Edge{
				From:      svcRef,
				To:        depRef,
				Condition: mapCondition(dep.Condition),
			})
		}
	}

	return edges
}

// computeVersion computes a deterministic version hash for a service config.
// DependsOn and Profiles are zeroed out since they affect ordering/filtering, not runtime.
func computeVersion(svc eval.Service) string {
	canonical := svc
	canonical.DependsOn = eval.DependsOnValue{}
	canonical.Profiles = nil
	data, _ := json.Marshal(canonical)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// extractVolumeName returns the named volume from a mount string, or "" for bind mounts.
// Mount format: source:dest[:options]. Bind mounts start with /, ./, ~, or ..
func extractVolumeName(mount string) string {
	parts := strings.SplitN(mount, ":", 2)
	if len(parts) == 0 {
		return ""
	}
	source := parts[0]
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "~") || strings.HasPrefix(source, "..") {
		return ""
	}
	return source
}

// mapCondition maps Docker Compose condition strings to orchestrator conditions.
func mapCondition(condition string) string {
	switch condition {
	case "service_started":
		return "started"
	case "service_healthy":
		return "healthy"
	case "service_completed_successfully":
		return "completed"
	default:
		return ""
	}
}

// formatNamedPort converts a NamedPort to a Docker-style port string.
func formatNamedPort(np eval.NamedPort) string {
	proto := ""
	if np.Protocol != "" && np.Protocol != "tcp" {
		proto = "/" + np.Protocol
	}
	if np.HostPort == 0 {
		return fmt.Sprintf("%d%s", np.ContainerPort, proto)
	}
	if np.HostIP != "" {
		return fmt.Sprintf("%s:%d:%d%s", np.HostIP, np.HostPort, np.ContainerPort, proto)
	}
	return fmt.Sprintf("%d:%d%s", np.HostPort, np.ContainerPort, proto)
}

// sortedKeys returns the sorted keys of a string-keyed map.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
