package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// State represents the health state of a service.
type State int

const (
	// StateStarting is the initial state before any probe has succeeded.
	StateStarting State = iota
	// StateHealthy means the probe has passed.
	StateHealthy
	// StateUnhealthy means the probe has failed FailureThreshold times.
	StateUnhealthy
)

// check holds per-service health monitoring state.
type check struct {
	containerID string
	podIP       string
	cfg         *ProbeConfig
	state       State
	failures    int
	ready       chan struct{} // closed once when first healthy
	readyOnce   sync.Once
}

// Monitor manages health probes for services.
type Monitor struct {
	executor ContainerExecutor
	checks   map[string]*check
	mu       sync.Mutex
}

// NewMonitor creates a new health monitor with the given executor.
func NewMonitor(executor ContainerExecutor) *Monitor {
	return &Monitor{
		executor: executor,
		checks:   make(map[string]*check),
	}
}

// Register adds a service to be monitored.
func (m *Monitor) Register(service, containerID, podIP string, cfg *ProbeConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks[service] = &check{
		containerID: containerID,
		podIP:       podIP,
		cfg:         cfg,
		state:       StateStarting,
		ready:       make(chan struct{}),
	}
}

// Start begins the health probe loop for a service in a goroutine.
func (m *Monitor) Start(ctx context.Context, service string) {
	m.mu.Lock()
	c, ok := m.checks[service]
	m.mu.Unlock()
	if !ok {
		return
	}

	go m.run(ctx, service, c)
}

// WaitHealthy blocks until the service reaches StateHealthy or the context is cancelled.
func (m *Monitor) WaitHealthy(ctx context.Context, service string) error {
	m.mu.Lock()
	c, ok := m.checks[service]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("service %q not registered for health monitoring", service)
	}

	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("health check for %q: %w", service, ctx.Err())
	}
}

// State returns the current health state of a service.
func (m *Monitor) State(service string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.checks[service]
	if !ok {
		return StateStarting
	}
	return c.state
}

func (m *Monitor) run(ctx context.Context, service string, c *check) {
	// Wait initial delay.
	if c.cfg.InitialDelay > 0 {
		select {
		case <-time.After(c.cfg.InitialDelay):
		case <-ctx.Done():
			return
		}
	}

	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	// Run first probe immediately, then on ticker.
	for {
		ok := m.runProbe(ctx, c)

		m.mu.Lock()
		if ok {
			c.failures = 0
			c.state = StateHealthy
			c.readyOnce.Do(func() { close(c.ready) })
		} else {
			c.failures++
			if c.failures >= c.cfg.FailureThreshold {
				c.state = StateUnhealthy
			}
		}
		m.mu.Unlock()

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (m *Monitor) runProbe(ctx context.Context, c *check) bool {
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	switch c.cfg.Type {
	case ProbeExec:
		return m.runExecProbe(probeCtx, c)
	case ProbeHTTP:
		return m.runHTTPProbe(probeCtx, c)
	case ProbeTCP:
		return m.runTCPProbe(probeCtx, c)
	default:
		return false
	}
}

func (m *Monitor) runExecProbe(ctx context.Context, c *check) bool {
	result, err := m.executor.ExecSync(ctx, c.containerID, c.cfg.ExecCommand, int64(c.cfg.Timeout/time.Second))
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

func (m *Monitor) runHTTPProbe(ctx context.Context, c *check) bool {
	url := fmt.Sprintf("%s://%s:%d%s", c.cfg.HTTPScheme, c.podIP, c.cfg.HTTPPort, c.cfg.HTTPPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func (m *Monitor) runTCPProbe(ctx context.Context, c *check) bool {
	addr := fmt.Sprintf("%s:%d", c.podIP, c.cfg.TCPPort)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
