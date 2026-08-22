package deploy

import (
	"log"
	"sync"
)

// Worker processes requests from a channel.
type Worker struct {
	close chan struct{}
	wg    *sync.WaitGroup
	ctx   *RequestContext
}

// NewWorker creates a Worker with the given context for request processing.
func NewWorker(wg *sync.WaitGroup, ctx *RequestContext) *Worker {
	return &Worker{
		close: make(chan struct{}),
		wg:    wg,
		ctx:   ctx,
	}
}

// Run reads from input, processes each request, and sends failures to feedback.
func (w *Worker) Run(input <-chan Request, feedback chan<- Request) {
	done := false
	for !done {
		select {
		case r := <-input:
			if r == nil {
				done = true
				break
			}
			w.handleRequest(r, feedback)
		case <-w.close:
			done = true
		}
	}
	log.Printf("deploy: worker done")
	w.wg.Done()
}

// Stop signals the worker to exit.
func (w *Worker) Stop() {
	close(w.close)
}

func (w *Worker) handleRequest(r Request, feedback chan<- Request) {
	err := r.Process(w.ctx)
	if err != nil {
		log.Printf("deploy: worker error request %s: %s, rescheduling",
			r, err)
		select {
		case <-w.close:
			break
		case feedback <- r:
			break
		}
		return
	}
	log.Printf("deploy: worker request success: %s", r)
}
