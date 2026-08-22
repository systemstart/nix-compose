package deploy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

const (
	DefaultNrWorkers = 2
)

var (
	// LimitRequestsPerSecond controls the rate of request scheduling.
	LimitRequestsPerSecond float64 = 100

	// RunTrackingCheckSeconds controls how often the tracker polls for status.
	RunTrackingCheckSeconds uint = 1
)

// Pool is a worker pool with a scheduler and status tracker.
type Pool struct {
	all       []*Worker
	wg        sync.WaitGroup
	close     chan struct{}
	queue     chan Request
	feedback  chan Request
	mu        sync.Mutex
	cancelled map[string]bool
	ctx       *RequestContext
}

// NewPool creates and starts a Pool with the given dependencies.
func NewPool(reqCtx *RequestContext) *Pool {
	p := &Pool{
		wg:        sync.WaitGroup{},
		close:     make(chan struct{}),
		queue:     make(chan Request, 1024),
		feedback:  make(chan Request, 1024),
		all:       make([]*Worker, DefaultNrWorkers),
		cancelled: make(map[string]bool),
		ctx:       reqCtx,
	}

	for i := 0; i < DefaultNrWorkers; i++ {
		w := NewWorker(&p.wg, reqCtx)
		p.all[i] = w
		w.wg.Add(1)
		go w.Run(p.queue, p.feedback)
	}

	log.Printf("deploy: pool with %d workers started", DefaultNrWorkers)
	return p
}

// Add persists a deployment and queues all its requests.
func (p *Pool) Add(d *Deployment) error {
	err := d.Save(p.ctx.DB)
	if err != nil {
		return fmt.Errorf("persisting %s failed: %w", d.GetId(), err)
	}
	log.Printf("deploy: queuing %d requests", len(d.Requests))
	for _, r := range d.Requests {
		p.feedback <- r
	}
	return nil
}

// Cancel marks all requests in a deployment as cancelled.
func (p *Pool) Cancel(d *Deployment) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range d.Requests {
		p.cancelled[r.GetSubjectId()] = true
	}
	log.Printf("deploy: cancelled deployment %s (%d requests)", d.GetId(), len(d.Requests))
}

// IsCancelled checks if a request has been cancelled.
func (p *Pool) IsCancelled(r Request) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancelled[r.GetSubjectId()]
}

// Stop shuts down the pool and waits for all workers.
func (p *Pool) Stop() {
	log.Print("deploy: stopping pool")
	close(p.close)
	close(p.queue)
	close(p.feedback)
	p.wg.Wait()
}

// Start launches the scheduler and tracker goroutines in the background.
// The WaitGroup count is incremented synchronously before returning, so it
// is safe to call Stop immediately after Start without a race.
func (p *Pool) Start() {
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		p.Schedule()
	}()
	go func() {
		defer p.wg.Done()
		p.Track()
	}()
}

// Track periodically polls rollout statuses.
func (p *Pool) Track() {
	t := time.NewTicker(time.Second * time.Duration(RunTrackingCheckSeconds)) //nolint:gosec // RunTrackingCheckSeconds is a small constant, no overflow risk
	defer t.Stop()

	for {
		select {
		case <-p.close:
			return
		case <-t.C:
			log.Print("deploy: tracker batch run")
			err := p.ctx.DB.Batch(state.RolloutsById, p.trackRollout)
			if err != nil {
				log.Printf("deploy: tracker batch failed: %s", err)
			}
		}
	}
}

// trackRollout updates the status of a single rollout entry.
func (p *Pool) trackRollout(key, value []byte) {
	rollout, err := DeserializeRollout(value)
	if err != nil {
		log.Printf("bad rollout %s: %s", key, err)
		return
	}
	prov, err := p.ctx.Providers.ForKey(rollout.InstanceKey)
	if err != nil {
		log.Printf("no provider for %s: %s", rollout.InstanceKey, err)
		return
	}
	ref := typing.NewReference(rollout.InstanceId, rollout.InstanceKey)

	status, err := prov.GetStatus(ref)
	if err != nil {
		log.Printf("couldn't fetch status for %s: %s", ref, err)
		rollout.Status = &RolloutStatus{Short: typing.RolloutStatusError}
	} else if status == nil {
		rollout.Status = &RolloutStatus{Short: typing.RolloutStatusPending}
	} else if rollout.Status != nil && rollout.Status.GetShort() != status.GetShort() {
		log.Printf("patching status for %s: %s -> %s", rollout, rollout.Status, status)
		rollout.UpdateStatus(status)
	}
}

// Schedule rate-limits and dispatches requests from feedback to workers.
func (p *Pool) Schedule() {
	delay := time.Second / time.Duration(LimitRequestsPerSecond)

	var lastExecution time.Time

	for {
		select {
		case r := <-p.feedback:
			if r == nil {
				return
			}
			if p.IsCancelled(r) {
				log.Printf("deploy: skipping cancelled request %s", r)
				continue
			}
			dt := time.Since(lastExecution)
			if dt < delay {
				time.Sleep(delay - dt)
			}
			select {
			case p.queue <- r:
			case <-p.close:
				return
			}
			lastExecution = time.Now()
		case <-p.close:
			return
		}
	}
}
