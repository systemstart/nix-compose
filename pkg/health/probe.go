package health

import (
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// ProbeType identifies the kind of health probe.
type ProbeType int

const (
	// ProbeExec runs a command in the container and checks exit code.
	ProbeExec ProbeType = iota
	// ProbeHTTP sends an HTTP GET to the pod IP and checks for 2xx/3xx.
	ProbeHTTP
	// ProbeTCP dials a TCP port on the pod IP.
	ProbeTCP
)

// Default probe timing values.
const (
	DefaultInterval         = 10 * time.Second
	DefaultTimeout          = 5 * time.Second
	DefaultInitialDelay     = 0
	DefaultFailureThreshold = 3
)

// ProbeConfig holds the resolved configuration for a health probe.
type ProbeConfig struct {
	Type             ProbeType
	ExecCommand      []string
	HTTPScheme       string
	HTTPPort         int
	HTTPPath         string
	TCPPort          int
	Interval         time.Duration
	Timeout          time.Duration
	InitialDelay     time.Duration
	FailureThreshold int
}

// ResolveProbe selects and normalizes a probe from a service definition.
// Priority: K8s readiness > K8s liveness > Compose healthcheck.
// Returns nil if no health check is configured.
func ResolveProbe(svc eval.Service) *ProbeConfig {
	// Try K8s-style probes first.
	if svc.XNixCompose != nil && svc.XNixCompose.Probes != nil {
		if p := svc.XNixCompose.Probes.Readiness; p != nil {
			return resolveK8sProbe(p)
		}
		if p := svc.XNixCompose.Probes.Liveness; p != nil {
			return resolveK8sProbe(p)
		}
	}

	// Fall back to Compose-style healthcheck.
	if svc.Healthcheck != nil && len(svc.Healthcheck.Test.Parts) > 0 {
		return resolveComposeHealthcheck(svc.Healthcheck)
	}

	return nil
}

func resolveK8sProbe(p *eval.Probe) *ProbeConfig {
	cfg := &ProbeConfig{
		Interval:         applyDuration(p.PeriodSeconds, DefaultInterval),
		Timeout:          applyDuration(p.TimeoutSeconds, DefaultTimeout),
		InitialDelay:     applyDuration(p.InitialDelaySeconds, DefaultInitialDelay),
		FailureThreshold: applyThreshold(p.FailureThreshold, DefaultFailureThreshold),
	}

	switch {
	case p.Exec != nil && len(p.Exec.Command) > 0:
		cfg.Type = ProbeExec
		cfg.ExecCommand = p.Exec.Command
	case p.HTTPGet != nil:
		cfg.Type = ProbeHTTP
		cfg.HTTPPort = p.HTTPGet.Port
		cfg.HTTPPath = p.HTTPGet.Path
		cfg.HTTPScheme = p.HTTPGet.Scheme
		if cfg.HTTPScheme == "" {
			cfg.HTTPScheme = "http"
		}
		if cfg.HTTPPath == "" {
			cfg.HTTPPath = "/"
		}
	default:
		return nil
	}

	return cfg
}

func resolveComposeHealthcheck(hc *eval.Healthcheck) *ProbeConfig {
	cmd := hc.Test.Parts
	// Compose healthcheck test may start with "CMD" or "CMD-SHELL".
	if len(cmd) > 1 && (cmd[0] == "CMD" || cmd[0] == "CMD-SHELL") {
		cmd = cmd[1:]
	}

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      cmd,
		Interval:         parseDurationOrDefault(hc.Interval, DefaultInterval),
		Timeout:          parseDurationOrDefault(hc.Timeout, DefaultTimeout),
		InitialDelay:     parseDurationOrDefault(hc.StartPeriod, DefaultInitialDelay),
		FailureThreshold: hc.Retries,
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = DefaultFailureThreshold
	}
	return cfg
}

func applyDuration(seconds int, def time.Duration) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return def
}

func applyThreshold(val, def int) int {
	if val > 0 {
		return val
	}
	return def
}

func parseDurationOrDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}
