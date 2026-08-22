package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	orchprovider "github.com/systemstart/nix-compose/pkg/orchestrate/provider"
	"github.com/systemstart/nix-compose/pkg/orchestrate/resources"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

// DefaultConditionTimeout is the default timeout for condition waits.
const DefaultConditionTimeout = 5 * 60 // 5 minutes in seconds

// Config holds configuration for the orchestration Engine.
type Config struct {
	DBPath           string
	CRIClient        *cri.Client
	CNIStore         *cni.Store
	VolStore         *volumes.Store
	ConditionTimeout int // seconds; 0 uses DefaultConditionTimeout
}

// Engine is the top-level orchestrator providing plan/apply/state semantics.
type Engine struct {
	db               *state.DB
	registry         *typing.Registry
	providers        *deploy.ProviderRegistry
	pool             *deploy.Pool
	reqCtx           *deploy.RequestContext
	criClient        *cri.Client
	conditionTimeout int
}

// New creates and starts an Engine with the given configuration.
func New(cfg Config) (*Engine, error) {
	dbPath := cfg.DBPath
	if dbPath == "" {
		var err error
		dbPath, err = state.DefaultDBPath()
		if err != nil {
			return nil, fmt.Errorf("default db path: %w", err)
		}
	}

	db, err := state.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening state db: %w", err)
	}

	reg := typing.NewRegistry()
	provReg := deploy.NewProviderRegistry(reg)

	// Register CRI provider
	criProvider := orchprovider.NewCRIProvider(cfg.CRIClient, cfg.CNIStore, cfg.VolStore, db)
	provReg.Register("cri", criProvider)

	reqCtx := &deploy.RequestContext{
		Registry:  reg,
		DB:        db,
		Providers: provReg,
	}

	pool := deploy.NewPool(reqCtx)
	pool.Start()

	condTimeout := cfg.ConditionTimeout
	if condTimeout == 0 {
		condTimeout = DefaultConditionTimeout
	}

	log.Printf("orchestrate: engine started (db: %s)", dbPath)

	return &Engine{
		db:               db,
		registry:         reg,
		providers:        provReg,
		pool:             pool,
		reqCtx:           reqCtx,
		criClient:        cfg.CRIClient,
		conditionTimeout: condTimeout,
	}, nil
}

// Plan computes actions by diffing desired deployment against current rollouts.
func (e *Engine) Plan(desired *deploy.Deployment, conditions convert.ConditionMap) (*Plan, error) {
	rollouts, err := e.State()
	if err != nil {
		return nil, fmt.Errorf("loading current state: %w", err)
	}
	return ComputePlan(desired, rollouts, conditions), nil
}

// ReqCtx returns the request context for direct request processing.
func (e *Engine) ReqCtx() *deploy.RequestContext {
	return e.reqCtx
}

// Apply validates references, persists dependency links, and executes the deployment.
func (e *Engine) Apply(d *deploy.Deployment) error {
	errs := d.CheckReferences(e.reqCtx)
	if len(errs) > 0 {
		return fmt.Errorf("reference check failed: %v", errs)
	}

	if err := d.PersistLinks(e.db); err != nil {
		return fmt.Errorf("persisting links: %w", err)
	}

	if err := e.pool.Add(d); err != nil {
		return fmt.Errorf("queuing deployment: %w", err)
	}
	return nil
}

// State returns all current rollouts from BoltDB.
func (e *Engine) State() ([]*deploy.Rollout, error) {
	keys, err := e.db.Keys(state.RolloutsById)
	if err != nil {
		return nil, fmt.Errorf("listing rollouts: %w", err)
	}

	var rollouts []*deploy.Rollout
	for _, key := range keys {
		rollout, err := deploy.LoadRollout(e.db, key)
		if err != nil {
			log.Printf("orchestrate: skipping bad rollout %s: %s", key, err)
			continue
		}
		if rollout != nil {
			rollouts = append(rollouts, rollout)
		}
	}
	return rollouts, nil
}

// DB returns the underlying state database.
func (e *Engine) DB() *state.DB {
	return e.db
}

// Registry returns the typing registry.
func (e *Engine) Registry() *typing.Registry {
	return e.registry
}

// Providers returns the provider registry.
func (e *Engine) Providers() *deploy.ProviderRegistry {
	return e.providers
}

// CRIClient returns the CRI client, if configured.
func (e *Engine) CRIClient() *cri.Client {
	return e.criClient
}

// Close stops the worker pool and closes the database.
func (e *Engine) Close() error {
	if e.pool != nil {
		e.pool.Stop()
	}
	if e.db != nil {
		if err := e.db.Close(); err != nil {
			return fmt.Errorf("closing state db: %w", err)
		}
	}
	return nil
}

// DriftResult describes a detected drift for a single resource.
type DriftResult struct {
	ResourceID string
	Key        typing.DefinitionKey
	Expected   typing.RolloutStatusShort
	Actual     typing.RolloutStatusShort
	Reason     string
}

