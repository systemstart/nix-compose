package deploy

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ===========================================================================
// Pool tests
// ===========================================================================

func TestNewPool(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	def := &processableDef{key: "test/v1/Widget"}
	reg.Register(def)
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := NewPool(ctx)
	t.Cleanup(func() {
		close(p.queue)
		p.wg.Wait()
	})

	// Verify pool is properly initialized.
	if p == nil {
		t.Fatal("expected non-nil pool")
		return
	}
	if len(p.all) != DefaultNrWorkers {
		t.Fatalf("expected %d workers, got %d", DefaultNrWorkers, len(p.all))
	}
	if p.cancelled == nil || len(p.cancelled) != 0 {
		t.Fatal("expected empty non-nil cancelled map")
	}
	if p.ctx != ctx {
		t.Fatal("expected ctx to be set")
	}
	if p.queue == nil || p.feedback == nil || p.close == nil {
		t.Fatal("expected non-nil channels")
	}
}

func TestPool_StartStop(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	// Build pool manually to avoid spawning workers (which need a processable
	// context). We only want to test Start/Stop lifecycle of scheduler+tracker.
	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	p.Start()

	// Give scheduler and tracker goroutines a moment to start.
	time.Sleep(50 * time.Millisecond)

	// Stop should close channels and wait for goroutines to finish without
	// blocking indefinitely. If Stop hangs, the test timeout will catch it.
	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("Pool.Stop() did not complete within 5 seconds")
	}
}

func TestPool_AddAndCancel(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	key := typing.DefinitionKey("test/v1/Svc")
	d := NewDeployment()
	d.Requests = append(d.Requests, &CreateRequest{SubjectId: "a-1", SubjectKey: key, Subject: json.RawMessage(`{}`)})
	d.Requests = append(d.Requests, &CreateRequest{SubjectId: "a-2", SubjectKey: key, Subject: json.RawMessage(`{}`)})

	// Add the deployment -- should persist and enqueue requests.
	if err := p.Add(d); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(p.feedback) != 2 {
		t.Fatalf("expected 2 feedback items after Add, got %d", len(p.feedback))
	}

	// Verify nothing is cancelled yet.
	if p.IsCancelled(d.Requests[0]) {
		t.Error("expected a-1 not cancelled before Cancel")
	}
	if p.IsCancelled(d.Requests[1]) {
		t.Error("expected a-2 not cancelled before Cancel")
	}

	// Cancel the deployment.
	p.Cancel(d)

	// Both requests should now be cancelled.
	if !p.IsCancelled(d.Requests[0]) {
		t.Error("expected a-1 cancelled after Cancel")
	}
	if !p.IsCancelled(d.Requests[1]) {
		t.Error("expected a-2 cancelled after Cancel")
	}

	// A request with a different subject ID should not be cancelled.
	other := &CreateRequest{SubjectId: "a-3", SubjectKey: key}
	if p.IsCancelled(other) {
		t.Error("expected a-3 not cancelled")
	}

	// Verify the deployment was persisted to DB.
	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if loaded.Id != d.Id {
		t.Fatalf("deployment ID mismatch: got %s, want %s", loaded.Id, d.Id)
	}
}

func TestPool_Schedule(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	// Use a very high rate limit so scheduling is near-instant.
	origRate := LimitRequestsPerSecond
	LimitRequestsPerSecond = 100000
	t.Cleanup(func() { LimitRequestsPerSecond = origRate })

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	// Start only the scheduler goroutine.
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.Schedule()
	}()

	key := typing.DefinitionKey("test/v1/Svc")
	req := &CreateRequest{SubjectId: "sched-1", SubjectKey: key, Subject: json.RawMessage(`{}`)}

	// Put the request into feedback (the scheduler reads from feedback).
	p.feedback <- req

	// The scheduler should forward it to the queue channel.
	select {
	case got := <-p.queue:
		if got == nil {
			t.Fatal("expected non-nil request from queue")
			return
		}
		if got.GetSubjectId() != "sched-1" {
			t.Fatalf("expected subject ID sched-1, got %s", got.GetSubjectId())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for request on queue")
	}

	// Stop the scheduler.
	close(p.close)
	p.wg.Wait()
}

