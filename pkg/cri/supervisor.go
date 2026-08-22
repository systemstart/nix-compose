package cri

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

const (
	backoffMin    = 1 * time.Second
	backoffMax    = 60 * time.Second
	backoffFactor = 2
	// If a container ran longer than this, reset the backoff.
	backoffResetThreshold = 30 * time.Second
)

// SupervisorConfig holds everything the Supervisor needs to restart services.
type SupervisorConfig struct {
	Client  *Client
	Project string
	Opts    ServiceUpOptions
}

// serviceEntry holds per-service metadata used by the supervisor.
type serviceEntry struct {
	svc    eval.Service
	policy RestartPolicy
}

// Supervisor watches running containers and restarts them according to their
// restart policy. Call Register for each service, then Run to block.
type Supervisor struct {
	cfg     SupervisorConfig
	mu      sync.Mutex
	entries map[string]serviceEntry
	stopped bool
}

// NewSupervisor creates a Supervisor from the given config.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	return &Supervisor{
		cfg:     cfg,
		entries: make(map[string]serviceEntry),
	}
}

// Register adds a service that the supervisor will watch.
func (s *Supervisor) Register(name string, svc eval.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[name] = serviceEntry{
		svc:    svc,
		policy: ParseRestartPolicy(svc.Restart),
	}
}

// Stop signals the supervisor to stop restarting services.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

func (s *Supervisor) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// Run starts a watcher goroutine for every registered service and blocks
// until all watchers exit. Watchers exit when the context is cancelled or
// the restart policy says not to restart and the container has exited.
func (s *Supervisor) Run(ctx context.Context) error {
	s.mu.Lock()
	entries := make(map[string]serviceEntry, len(s.entries))
	for k, v := range s.entries {
		entries[k] = v
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	errCh := make(chan error, len(entries))

	for name, entry := range entries {
		wg.Add(1)
		go func(name string, entry serviceEntry) {
			defer wg.Done()
			if err := s.watchService(ctx, name, entry); err != nil {
				errCh <- fmt.Errorf("service %s: %w", name, err)
			}
		}(name, entry)
	}

	wg.Wait()
	close(errCh)

	// Return the first error, if any.
	for err := range errCh {
		return err
	}
	return nil
}

// watchService monitors a single service and restarts it when appropriate.
func (s *Supervisor) watchService(ctx context.Context, name string, entry serviceEntry) error {
	backoff := backoffMin

	for {
		if ctx.Err() != nil {
			return nil
		}

		shouldRestart, ranLong, err := s.waitForExit(ctx, name, entry)
		if err != nil {
			return err
		}
		if !shouldRestart {
			return nil
		}

		if ranLong {
			backoff = backoffMin
		}

		if err := s.backoffAndRestart(ctx, name, entry, backoff); err != nil {
			return err
		}

		backoff *= backoffFactor
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// waitForExit looks up the container and waits for it to exit.
// Returns whether we should restart and whether the container ran long enough to reset backoff.
func (s *Supervisor) waitForExit(ctx context.Context, name string, entry serviceEntry) (shouldRestart, ranLong bool, err error) {
	ctrID, err := s.lookupContainer(ctx, name)
	if err != nil {
		if ctx.Err() != nil {
			return false, false, nil
		}
		return false, false, fmt.Errorf("looking up container: %w", err)
	}

	startedAt := time.Now()

	exitCode, err := s.cfg.Client.WaitExited(ctx, ctrID, 2*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return false, false, nil
		}
		return false, false, fmt.Errorf("waiting for exit: %w", err)
	}

	if !entry.policy.ShouldRestart(int(exitCode), s.isStopped()) {
		return false, false, nil
	}
	if ctx.Err() != nil {
		return false, false, nil
	}

	return true, time.Since(startedAt) > backoffResetThreshold, nil
}

// backoffAndRestart waits for the backoff duration and restarts the service.
func (s *Supervisor) backoffAndRestart(ctx context.Context, name string, entry serviceEntry, backoff time.Duration) error {
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(backoff):
	}

	if ctx.Err() != nil {
		return nil
	}

	if err := s.cfg.Client.ServiceUp(ctx, name, entry.svc, s.cfg.Opts); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("restarting: %w", err)
	}
	return nil
}

// lookupContainer finds the container ID for a service.
func (s *Supervisor) lookupContainer(ctx context.Context, name string) (string, error) {
	pods, err := s.cfg.Client.ListPodSandboxes(ctx, map[string]string{
		LabelProject: s.cfg.Project,
		LabelService: name,
	})
	if err != nil {
		return "", err
	}
	if len(pods) == 0 {
		return "", fmt.Errorf("no pods found for service %s", name)
	}
	ctrs, err := s.cfg.Client.ListContainers(ctx, pods[0].Id)
	if err != nil {
		return "", err
	}
	if len(ctrs) == 0 {
		return "", fmt.Errorf("no containers found for service %s", name)
	}
	return ctrs[0].Id, nil
}
