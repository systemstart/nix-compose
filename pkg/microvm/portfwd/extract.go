package portfwd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// ExtractPorts collects port mappings from a composition that should be
// forwarded from the host into the VM. It parses both standard compose
// port strings (svc.Ports) and the nix-compose NamedPort extension.
//
// Only TCP ports with a non-zero HostPort are included. Non-TCP
// protocols are logged as warnings and skipped.
func ExtractPorts(comp *eval.Composition) []PortMapping {
	if comp == nil {
		return nil
	}

	var mappings []PortMapping

	for name, svc := range comp.Services {
		// Standard compose port strings.
		for _, raw := range svc.Ports {
			pm, ok := parsePortString(raw, name)
			if !ok {
				continue
			}
			mappings = append(mappings, pm)
		}

		// x-nix-compose named ports.
		if svc.XNixCompose != nil {
			for _, np := range svc.XNixCompose.NamedPorts {
				pm, ok := fromNamedPort(np, name)
				if !ok {
					continue
				}
				mappings = append(mappings, pm)
			}
		}
	}

	return mappings
}

// parsePortString parses a compose port string into a PortMapping.
// Supported formats:
//
//	"80"             → HostPort=80,  VMPort=80
//	"8080:80"        → HostPort=8080, VMPort=8080
//	"127.0.0.1:8080:80" → HostIP=127.0.0.1, HostPort=8080, VMPort=8080
//	"8080:80/udp"    → skipped (non-TCP)
//
// VMPort is always set to HostPort because the CNI portmap plugin
// inside the VM maps HostPort → ContainerPort.
func parsePortString(raw, service string) (PortMapping, bool) {
	proto := "tcp"
	s := raw

	// Split off /protocol suffix.
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		proto = s[idx+1:]
		s = s[:idx]
	}

	if proto != "tcp" {
		fmt.Printf("Warning: skipping %s port %s (only TCP is supported for port forwarding)\n", proto, raw)
		return PortMapping{}, false
	}

	hostIP, hostPort, containerPort, ok := parsePortParts(strings.Split(s, ":"))
	if !ok || hostPort == 0 {
		return PortMapping{}, false
	}

	return PortMapping{
		HostIP:   hostIP,
		HostPort: hostPort,
		VMPort:   hostPort, // CNI portmap inside VM maps HostPort → ContainerPort
		Protocol: proto,
		Service:  fmt.Sprintf("%s (%d)", service, containerPort),
	}, true
}

// parsePortParts extracts host IP, host port, and container port from
// colon-split port string parts.
func parsePortParts(parts []string) (hostIP string, hostPort, containerPort uint16, ok bool) {
	switch len(parts) {
	case 1:
		p, err := parsePort(parts[0])
		if err != nil {
			return "", 0, 0, false
		}
		return "", p, p, true
	case 2:
		hp, err := parsePort(parts[0])
		if err != nil {
			return "", 0, 0, false
		}
		cp, err := parsePort(parts[1])
		if err != nil {
			return "", 0, 0, false
		}
		return "", hp, cp, true
	case 3:
		hp, err := parsePort(parts[1])
		if err != nil {
			return "", 0, 0, false
		}
		cp, err := parsePort(parts[2])
		if err != nil {
			return "", 0, 0, false
		}
		return parts[0], hp, cp, true
	default:
		return "", 0, 0, false
	}
}

// fromNamedPort converts an eval.NamedPort to a PortMapping.
func fromNamedPort(np eval.NamedPort, service string) (PortMapping, bool) {
	proto := np.Protocol
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" {
		fmt.Printf("Warning: skipping %s named port %s/%s (only TCP is supported for port forwarding)\n",
			proto, service, np.Name)
		return PortMapping{}, false
	}

	if np.HostPort <= 0 || np.HostPort > 65535 {
		return PortMapping{}, false
	}
	hostPort := uint16(np.HostPort) //nolint:gosec // bounds checked above

	return PortMapping{
		HostIP:   np.HostIP,
		HostPort: hostPort,
		VMPort:   hostPort,
		Protocol: proto,
		Service:  fmt.Sprintf("%s (%d)", service, np.ContainerPort),
	}, true
}

func parsePort(s string) (uint16, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", s, err)
	}
	return uint16(n), nil
}