func TestPool_Schedule_SkipsCancelled(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	origRate := LimitRequestsPerSecond
	LimitRequestsPerSecond = 100000
	t.Cleanup(func() { LimitRequestsPerSecond = origRate })

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	key := typing.DefinitionKey("test/v1/Svc")
	cancelledReq := &CreateRequest{SubjectId: "cancel-1", SubjectKey: key}
	normalReq := &CreateRequest{SubjectId: "normal-1", SubjectKey: key}

	// Mark cancel-1 as cancelled before scheduling.
	p.mu.Lock()
	p.cancelled["cancel-1"] = true
	p.mu.Unlock()

	// Start scheduler.
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.Schedule()
	}()

	// Send cancelled request first, then normal request.
	p.feedback <- cancelledReq
	p.feedback <- normalReq

	// Only the normal request should arrive on the queue.
	select {
	case got := <-p.queue:
		if got.GetSubjectId() != "normal-1" {
			t.Fatalf("expected normal-1 on queue, got %s", got.GetSubjectId())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for request on queue")
	}

	// Verify the cancelled request did not end up in queue.
	select {
	case extra := <-p.queue:
		t.Fatalf("unexpected extra request on queue: %s", extra.GetSubjectId())
	case <-time.After(100 * time.Millisecond):
		// expected: nothing else in queue
	}

	close(p.close)
	p.wg.Wait()
}

func TestPool_Schedule_StopsOnClose(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.Schedule()
	}()

	// Close should cause Schedule to return.
	close(p.close)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("Schedule did not exit after close within 3 seconds")
	}
}

func TestNewWorker(t *testing.T) {
	var wg sync.WaitGroup
	w := NewWorker(&wg, nil)
	if w == nil {
		t.Fatal("expected non-nil worker")
		return
	}
	if w.wg != &wg {
		t.Fatal("expected wg to be set")
	}
}

func TestWorker_RunStopsOnNilInput(t *testing.T) {
	var wg sync.WaitGroup
	w := NewWorker(&wg, nil)
	input := make(chan Request, 1)
	feedback := make(chan Request, 1)

	wg.Add(1)
	go w.Run(input, feedback)

	// Send nil to stop the worker.
	input <- nil
	wg.Wait()
}

func TestWorker_RunStopsOnClose(t *testing.T) {
	var wg sync.WaitGroup
	w := NewWorker(&wg, nil)
	input := make(chan Request, 1)
	feedback := make(chan Request, 1)

	wg.Add(1)
	go w.Run(input, feedback)

	w.Stop()
	wg.Wait()
}

func TestWorker_HandleRequestSuccess(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	def := &processableDef{key: "test/v1/Widget"}
	reg.Register(def)

	var wg sync.WaitGroup
	ctx := &RequestContext{Registry: reg, DB: db}
	w := NewWorker(&wg, ctx)

	input := make(chan Request, 1)
	feedback := make(chan Request, 10)

	cr := &CreateRequest{
		SubjectId:  "w-1",
		SubjectKey: "test/v1/Widget",
		Subject:    json.RawMessage(`{}`),
	}

	wg.Add(1)
	go w.Run(input, feedback)

	input <- cr
	// Send nil to stop after processing.
	input <- nil
	wg.Wait()

	// Feedback should be empty on success.
	if len(feedback) != 0 {
		t.Errorf("expected empty feedback, got %d items", len(feedback))
	}
}

func TestWorker_HandleRequestFailureReschedules(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()

	var wg sync.WaitGroup
	ctx := &RequestContext{Registry: reg, DB: db}
	w := NewWorker(&wg, ctx)

	input := make(chan Request, 1)
	feedback := make(chan Request, 10)

	// Unknown key will cause Process to fail.
	cr := &CreateRequest{
		SubjectId:  "w-1",
		SubjectKey: "unknown/v1/Widget",
		Subject:    json.RawMessage(`{}`),
	}

	wg.Add(1)
	go w.Run(input, feedback)

	input <- cr
	input <- nil
	wg.Wait()

	// Failed request should be rescheduled to feedback.
	if len(feedback) != 1 {
		t.Fatalf("expected 1 feedback item, got %d", len(feedback))
	}
}

func TestPool_CancelAndIsCancelled(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		all:       nil,
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	key := typing.DefinitionKey("test/v1/Svc")
	d := NewDeployment()
	d.Requests = append(d.Requests, &CreateRequest{SubjectId: "s-1", SubjectKey: key})
	d.Requests = append(d.Requests, &CreateRequest{SubjectId: "s-2", SubjectKey: key})

	// Before cancel, nothing should be cancelled.
	if p.IsCancelled(d.Requests[0]) {
		t.Error("expected not cancelled before Cancel")
	}

	p.Cancel(d)

	if !p.IsCancelled(d.Requests[0]) {
		t.Error("expected cancelled after Cancel")
	}
	if !p.IsCancelled(d.Requests[1]) {
		t.Error("expected s-2 cancelled after Cancel")
	}

	// A different request should not be cancelled.
	other := &CreateRequest{SubjectId: "s-3", SubjectKey: key}
	if p.IsCancelled(other) {
		t.Error("expected s-3 not cancelled")
	}
}

