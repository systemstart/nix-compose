package k8s

import (
	"sort"
	"strconv"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// convertDeployment converts a named service to a K8s Deployment manifest.
func convertDeployment(name string, svc eval.Service, compVolumes map[string]eval.Volume, opts RenderOptions) Manifest {
	labels := standardLabels(name)
	containers := []Container{buildMainContainer(name, svc, opts)}
	podVolumes := convertPodVolumes(svc.Volumes, compVolumes)

	deploy := Deployment{
		TypeMeta: TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		Metadata: ObjectMeta{Name: name, Namespace: opts.Namespace, Labels: labels},
		Spec: DeploymentSpec{
			Replicas: 1,
			Selector: &LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": name}},
			Template: PodTemplateSpec{
				Metadata: ObjectMeta{Labels: labels},
				Spec: PodSpec{
					InitContainers: convertInitContainers(svc),
					Containers:     containers,
					Volumes:        podVolumes,
				},
			},
		},
	}
	return Manifest{Object: deploy, Filename: name + "-deployment.yaml"}
}

// buildMainContainer assembles the primary container for a Deployment.
func buildMainContainer(name string, svc eval.Service, opts RenderOptions) Container {
	c := Container{
		Name:       name,
		Image:      svc.Image,
		Command:    svc.Command.Parts,
		WorkingDir: svc.WorkingDir,
		Ports:      convertContainerPorts(svc),
		Env:        convertEnvVars(svc.Environment),
		Resources:  convertResources(svc),
	}
	setProbes(&c, svc)
	setEnvFrom(&c, name, svc, opts)
	c.VolumeMounts = convertVolumeMounts(svc.Volumes)
	return c
}

// standardLabels returns the standard K8s labels for a service.
func standardLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "nix-compose",
	}
}

// convertContainerPorts converts named ports or string ports to K8s container ports.
func convertContainerPorts(svc eval.Service) []ContainerPort {
	if svc.XNixCompose != nil && len(svc.XNixCompose.NamedPorts) > 0 {
		return convertNamedPorts(svc.XNixCompose.NamedPorts)
	}
	return convertStringPorts(svc.Ports)
}

// convertNamedPorts converts eval.NamedPort entries to K8s ContainerPort entries.
func convertNamedPorts(nps []eval.NamedPort) []ContainerPort {
	ports := make([]ContainerPort, 0, len(nps))
	for _, np := range nps {
		cp := ContainerPort{
			Name:          np.Name,
			ContainerPort: np.ContainerPort,
		}
		if np.Protocol != "" {
			cp.Protocol = strings.ToUpper(np.Protocol)
		}
		ports = append(ports, cp)
	}
	return ports
}

// convertStringPorts parses compose port strings like "8080:80" into ContainerPort entries.
func convertStringPorts(ports []string) []ContainerPort {
	result := make([]ContainerPort, 0, len(ports))
	for _, p := range ports {
		cp := parsePortString(p)
		if cp.ContainerPort > 0 {
			result = append(result, cp)
		}
	}
	return result
}

// parsePortString parses a single compose port string into a ContainerPort.
func parsePortString(portStr string) ContainerPort {
	// Strip protocol suffix.
	protocol := ""
	base := portStr
	if idx := strings.LastIndex(portStr, "/"); idx >= 0 {
		protocol = strings.ToUpper(portStr[idx+1:])
		base = portStr[:idx]
	}

	var containerPort int
	if parts := strings.SplitN(base, ":", 2); len(parts) == 2 {
		containerPort, _ = strconv.Atoi(parts[1])
	} else {
		containerPort, _ = strconv.Atoi(parts[0])
	}

	return ContainerPort{ContainerPort: containerPort, Protocol: protocol}
}

// convertEnvVars converts an env map to a sorted slice of EnvVar.
func convertEnvVars(env map[string]string) []EnvVar {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	vars := make([]EnvVar, 0, len(keys))
	for _, k := range keys {
		vars = append(vars, EnvVar{Name: k, Value: env[k]})
	}
	return vars
}

// convertResources converts eval resources to K8s ResourceRequirements.
func convertResources(svc eval.Service) *ResourceRequirements {
	if svc.XNixCompose == nil || svc.XNixCompose.Resources == nil {
		return nil
	}
	res := svc.XNixCompose.Resources
	rr := &ResourceRequirements{}
	if res.Limits != nil {
		rr.Limits = resourceSpecToMap(res.Limits)
	}
	if res.Requests != nil {
		rr.Requests = resourceSpecToMap(res.Requests)
	}
	return rr
}

