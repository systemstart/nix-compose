package k8s

import (
	"sort"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// Convert transforms a Composition and resolved secrets into K8s manifests.
// Output ordering is deterministic: Secrets, PVCs, then (Deployment + Service) per service name.
func Convert(comp *eval.Composition, resolvedSecrets map[string]map[string]string, opts RenderOptions) []Manifest {
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}

	var manifests []Manifest
	manifests = append(manifests, convertSecrets(comp, resolvedSecrets, opts)...)
	manifests = append(manifests, convertPVCs(comp, opts)...)
	manifests = append(manifests, convertWorkloads(comp, opts)...)
	return manifests
}

// convertSecrets produces Secret manifests for services with resolved envFrom.
func convertSecrets(comp *eval.Composition, resolvedSecrets map[string]map[string]string, opts RenderOptions) []Manifest {
	names := sortedServiceNames(comp)
	var manifests []Manifest
	for _, name := range names {
		if m := convertSecret(name, resolvedSecrets[name], opts); m != nil {
			manifests = append(manifests, *m)
		}
	}
	return manifests
}

// convertPVCs produces PVC manifests for named volumes.
func convertPVCs(comp *eval.Composition, opts RenderOptions) []Manifest {
	names := sortedVolumeNames(comp)
	manifests := make([]Manifest, 0, len(names))
	for _, name := range names {
		manifests = append(manifests, convertPVC(name, opts))
	}
	return manifests
}

// convertWorkloads produces Deployment/Job and optional Service manifests per service.
func convertWorkloads(comp *eval.Composition, opts RenderOptions) []Manifest {
	names := sortedServiceNames(comp)
	volumes := comp.Volumes
	if volumes == nil {
		volumes = make(map[string]eval.Volume)
	}

	var manifests []Manifest
	for _, name := range names {
		svc := comp.Services[name]
		if isJobService(svc) {
			manifests = append(manifests, convertJob(name, svc, volumes, opts))
		} else {
			manifests = append(manifests, convertDeployment(name, svc, volumes, opts))
			if m := convertK8sService(name, svc, opts); m != nil {
				manifests = append(manifests, *m)
			}
		}
	}
	return manifests
}

// isJobService returns true when the service should be rendered as a K8s Job.
func isJobService(svc eval.Service) bool {
	return svc.Restart == "no" || svc.Restart == "on-failure"
}

// sortedServiceNames returns service names in sorted order.
func sortedServiceNames(comp *eval.Composition) []string {
	names := make([]string, 0, len(comp.Services))
	for name := range comp.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedVolumeNames returns volume names in sorted order.
func sortedVolumeNames(comp *eval.Composition) []string {
	names := make([]string, 0, len(comp.Volumes))
	for name := range comp.Volumes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