func TestPool_Add(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	key := typing.DefinitionKey("test/v1/Svc")
	d := NewDeployment()
	d.Requests = append(d.Requests, &CreateRequest{SubjectId: "s-1", SubjectKey: key, Subject: json.RawMessage(`{}`)})
	d.Requests = append(d.Requests, &DeleteRequest{SubjectId: "s-2", SubjectKey: key})

	if err := p.Add(d); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Requests should be queued in feedback.
	if len(p.feedback) != 2 {
		t.Fatalf("expected 2 feedback items, got %d", len(p.feedback))
	}

	// Deployment should be saved to DB.
	loaded, err := LoadDeployment(db, d.Id)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if loaded.Id != d.Id {
		t.Fatalf("deployment ID mismatch: got %s, want %s", loaded.Id, d.Id)
	}
}

func TestDeployment_PersistLinks_BothDirections(t *testing.T) {
	db := openTestDB(t)
	d := NewDeployment()

	// Add dependencies.
	inst := &stubInstance{id: "s-1", key: "test/v1/Svc"}
	dep := typing.NewReference("d-1", "test/v1/Dep")
	d.AddCreation(inst, json.RawMessage(`{}`), typing.ReferenceList{dep})

	// Add depending links too.
	depLink := state.NewLink(typing.NewReference("x-1", "test/v1/X"), typing.NewReference("s-1", "test/v1/Svc"))
	d.Depending = append(d.Depending, depLink)

	if err := d.PersistLinks(db); err != nil {
		t.Fatalf("PersistLinks: %v", err)
	}

	// Verify dependency link.
	deps, err := db.GetDependencies(typing.NewReference("s-1", "test/v1/Svc"))
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(deps))
	}

	// Verify depending link.
	deps2, err := db.GetDependencies(typing.NewReference("x-1", "test/v1/X"))
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(deps2) != 1 {
		t.Fatalf("expected 1 depending, got %d", len(deps2))
	}
}

func TestProviderRegistry_DuplicateKeyPanics(t *testing.T) {
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	def := &stubProviderDef{key: "test/v1/Widget"}
	pr.Register("p1", &stubProvider{definitions: []typing.Definition{def}})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate key")
		}
	}()

	// Register another provider with the same key.
	def2 := &stubProviderDef{key: "test/v1/Widget"}
	pr.Register("p2", &stubProvider{definitions: []typing.Definition{def2}})
}

func TestRollout_NilStatus(t *testing.T) {
	r := &Rollout{InstanceId: "id", InstanceKey: "g/v1/K"}
	s := r.GetStatus()
	if s == nil {
		t.Fatal("GetStatus() on rollout with nil Status should return the nil *RolloutStatus")
		return
	}
	if s.GetShort() != typing.RolloutStatusUnknown {
		t.Fatalf("nil status GetShort() = %q, want %q", s.GetShort(), typing.RolloutStatusUnknown)
	}
}

func TestRollout_ErrorsField(t *testing.T) {
	r := &Rollout{
		InstanceId:  "id",
		InstanceKey: "g/v1/K",
		Errors:      []string{"err1", "err2"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded Rollout
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(loaded.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(loaded.Errors))
	}
}

// ===========================================================================
// trackRollout tests
// ===========================================================================

func TestPool_TrackRollout_ValidRollout(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)

	// Register the provider through the ProviderRegistry which also registers
	// the definition in the typing registry.
	sprov := &stubProvider{definitions: []typing.Definition{&stubProviderDef{key: "test/v1/Widget"}}}
	pr.Register("test", sprov)

	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}
	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	// Save a rollout so trackRollout can process it.
	rollout := &Rollout{
		InstanceId:  "track-1",
		InstanceKey: "test/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusPending},
	}
	if err := db.Save(state.RolloutsById, rollout); err != nil {
		t.Fatalf("Save rollout: %v", err)
	}

	// Serialize the rollout to call trackRollout directly.
	data, err := json.Marshal(rollout)
	if err != nil {
		t.Fatalf("Marshal rollout: %v", err)
	}

	// trackRollout should not panic.
	p.trackRollout([]byte("track-1"), data)
}

func TestPool_TrackRollout_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	// trackRollout with invalid JSON should log error but not panic.
	p.trackRollout([]byte("bad-key"), []byte(`{invalid json`))
}

func TestPool_TrackRollout_UnknownProvider(t *testing.T) {
	db := openTestDB(t)
	reg := typing.NewRegistry()
	pr := NewProviderRegistry(reg)
	ctx := &RequestContext{Registry: reg, DB: db, Providers: pr}

	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		cancelled: make(map[string]bool),
		ctx:       ctx,
	}

	// Rollout with a key that has no registered provider.
	rollout := &Rollout{
		InstanceId:  "unknown-1",
		InstanceKey: "unknown/v1/Widget",
		Status:      &RolloutStatus{Short: typing.RolloutStatusPending},
	}
	data, _ := json.Marshal(rollout)

	// Should log error for unknown provider but not panic.
	p.trackRollout([]byte("unknown-1"), data)
}

// Ensure fmt import is used in this file.
var _ = fmt.Errorf
