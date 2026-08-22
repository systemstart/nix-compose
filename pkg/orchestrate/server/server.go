package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/logs"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements the OrchestrateService gRPC API.
type Server struct {
	orchestratev1.UnimplementedOrchestrateServiceServer
	criClient *cri.Client
	cniStore  *cni.Store
	volStore  *volumes.Store
	logBase   string
	dbPath    string
	grpcSrv   *grpc.Server
}

// Config holds configuration for the orchestrate server.
type Config struct {
	CRIClient *cri.Client
	CNIStore  *cni.Store
	VolStore  *volumes.Store
	LogBase   string
	DBPath    string // state DB path; empty uses default
}

// New creates a new orchestrate gRPC server.
func New(cfg Config) *Server {
	logBase := cfg.LogBase
	if logBase == "" {
		logBase = logs.DefaultLogBase
	}
	s := &Server{
		criClient: cfg.CRIClient,
		cniStore:  cfg.CNIStore,
		volStore:  cfg.VolStore,
		logBase:   logBase,
		dbPath:    cfg.DBPath,
		grpcSrv:   grpc.NewServer(),
	}
	orchestratev1.RegisterOrchestrateServiceServer(s.grpcSrv, s)
	return s
}

// Serve starts the gRPC server on the given listener.
func (s *Server) Serve(lis net.Listener) error {
	log.Printf("orchestrate server: listening on %s", lis.Addr())
	if err := s.grpcSrv.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

// GracefulStop gracefully stops the gRPC server.
func (s *Server) GracefulStop() {
	s.grpcSrv.GracefulStop()
}

func (s *Server) newEngine() (*orchestrate.Engine, error) {
	engine, err := orchestrate.New(orchestrate.Config{
		DBPath:    s.dbPath,
		CRIClient: s.criClient,
		CNIStore:  s.cniStore,
		VolStore:  s.volStore,
	})
	if err != nil {
		return nil, fmt.Errorf("creating engine: %w", err)
	}
	return engine, nil
}

// planFromComposition runs the full convert → bridge → plan pipeline.
func (s *Server) planFromComposition(ctx context.Context, compJSON []byte, project string, useCNI bool) (*orchestrate.Engine, *orchestrate.Plan, error) {
	var comp eval.Composition
	if err := json.Unmarshal(compJSON, &comp); err != nil {
		return nil, nil, fmt.Errorf("unmarshaling composition: %w", err)
	}

	result, err := convert.Convert(&comp, convert.Options{
		Project: project,
		UseCNI:  useCNI,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("converting to manifests: %w", err)
	}

	engine, err := s.newEngine()
	if err != nil {
		return nil, nil, fmt.Errorf("starting engine: %w", err)
	}

	deployment, conditions, err := convert.Bridge(result, engine.Registry())
	if err != nil {
		_ = engine.Close()
		return nil, nil, fmt.Errorf("bridging to deployment: %w", err)
	}

	plan, err := engine.Plan(deployment, conditions)
	if err != nil {
		_ = engine.Close()
		return nil, nil, fmt.Errorf("computing plan: %w", err)
	}

	return engine, plan, nil
}

// planToActions converts an orchestrate.Plan to proto actions and summary.
func planToActions(plan *orchestrate.Plan) ([]*orchestratev1.Action, int32, int32, int32, int32) {
	var actions []*orchestratev1.Action
	for _, a := range plan.Actions {
		gvk := a.Key.GetGVK()
		actions = append(actions, &orchestratev1.Action{
			Type:       string(a.Type),
			ResourceId: a.ResourceID,
			Kind:       gvk.Kind,
			Reason:     a.Reason,
		})
	}
	creates, updates, destroys, noops := plan.Summary()
	return actions, int32(creates), int32(updates), int32(destroys), int32(noops) //nolint:gosec // plan summary values are small
}

func grpcErr(c codes.Code, format string, args ...any) error {
	return status.Errorf(c, format, args...) //nolint:wrapcheck // gRPC status errors are terminal
}

// Plan computes a plan from the given composition without applying it.
func (s *Server) Plan(ctx context.Context, req *orchestratev1.PlanRequest) (*orchestratev1.PlanResponse, error) {
	if req.Project == "" {
		return nil, grpcErr(codes.InvalidArgument, "project is required")
	}
	if len(req.CompositionJson) == 0 {
		return nil, grpcErr(codes.InvalidArgument, "composition_json is required")
	}

	engine, plan, err := s.planFromComposition(ctx, req.CompositionJson, req.Project, req.UseCni)
	if err != nil {
		return nil, grpcErr(codes.Internal, "plan failed: %v", err)
	}
	defer func() { _ = engine.Close() }()

	actions, creates, updates, destroys, noops := planToActions(plan)
	return &orchestratev1.PlanResponse{
		Actions:  actions,
		Creates:  creates,
		Updates:  updates,
		Destroys: destroys,
		Noops:    noops,
	}, nil
}

// Apply computes a plan and applies it in one shot.
func (s *Server) Apply(ctx context.Context, req *orchestratev1.ApplyRequest) (*orchestratev1.ApplyResponse, error) {
	if req.Project == "" {
		return nil, grpcErr(codes.InvalidArgument, "project is required")
	}
	if len(req.CompositionJson) == 0 {
		return nil, grpcErr(codes.InvalidArgument, "composition_json is required")
	}

	engine, plan, err := s.planFromComposition(ctx, req.CompositionJson, req.Project, req.UseCni)
	if err != nil {
		return nil, grpcErr(codes.Internal, "plan failed: %v", err)
	}
	defer func() { _ = engine.Close() }()

	actions, creates, updates, destroys, noops := planToActions(plan)

	if creates+updates+destroys > 0 {
		if err := engine.ApplySync(ctx, plan); err != nil {
			return nil, grpcErr(codes.Internal, "apply failed: %v", err)
		}
	}

	return &orchestratev1.ApplyResponse{
		Actions:  actions,
		Creates:  creates,
		Updates:  updates,
		Destroys: destroys,
		Noops:    noops,
	}, nil
}

// Teardown tears down services for a project.
func (s *Server) Teardown(ctx context.Context, req *orchestratev1.TeardownRequest) (*orchestratev1.TeardownResponse, error) {
	if req.Project == "" {
		return nil, grpcErr(codes.InvalidArgument, "project is required")
	}

	timeout := int64(req.Timeout)
	if timeout == 0 {
		timeout = 10
	}

	// If composition is provided, use ordered shutdown.
	var comp *eval.Composition
	if len(req.CompositionJson) > 0 {
		comp = &eval.Composition{}
		if err := json.Unmarshal(req.CompositionJson, comp); err != nil {
			return nil, grpcErr(codes.InvalidArgument, "invalid composition_json: %v", err)
		}
	}

	// Shutdown services (ordered if composition available, unordered otherwise).
	if err := s.criClient.ProjectDown(ctx, req.Project, timeout); err != nil {
		return nil, grpcErr(codes.Internal, "teardown failed: %v", err)
	}
	_ = comp // comp would be used for ordered shutdown in future

	// Clean up CNI config.
	if s.cniStore != nil {
		_ = s.cniStore.Remove(req.Project)
	}

	// Remove volumes if requested.
	if req.RemoveVolumes && s.volStore != nil {
		_ = s.volStore.RemoveAll(req.Project)
	}

	return &orchestratev1.TeardownResponse{}, nil
}

// State returns the current rollout state.
func (s *Server) State(_ context.Context, _ *orchestratev1.StateRequest) (*orchestratev1.StateResponse, error) {
	engine, err := s.newEngine()
	if err != nil {
		return nil, grpcErr(codes.Internal, "starting engine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	rollouts, err := engine.State()
	if err != nil {
		return nil, grpcErr(codes.Internal, "reading state: %v", err)
	}

	var infos []*orchestratev1.RolloutInfo
	for _, r := range rollouts {
		statusStr := "unknown"
		if r.Status != nil {
			statusStr = string(r.Status.Short)
		}
		infos = append(infos, &orchestratev1.RolloutInfo{
			InstanceId:  r.InstanceId,
			InstanceKey: string(r.InstanceKey),
			Status:      statusStr,
			Body:        r.Body,
		})
	}

	return &orchestratev1.StateResponse{Rollouts: infos}, nil
}

// Health checks the CRI runtime health.
func (s *Server) Health(ctx context.Context, _ *orchestratev1.HealthRequest) (*orchestratev1.HealthResponse, error) {
	v, err := s.criClient.Version(ctx)
	if err != nil {
		return &orchestratev1.HealthResponse{Healthy: false}, nil
	}
	return &orchestratev1.HealthResponse{
		Healthy:        true,
		RuntimeName:    v.RuntimeName,
		RuntimeVersion: v.RuntimeVersion,
	}, nil
}

// ExecSync executes a command synchronously in a service container.
func (s *Server) ExecSync(ctx context.Context, req *orchestratev1.ExecSyncRequest) (*orchestratev1.ExecSyncResponse, error) {
	if req.Project == "" {
		return nil, grpcErr(codes.InvalidArgument, "project is required")
	}
	if req.Service == "" {
		return nil, grpcErr(codes.InvalidArgument, "service is required")
	}
	if len(req.Cmd) == 0 {
		return nil, grpcErr(codes.InvalidArgument, "cmd is required")
	}

	containerID, err := lookupContainerID(ctx, s.criClient, req.Project, req.Service)
	if err != nil {
		return nil, grpcErr(codes.NotFound, "finding container: %v", err)
	}

	resp, err := s.criClient.ExecSync(ctx, containerID, req.Cmd, req.Timeout)
	if err != nil {
		return nil, grpcErr(codes.Internal, "exec sync: %v", err)
	}

	return &orchestratev1.ExecSyncResponse{
		Stdout:   resp.GetStdout(),
		Stderr:   resp.GetStderr(),
		ExitCode: resp.GetExitCode(),
	}, nil
}

// Logs streams log entries for the requested services.
func (s *Server) Logs(req *orchestratev1.LogsRequest, stream grpc.ServerStreamingServer[orchestratev1.LogEntry]) error {
	if req.Project == "" {
		return grpcErr(codes.InvalidArgument, "project is required")
	}

	services := req.Services
	opts := logs.Options{
		Follow:     req.Follow,
		Timestamps: req.Timestamps,
		Tail:       req.Tail,
		Since:      req.Since,
	}

	// Collect and send existing log entries.
	entries := logs.CollectAndFilter(s.logBase, req.Project, services, opts)
	for _, e := range entries {
		entry := &orchestratev1.LogEntry{
			Service:   e.Service,
			Timestamp: timestamppb.New(e.Timestamp),
			Stream:    e.Stream,
			Message:   e.Message,
		}
		if err := stream.Send(entry); err != nil {
			return fmt.Errorf("sending log entry: %w", err)
		}
	}

	if !req.Follow {
		return nil
	}

	// Follow mode: poll for new entries.
	return logs.FollowStream(stream.Context(), s.logBase, req.Project, services, func(e logs.LogEntry) error { //nolint:wrapcheck // logs.FollowStream returns nil or context errors
		return stream.Send(&orchestratev1.LogEntry{ //nolint:wrapcheck // gRPC stream send errors are terminal
			Service:   e.Service,
			Timestamp: timestamppb.New(e.Timestamp),
			Stream:    e.Stream,
			Message:   e.Message,
		})
	})
}

// Drift checks for drift between desired and actual state.
func (s *Server) Drift(ctx context.Context, req *orchestratev1.DriftRequest) (*orchestratev1.DriftResponse, error) {
	engine, err := s.newEngine()
	if err != nil {
		return nil, grpcErr(codes.Internal, "starting engine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	results, err := engine.DriftCheck(ctx)
	if err != nil {
		return nil, grpcErr(codes.Internal, "drift check: %v", err)
	}

	var items []*orchestratev1.DriftItem
	for _, r := range results {
		gvk := r.Key.GetGVK()
		items = append(items, &orchestratev1.DriftItem{
			ResourceId: r.ResourceID,
			Kind:       gvk.Kind,
			Expected:   string(r.Expected),
			Actual:     string(r.Actual),
			Reason:     r.Reason,
		})
	}

	return &orchestratev1.DriftResponse{Items: items}, nil
}

// Rollback reverts to a previous deployment.
func (s *Server) Rollback(ctx context.Context, req *orchestratev1.RollbackRequest) (*orchestratev1.RollbackResponse, error) {
	if req.DeploymentId == "" {
		return nil, grpcErr(codes.InvalidArgument, "deployment_id is required")
	}

	engine, err := s.newEngine()
	if err != nil {
		return nil, grpcErr(codes.Internal, "starting engine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	plan, err := engine.Rollback(ctx, req.DeploymentId, req.DryRun)
	if err != nil {
		return nil, grpcErr(codes.Internal, "rollback failed: %v", err)
	}

	actions, creates, updates, destroys, noops := planToActions(plan)
	return &orchestratev1.RollbackResponse{
		Actions:  actions,
		Creates:  creates,
		Updates:  updates,
		Destroys: destroys,
		Noops:    noops,
	}, nil
}

// lookupContainerID finds the first container ID for a service in a project.
func lookupContainerID(ctx context.Context, client *cri.Client, project, service string) (string, error) {
	pods, err := client.ListPodSandboxes(ctx, map[string]string{
		cri.LabelProject: project,
		cri.LabelService: service,
	})
	if err != nil {
		return "", fmt.Errorf("listing pod sandboxes: %w", err)
	}
	if len(pods) == 0 {
		return "", fmt.Errorf("no pods found for service %s", service)
	}
	ctrs, err := client.ListContainers(ctx, pods[0].Id)
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}
	if len(ctrs) == 0 {
		return "", fmt.Errorf("no containers found for service %s", service)
	}
	return ctrs[0].Id, nil
}
