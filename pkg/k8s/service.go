package k8s

import (
	"strconv"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// convertK8sService converts a named service to a K8s Service manifest.
// Returns nil if the service exposes no ports.
func convertK8sService(name string, svc eval.Service, opts RenderOptions) *Manifest {
	ports := buildServicePorts(svc)
	if len(ports) == 0 {
		return nil
	}

	k8sSvc := K8sService{
		TypeMeta: TypeMeta{APIVersion: "v1", Kind: "Service"},
		Metadata: ObjectMeta{
			Name:      name,
			Namespace: opts.Namespace,
			Labels:    standardLabels(name),
		},
		Spec: ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": name},
			Ports:    ports,
		},
	}
	return &Manifest{Object: k8sSvc, Filename: name + "-service.yaml"}
}

// buildServicePorts assembles K8s ServicePorts from named or string ports.
func buildServicePorts(svc eval.Service) []ServicePort {
	if svc.XNixCompose != nil && len(svc.XNixCompose.NamedPorts) > 0 {
		return namedToServicePorts(svc.XNixCompose.NamedPorts)
	}
	return stringToServicePorts(svc.Ports)
}

// namedToServicePorts converts eval.NamedPort entries to K8s ServicePort entries.
func namedToServicePorts(nps []eval.NamedPort) []ServicePort {
	ports := make([]ServicePort, 0, len(nps))
	for _, np := range nps {
		sp := ServicePort{
			Name:       np.Name,
			Port:       np.ContainerPort,
			TargetPort: np.Name,
		}
		if np.Protocol != "" {
			sp.Protocol = strings.ToUpper(np.Protocol)
		}
		ports = append(ports, sp)
	}
	return ports
}

// stringToServicePorts parses compose port strings into K8s ServicePort entries.
func stringToServicePorts(ports []string) []ServicePort {
	result := make([]ServicePort, 0, len(ports))
	for _, p := range ports {
		sp := parseServicePort(p)
		if sp.Port > 0 {
			result = append(result, sp)
		}
	}
	return result
}

// parseServicePort parses a single compose port string into a ServicePort.
func parseServicePort(portStr string) ServicePort {
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

	return ServicePort{
		Port:       containerPort,
		TargetPort: strconv.Itoa(containerPort),
		Protocol:   protocol,
	}
}
