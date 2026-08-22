package health

import (
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestResolveProbe_ComposeHealthcheck(t *testing.T) {
	svc := eval.Service{
		Healthcheck: &eval.Healthcheck{
			Test:     eval.CommandValue{Parts: []string{"CMD", "curl", "-f", "http://localhost/"}},
			Interval: "5s",
			Timeout:  "3s",
			Retries:  5,
		},
	}

	cfg := ResolveProbe(svc)
	if cfg == nil {
		t.Fatal("expected non-nil probe config")
		return
	}
	if cfg.Type != ProbeExec {
		t.Errorf("type = %d, want ProbeExec", cfg.Type)
	}
	if len(cfg.ExecCommand) != 3 || cfg.ExecCommand[0] != "curl" {
		t.Errorf("command = %v, want [curl -f http://localhost/]", cfg.ExecCommand)
	}
	if cfg.Interval != 5*time.Second {
		t.Errorf("interval = %v, want 5s", cfg.Interval)
	}
	if cfg.Timeout != 3*time.Second {
		t.Errorf("timeout = %v, want 3s", cfg.Timeout)
	}
	if cfg.FailureThreshold != 5 {
		t.Errorf("failure threshold = %d, want 5", cfg.FailureThreshold)
	}
}

func TestResolveProbe_K8sReadiness(t *testing.T) {
	svc := eval.Service{
		XNixCompose: &eval.NixComposeExtended{
			Probes: &eval.Probes{
				Readiness: &eval.Probe{
					HTTPGet: &eval.ProbeHTTPGet{
						Path: "/ready",
						Port: 8080,
					},
					PeriodSeconds:    15,
					TimeoutSeconds:   3,
					FailureThreshold: 2,
				},
			},
		},
	}

	cfg := ResolveProbe(svc)
	if cfg == nil {
		t.Fatal("expected non-nil probe config")
		return
	}
	if cfg.Type != ProbeHTTP {
		t.Errorf("type = %d, want ProbeHTTP", cfg.Type)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("port = %d, want 8080", cfg.HTTPPort)
	}
	if cfg.HTTPPath != "/ready" {
		t.Errorf("path = %q, want /ready", cfg.HTTPPath)
	}
	if cfg.HTTPScheme != "http" {
		t.Errorf("scheme = %q, want http", cfg.HTTPScheme)
	}
	if cfg.Interval != 15*time.Second {
		t.Errorf("interval = %v, want 15s", cfg.Interval)
	}
	if cfg.FailureThreshold != 2 {
		t.Errorf("failure threshold = %d, want 2", cfg.FailureThreshold)
	}
}

func TestResolveProbe_K8sTakesPrecedence(t *testing.T) {
	svc := eval.Service{
		Healthcheck: &eval.Healthcheck{
			Test: eval.CommandValue{Parts: []string{"CMD", "curl", "http://localhost/"}},
		},
		XNixCompose: &eval.NixComposeExtended{
			Probes: &eval.Probes{
				Readiness: &eval.Probe{
					Exec: &eval.ProbeExec{
						Command: []string{"/bin/check"},
					},
				},
			},
		},
	}

	cfg := ResolveProbe(svc)
	if cfg == nil {
		t.Fatal("expected non-nil probe config")
		return
	}
	// K8s readiness should win over compose healthcheck.
	if len(cfg.ExecCommand) != 1 || cfg.ExecCommand[0] != "/bin/check" {
		t.Errorf("expected K8s readiness command, got %v", cfg.ExecCommand)
	}
}

func TestResolveProbe_NoProbes(t *testing.T) {
	svc := eval.Service{Image: "nginx"}
	cfg := ResolveProbe(svc)
	if cfg != nil {
		t.Errorf("expected nil probe config, got %+v", cfg)
	}
}

func TestResolveProbe_Defaults(t *testing.T) {
	svc := eval.Service{
		Healthcheck: &eval.Healthcheck{
			Test: eval.CommandValue{Parts: []string{"true"}},
		},
	}

	cfg := ResolveProbe(svc)
	if cfg == nil {
		t.Fatal("expected non-nil probe config")
		return
	}
	if cfg.Interval != DefaultInterval {
		t.Errorf("interval = %v, want %v", cfg.Interval, DefaultInterval)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", cfg.Timeout, DefaultTimeout)
	}
	if cfg.InitialDelay != DefaultInitialDelay {
		t.Errorf("initial delay = %v, want %v", cfg.InitialDelay, DefaultInitialDelay)
	}
	if cfg.FailureThreshold != DefaultFailureThreshold {
		t.Errorf("failure threshold = %d, want %d", cfg.FailureThreshold, DefaultFailureThreshold)
	}
}

func TestResolveProbe_K8sLivenessFallback(t *testing.T) {
	svc := eval.Service{
		XNixCompose: &eval.NixComposeExtended{
			Probes: &eval.Probes{
				Liveness: &eval.Probe{
					Exec: &eval.ProbeExec{
						Command: []string{"/bin/liveness"},
					},
				},
			},
		},
	}

	cfg := ResolveProbe(svc)
	if cfg == nil {
		t.Fatal("expected non-nil probe config")
		return
	}
	if len(cfg.ExecCommand) != 1 || cfg.ExecCommand[0] != "/bin/liveness" {
		t.Errorf("expected liveness command, got %v", cfg.ExecCommand)
	}
}

func TestResolveProbe_HTTPDefaults(t *testing.T) {
	svc := eval.Service{
		XNixCompose: &eval.NixComposeExtended{
			Probes: &eval.Probes{
				Readiness: &eval.Probe{
					HTTPGet: &eval.ProbeHTTPGet{
						Port: 80,
					},
				},
			},
		},
	}

	cfg := ResolveProbe(svc)
	if cfg == nil {
		t.Fatal("expected non-nil probe config")
		return
	}
	if cfg.HTTPScheme != "http" {
		t.Errorf("scheme = %q, want http", cfg.HTTPScheme)
	}
	if cfg.HTTPPath != "/" {
		t.Errorf("path = %q, want /", cfg.HTTPPath)
	}
}