// DriftCheck inspects all SUCCEEDED rollouts and compares their expected
// state against the actual CRI provider state.
func (e *Engine) DriftCheck(ctx context.Context) ([]DriftResult, error) {
	rollouts, err := e.State()
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}

	var results []DriftResult
	for _, r := range rollouts {
		if dr, ok := e.checkRolloutDrift(r); ok {
			results = append(results, dr)
		}
	}
	return results, nil
}

// checkRolloutDrift checks a single rollout for drift.
// Returns the drift result and true if drift was detected.
func (e *Engine) checkRolloutDrift(r *deploy.Rollout) (DriftResult, bool) {
	if r.Status == nil {
		return DriftResult{}, false
	}
	// Only check resources that were previously SUCCEEDED or RUNNING.
	if r.Status.Short != typing.RolloutStatusSuccess && r.Status.Short != typing.RolloutStatusRunning {
		return DriftResult{}, false
	}

	def, err := e.registry.GetDefinition(r.InstanceKey)
	if err != nil {
		log.Printf("drift: skipping %s: no definition for %s", r.InstanceId, r.InstanceKey)
		return DriftResult{}, false
	}

	actual, err := e.getProviderStatus(def, r)
	if err != nil {
		log.Printf("drift: error checking %s: %v", r.InstanceId, err)
		return DriftResult{}, false
	}

	actualShort := actual.GetShort()
	// RUNNING is a normal operational state for SUCCEEDED rollouts.
	if actualShort == typing.RolloutStatusSuccess || actualShort == typing.RolloutStatusRunning {
		return DriftResult{}, false
	}

	reason := ""
	if s, ok := actual.(*resources.SimpleStatus); ok && s.Message != "" {
		reason = s.Message
	}

	return DriftResult{
		ResourceID: r.InstanceId,
		Key:        r.InstanceKey,
		Expected:   r.Status.Short,
		Actual:     actualShort,
		Reason:     reason,
	}, true
}

// getProviderStatus retrieves the actual provider status, using version-aware
// checks for container definitions.
func (e *Engine) getProviderStatus(def typing.Definition, r *deploy.Rollout) (typing.Status, error) {
	ref := typing.NewReference(r.InstanceId, r.InstanceKey)

	if cDef, ok := def.(*resources.ContainerDefinition); ok {
		version := extractVersionFromBody(r.Body)
		s, err := cDef.GetProviderStatusWithVersion(ref, version)
		if err != nil {
			return nil, fmt.Errorf("provider status for %s: %w", r.InstanceId, err)
		}
		return s, nil
	}
	s, err := def.GetProviderStatus(ref)
	if err != nil {
		return nil, fmt.Errorf("provider status for %s: %w", r.InstanceId, err)
	}
	return s, nil
}

// extractVersionFromBody pulls the version field from a rollout body JSON.
func extractVersionFromBody(body json.RawMessage) string {
	// Try direct version field (ContainerSpec).
	var spec struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &spec) == nil && spec.Version != "" {
		return spec.Version
	}
	// Try nested container.version (ServiceSpec).
	var sSpec struct {
		Container struct {
			Version string `json:"version"`
		} `json:"container"`
	}
	if json.Unmarshal(body, &sSpec) == nil && sSpec.Container.Version != "" {
		return sSpec.Container.Version
	}
	return ""
}

// ListDeployments returns all persisted deployments from BoltDB.
func (e *Engine) ListDeployments() ([]*deploy.Deployment, error) {
	keys, err := e.db.Keys(state.DeploymentsById)
	if err != nil {
		return nil, fmt.Errorf("listing deployment keys: %w", err)
	}

	var deployments []*deploy.Deployment
	for _, key := range keys {
		d, err := deploy.LoadDeployment(e.db, key)
		if err != nil {
			log.Printf("orchestrate: skipping bad deployment %s: %v", key, err)
			continue
		}
		if d != nil {
			deployments = append(deployments, d)
		}
	}
	return deployments, nil
}

// Rollback loads a previous deployment and applies it, reverting to that state.
// If dryRun is true, it computes the plan without applying.
func (e *Engine) Rollback(ctx context.Context, deploymentID string, dryRun bool) (*Plan, error) {
	d, err := deploy.LoadDeployment(e.db, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("loading deployment %s: %w", deploymentID, err)
	}

	// Build a new deployment from the stored create requests.
	desired := deploy.NewDeployment()
	for _, req := range d.Requests {
		if req.GetType() != deploy.RequestTypeCreate {
			continue
		}
		desired.Requests = append(desired.Requests, req)
	}
	desired.Dependencies = d.Dependencies
	desired.References = d.References

	rollouts, err := e.State()
	if err != nil {
		return nil, fmt.Errorf("loading current state: %w", err)
	}

	plan := ComputePlan(desired, rollouts, nil)

	if dryRun {
		return plan, nil
	}

	if err := e.ApplySync(ctx, plan); err != nil {
		return nil, fmt.Errorf("applying rollback: %w", err)
	}

	return plan, nil
}
