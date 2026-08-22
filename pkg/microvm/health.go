package microvm

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
)

const (
	healthInitialBackoff = 100 * time.Millisecond
	healthMaxBackoff     = time.Second
	healthBackoffFactor  = 2
)

func (v *vm) waitForHealth(ctx context.Context) error {
	backoff := healthInitialBackoff

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("health check: %w", ctx.Err())
		default:
		}

		c, err := dialVsockClient(ctx, v.cfg.CID, v.cfg.VsockPort)
		if err == nil {
			_, healthErr := c.Health(ctx)
			_ = c.Close()
			if healthErr == nil {
				log.Printf("microvm: VM cid=%d is healthy", v.cfg.CID)
				return nil
			}
			log.Printf("microvm: health check failed: %v, retrying in %v", healthErr, backoff)
		} else {
			log.Printf("microvm: vsock dial failed: %v, retrying in %v", err, backoff)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("health check: %w", ctx.Err())
		case <-time.After(backoff):
		}

		backoff *= healthBackoffFactor
		if backoff > healthMaxBackoff {
			backoff = healthMaxBackoff
		}
	}
}

// dialVsockClient wraps client.DialVsock to allow the health check to be
// decoupled from the concrete vsock implementation in tests.
var dialVsockClient = defaultDialVsockClient

func defaultDialVsockClient(ctx context.Context, cid, port uint32) (*client.Client, error) {
	c, err := client.DialVsock(ctx, cid, port)
	if err != nil {
		return nil, fmt.Errorf("dial vsock cid=%d port=%d: %w", cid, port, err)
	}
	return c, nil
}
