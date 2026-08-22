package client

import (
	"context"
	"fmt"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// LogsOpts holds options for the Logs RPC.
type LogsOpts struct {
	Follow     bool
	Timestamps bool
	Tail       string
	Since      string
}

// Client wraps the generated OrchestrateService gRPC client.
type Client struct {
	conn *grpc.ClientConn
	rpc  orchestratev1.OrchestrateServiceClient
}

// Dial connects to the orchestrate gRPC server at the given unix socket.
func Dial(ctx context.Context, socket string) (*Client, error) {
	conn, err := grpc.NewClient(
		"unix://"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("orchestrate client: dial %s: %w", socket, err)
	}

	c := &Client{
		conn: conn,
		rpc:  orchestratev1.NewOrchestrateServiceClient(conn),
	}

	// Verify the connection works with a health check.
	if _, err := c.Health(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("orchestrate client: health check on %s: %w", socket, err)
	}

	return c, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("orchestrate client: close: %w", err)
	}
	return nil
}

// Health checks the remote orchestrate server health.
func (c *Client) Health(ctx context.Context) (*orchestratev1.HealthResponse, error) {
	resp, err := c.rpc.Health(ctx, &orchestratev1.HealthRequest{})
	if err != nil {
		return nil, fmt.Errorf("health: %w", err)
	}
	return resp, nil
}

// Plan computes a plan from the given composition without applying.
func (c *Client) Plan(ctx context.Context, compJSON []byte, project string, useCNI bool) (*orchestratev1.PlanResponse, error) {
	resp, err := c.rpc.Plan(ctx, &orchestratev1.PlanRequest{
		CompositionJson: compJSON,
		Project:         project,
		UseCni:          useCNI,
	})
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return resp, nil
}

// Apply computes a plan and applies it in one shot.
func (c *Client) Apply(ctx context.Context, compJSON []byte, project string, useCNI bool) (*orchestratev1.ApplyResponse, error) {
	resp, err := c.rpc.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         project,
		UseCni:          useCNI,
	})
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	return resp, nil
}

// Teardown tears down services for a project.
func (c *Client) Teardown(ctx context.Context, project string, timeout int32, removeVolumes bool, compJSON []byte) error {
	_, err := c.rpc.Teardown(ctx, &orchestratev1.TeardownRequest{
		Project:         project,
		Timeout:         timeout,
		RemoveVolumes:   removeVolumes,
		CompositionJson: compJSON,
	})
	if err != nil {
		return fmt.Errorf("teardown: %w", err)
	}
	return nil
}

// State returns the current rollout state.
func (c *Client) State(ctx context.Context) (*orchestratev1.StateResponse, error) {
	resp, err := c.rpc.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	return resp, nil
}

// ExecSync executes a command synchronously in a service container.
func (c *Client) ExecSync(ctx context.Context, project, service string, cmd []string, timeout int64) (*orchestratev1.ExecSyncResponse, error) {
	resp, err := c.rpc.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Project: project,
		Service: service,
		Cmd:     cmd,
		Timeout: timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("exec sync: %w", err)
	}
	return resp, nil
}

// Drift checks for drift between desired and actual state.
func (c *Client) Drift(ctx context.Context, project string) (*orchestratev1.DriftResponse, error) {
	resp, err := c.rpc.Drift(ctx, &orchestratev1.DriftRequest{
		Project: project,
	})
	if err != nil {
		return nil, fmt.Errorf("drift: %w", err)
	}
	return resp, nil
}

// Rollback reverts to a previous deployment.
func (c *Client) Rollback(ctx context.Context, deploymentID string, dryRun bool) (*orchestratev1.RollbackResponse, error) {
	resp, err := c.rpc.Rollback(ctx, &orchestratev1.RollbackRequest{
		DeploymentId: deploymentID,
		DryRun:       dryRun,
	})
	if err != nil {
		return nil, fmt.Errorf("rollback: %w", err)
	}
	return resp, nil
}

// Logs opens a streaming log connection for the requested services.
func (c *Client) Logs(ctx context.Context, project string, services []string, opts LogsOpts) (grpc.ServerStreamingClient[orchestratev1.LogEntry], error) {
	stream, err := c.rpc.Logs(ctx, &orchestratev1.LogsRequest{
		Project:    project,
		Services:   services,
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
		Tail:       opts.Tail,
		Since:      opts.Since,
	})
	if err != nil {
		return nil, fmt.Errorf("logs: %w", err)
	}
	return stream, nil
}
