package k8s

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nix-gizmos/nix-compose/pkg/eval"
)

// MountRefusalError reports every bind mount a render refused, so one run
// names all of them rather than one per run. Refusal has two causes — a mount
// with no Kubernetes equivalent, and one whose content is secret material —
// and Overrides carries the flag that would render each kind anyway.
type MountRefusalError struct {
	Refusals  []error
	Overrides []string
	// MustFix reports that at least one refusal has no override, so passing
	// every flag in Overrides still will not render the composition.
	MustFix bool
}

// UnrepresentableMountError is the previous name of MountRefusalError, kept
// as an alias so existing errors.As targets keep compiling.
//
// Deprecated: use MountRefusalError.
type UnrepresentableMountError = MountRefusalError

func (e *MountRefusalError) Error() string {
	var b strings.Builder
	if len(e.Refusals) == 1 {
		b.WriteString("1 bind mount cannot be rendered:")
	} else {
		fmt.Fprintf(&b, "%d bind mounts cannot be rendered:", len(e.Refusals))
	}
	for _, r := range e.Refusals {
		b.WriteString("\n  - ")
		b.WriteString(r.Error())
	}
	return b.String()
}

// Unwrap exposes the individual refusals to errors.Is and errors.As.
func (e *MountRefusalError) Unwrap() []error { return e.Refusals }

// Convert transforms a Composition and resolved secrets into K8s manifests.
// Output ordering is deterministic: Secrets, ConfigMaps, PVCs, then
// (Deployment + Service) per service name.
//
// It fails when a service bind-mounts a host path that has no K8s
// equivalent, unless opts.UnrepresentableMounts says otherwise.
func Convert(comp *eval.Composition, resolvedSecrets map[string]map[string]string, opts RenderOptions) (*Result, error) {
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.ProjectDir == "" {
		opts.ProjectDir = "."
	}

	plans, warnings, err := planCompositionVolumes(comp, opts)
	if err != nil {
		return nil, err
	}

	var manifests []Manifest
	manifests = append(manifests, convertSecrets(comp, resolvedSecrets, opts)...)
	manifests = append(manifests, convertConfigMaps(comp, plans)...)
	manifests = append(manifests, convertPVCs(comp, opts)...)
	manifests = append(manifests, convertWorkloads(comp, plans, opts)...)
	return &Result{Manifests: manifests, Warnings: warnings}, nil
}

// planCompositionVolumes resolves the volumes of every service up front, so
// a service that cannot be represented fails the render before any manifest
// is written.
func planCompositionVolumes(comp *eval.Composition, opts RenderOptions) (map[string]*volumePlan, []string, error) {
	volumes := comp.Volumes
	if volumes == nil {
		volumes = make(map[string]eval.Volume)
	}

	plans := make(map[string]*volumePlan, len(comp.Services))
	var warnings []string
	var refusals []error
	var overrides []string
	mustFix := false
	for _, name := range sortedServiceNames(comp) {
		plan := planVolumes(name, comp.Services[name], volumes, opts)
		plans[name] = plan
		warnings = append(warnings, plan.warnings...)
		refusals = append(refusals, plan.refusals...)
		overrides = mergeOverrides(overrides, plan.overrides)
		mustFix = mustFix || plan.mustFix
	}
	if shadowed := shadowedConfigMaps(comp, plans); len(shadowed) > 0 {
		refusals = append(refusals, shadowed...)
		mustFix = true
	}
	if len(refusals) > 0 {
		return nil, nil, &MountRefusalError{Refusals: refusals, Overrides: overrides, MustFix: mustFix}
	}
	return plans, warnings, nil
}

// shadowedConfigMaps refuses a generated ConfigMap that has the name of one
// a configMapMounts entry says the cluster supplies, in any service: applying
// the rendered output would replace the maintained ConfigMap with a copy of
// a project file.
func shadowedConfigMaps(comp *eval.Composition, plans map[string]*volumePlan) []error {
	supplied := make(map[string]string) // ConfigMap name -> service referencing it
	names := sortedServiceNames(comp)
	for _, name := range names {
		for source, cm := range plans[name].suppliedConfigMaps {
			if plans[name].usedConfigMaps[source] && cm.ConfigMapName != "" {
				if _, seen := supplied[cm.ConfigMapName]; !seen {
					supplied[cm.ConfigMapName] = name
				}
			}
		}
	}
	var refusals []error
	for _, name := range names {
		for _, generated := range plans[name].configMaps {
			if ref, clash := supplied[generated.Metadata.Name]; clash {
				refusals = append(refusals, fmt.Errorf("service %q: generates ConfigMap %q, which service %q "+
					"references through x-nix-compose.configMapMounts as one the cluster supplies — applying the "+
					"output would replace it; declare that mount in configMapMounts too, or rename one of them",
					name, generated.Metadata.Name, ref))
			}
		}
	}
	return refusals
}

// mergeOverrides appends the overrides not already present, keeping the
// order in which they were first reported.
func mergeOverrides(into, from []string) []string {
	for _, o := range from {
		if !slices.Contains(into, o) {
			into = append(into, o)
		}
	}
	return into
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

// convertConfigMaps produces the ConfigMap manifests generated for
// bind-mounted files, in service order.
func convertConfigMaps(comp *eval.Composition, plans map[string]*volumePlan) []Manifest {
	var manifests []Manifest
	for _, name := range sortedServiceNames(comp) {
		for _, cm := range plans[name].configMaps {
			manifests = append(manifests, Manifest{Object: cm, Filename: cm.Metadata.Name + "-configmap.yaml"})
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
func convertWorkloads(comp *eval.Composition, plans map[string]*volumePlan, opts RenderOptions) []Manifest {
	names := sortedServiceNames(comp)

	var manifests []Manifest
	for _, name := range names {
		svc := comp.Services[name]
		if isJobService(svc) {
			manifests = append(manifests, convertJob(name, svc, plans[name], opts))
		} else {
			manifests = append(manifests, convertDeployment(name, svc, plans[name], opts))
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