// resourceSpecToMap converts a ResourceSpec to a string map for K8s.
func resourceSpecToMap(spec *eval.ResourceSpec) map[string]string {
	m := make(map[string]string)
	if spec.CPU != "" {
		m["cpu"] = spec.CPU
	}
	if spec.Memory != "" {
		m["memory"] = spec.Memory
	}
	return m
}

// setProbes sets liveness and readiness probes on a container.
func setProbes(c *Container, svc eval.Service) {
	if svc.XNixCompose == nil || svc.XNixCompose.Probes == nil {
		return
	}
	c.LivenessProbe = convertProbe(svc.XNixCompose.Probes.Liveness)
	c.ReadinessProbe = convertProbe(svc.XNixCompose.Probes.Readiness)
}

// convertProbe converts an eval.Probe to a K8s ProbeSpec.
func convertProbe(p *eval.Probe) *ProbeSpec {
	if p == nil {
		return nil
	}
	ps := &ProbeSpec{
		InitialDelaySeconds: p.InitialDelaySeconds,
		PeriodSeconds:       p.PeriodSeconds,
		TimeoutSeconds:      p.TimeoutSeconds,
		FailureThreshold:    p.FailureThreshold,
	}
	if p.Exec != nil {
		ps.Exec = &ExecAction{Command: p.Exec.Command}
	}
	if p.HTTPGet != nil {
		ps.HTTPGet = &HTTPGetAction{
			Path:   p.HTTPGet.Path,
			Port:   p.HTTPGet.Port,
			Scheme: p.HTTPGet.Scheme,
		}
	}
	return ps
}

// setEnvFrom configures secretRef-based envFrom on a container.
func setEnvFrom(c *Container, name string, svc eval.Service, _ RenderOptions) {
	if svc.XNixCompose == nil || len(svc.XNixCompose.EnvFrom) == 0 {
		return
	}
	c.EnvFrom = []EnvFromSource{
		{SecretRef: &SecretRef{Name: name + "-secrets"}},
	}
}

// convertInitContainers converts eval init containers to K8s native init containers.
func convertInitContainers(svc eval.Service) []Container {
	if svc.XNixCompose == nil || len(svc.XNixCompose.InitContainers) == 0 {
		return nil
	}
	inits := make([]Container, 0, len(svc.XNixCompose.InitContainers))
	for _, ic := range svc.XNixCompose.InitContainers {
		c := Container{
			Name:    ic.Name,
			Image:   ic.Image,
			Command: ic.Command.Parts,
			Env:     convertEnvVars(ic.Environment),
		}
		c.VolumeMounts = convertVolumeMounts(ic.Volumes)
		inits = append(inits, c)
	}
	return inits
}

// convertVolumeMounts parses compose volume strings into K8s VolumeMounts.
func convertVolumeMounts(volumes []string) []VolumeMount {
	if len(volumes) == 0 {
		return nil
	}
	mounts := make([]VolumeMount, 0, len(volumes))
	for _, vol := range volumes {
		source, dest, readOnly := parseVolumeString(vol)
		if dest == "" {
			continue
		}
		mounts = append(mounts, VolumeMount{
			Name:      sanitizeVolumeName(source),
			MountPath: dest,
			ReadOnly:  readOnly,
		})
	}
	return mounts
}

// convertPodVolumes converts compose volume strings to K8s pod volumes.
func convertPodVolumes(volumes []string, compVolumes map[string]eval.Volume) []PodVolume {
	if len(volumes) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	podVols := make([]PodVolume, 0, len(volumes))
	for _, vol := range volumes {
		source, _, _ := parseVolumeString(vol)
		volName := sanitizeVolumeName(source)
		if seen[volName] {
			continue
		}
		seen[volName] = true

		pv := PodVolume{Name: volName}
		if _, isNamed := compVolumes[source]; isNamed {
			pv.PersistentVolumeClaim = &PVCVolumeSource{ClaimName: source}
		} else {
			pv.EmptyDir = &EmptyDirVolumeSource{}
		}
		podVols = append(podVols, pv)
	}
	return podVols
}

// parseVolumeString parses a compose volume string "source:dest[:ro]".
func parseVolumeString(vol string) (source, dest string, readOnly bool) {
	parts := strings.SplitN(vol, ":", 3)
	switch len(parts) {
	case 1:
		return parts[0], parts[0], false
	case 2: //nolint:mnd // source:dest
		return parts[0], parts[1], false
	default:
		return parts[0], parts[1], parts[2] == "ro"
	}
}

// sanitizeVolumeName converts a path or volume name into a valid K8s volume name.
func sanitizeVolumeName(s string) string {
	// Named volumes are already valid; paths need sanitization.
	if !strings.Contains(s, "/") {
		return s
	}
	name := strings.ReplaceAll(strings.Trim(s, "/"), "/", "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}
