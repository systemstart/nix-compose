package orchestrate

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/systemstart/nix-compose/pkg/orchestrate/convert"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// ApplySync executes a plan synchronously in dependency order.
// Deletes run in reverse dependency order, creates in forward order.
// On create failure, previously-created resources are rolled back (best-effort).
func (e *Engine) ApplySync(ctx context.Context, plan *Plan) error {
	d := plan.Deployment

	errs := d.CheckReferences(e.reqCtx)
	if len(errs) > 0 {
		return fmt.Errorf("reference check failed: %v", errs)
	}

	if err := d.PersistLinks(e.db); err != nil {
		return fmt.Errorf("persisting links: %w", err)
	}

	deletes, creates := splitRequests(d.Requests)

	// Load links for topo sort.
	links, err := e.loadLinks()
	if err != nil {
		log.Printf("apply: warning: could not load links for ordering: %v", err)
	}

	if err := executeDeletes(ctx, topoSortRequests(deletes, links, true), e.reqCtx); err != nil {
		return err
	}

	if err := executeCreates(ctx, topoSortRequests(creates, links, false), e.reqCtx, e, plan.Conditions); err != nil {
		return err
	}

	// Persist the deployment for rollback history.
	if err := plan.Deployment.Save(e.db); err != nil {
		log.Printf("apply: warning: could not persist deployment: %v", err)
	}

	return nil
}

// splitRequests separates requests into deletes and creates.
func splitRequests(requests deploy.RequestList) (deletes, creates []deploy.Request) {
	for _, req := range requests {
		switch req.GetType() {
		case deploy.RequestTypeDelete:
			deletes = append(deletes, req)
		case deploy.RequestTypeCreate:
			creates = append(creates, req)
		}
	}
	return deletes, creates
}

// executeDeletes runs delete requests in order, logging failures but continuing.
func executeDeletes(ctx context.Context, sorted []deploy.Request, reqCtx *deploy.RequestContext) error {
	for _, req := range sorted {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled: %w", err)
		}
		if err := req.Process(reqCtx); err != nil {
			log.Printf("apply: delete %s failed: %v", req, err)
		}
	}
	return nil
}

// executeCreates runs create requests in order, rolling back on failure.
// After each successful create, if the resource has condition dependencies,
// it waits for the condition to be satisfied before proceeding.
func executeCreates(
	ctx context.Context, sorted []deploy.Request, reqCtx *deploy.RequestContext,
	e *Engine, conditions convert.ConditionMap,
) error {
	var applied []deploy.Request
	for _, req := range sorted {
		if err := ctx.Err(); err != nil {
			rollback(applied, reqCtx)
			return fmt.Errorf("context cancelled: %w", err)
		}
		if err := req.Process(reqCtx); err != nil {
			rollback(applied, reqCtx)
			return fmt.Errorf("creating %s %s: %w", req.GetSubjectKey(), req.GetSubjectId(), err)
		}
		applied = append(applied, req)

		// After successful create, wait for any condition dependencies.
		resourceID := req.GetSubjectId()
		if conditions != nil {
			if _, ok := conditions[resourceID]; ok {
				if err := waitCondition(ctx, e, resourceID, conditions); err != nil {
					rollback(applied, reqCtx)
					return fmt.Errorf("condition wait for %s: %w", resourceID, err)
				}
			}
		}
	}
	return nil
}

// rollback deletes previously-created resources in reverse order (best-effort).
func rollback(applied []deploy.Request, reqCtx *deploy.RequestContext) {
	for i := len(applied) - 1; i >= 0; i-- {
		req := applied[i]
		dr := &deploy.DeleteRequest{
			SubjectId:  req.GetSubjectId(),
			SubjectKey: req.GetSubjectKey(),
		}
		if err := dr.Process(reqCtx); err != nil {
			log.Printf("apply: rollback delete %s failed: %v", req, err)
		}
	}
}

// loadLinks gathers all dependency links from the database.
func (e *Engine) loadLinks() ([]*state.Link, error) {
	var links []*state.Link
	keys, err := e.db.Keys(state.LinksBySourceId)
	if err != nil {
		return nil, fmt.Errorf("loading link keys: %w", err)
	}
	for _, key := range keys {
		ref := typing.NewReference(key, "")
		deps, err := e.db.GetDependencies(ref)
		if err != nil {
			continue
		}
		for _, dep := range deps {
			links = append(links, state.NewLink(ref, dep))
		}
	}
	return links, nil
}

// topoSortRequests sorts requests in dependency order using Kahn's algorithm.
// If reverse is true, returns reverse topological order (for deletes).
// Only considers dependencies between requests in the batch.
func topoSortRequests(requests []deploy.Request, links []*state.Link, reverse bool) []deploy.Request {
	if len(requests) == 0 {
		return nil
	}

	reqSet := buildRequestSet(requests)
	inDegree, adjacency := buildGraph(reqSet, links)
	sorted := kahnSort(reqSet, inDegree, adjacency)
	sorted = appendCycleOrphans(sorted, requests)

	if reverse {
		reverseSlice(sorted)
	}

	return sorted
}

// buildRequestSet creates a map of request ID → Request for the batch.
func buildRequestSet(requests []deploy.Request) map[string]deploy.Request {
	reqSet := make(map[string]deploy.Request, len(requests))
	for _, r := range requests {
		reqSet[r.GetSubjectId()] = r
	}
	return reqSet
}

// buildGraph builds adjacency and in-degree maps from links within the batch.
func buildGraph(reqSet map[string]deploy.Request, links []*state.Link) (map[string]int, map[string][]string) {
	inDegree := make(map[string]int)
	adjacency := make(map[string][]string)
	for id := range reqSet {
		inDegree[id] = 0
	}

	for _, link := range links {
		sourceID := link.Source.GetId()
		targetID := link.Target.GetId()
		if _, ok := reqSet[sourceID]; !ok {
			continue
		}
		if _, ok := reqSet[targetID]; !ok {
			continue
		}
		adjacency[targetID] = append(adjacency[targetID], sourceID)
		inDegree[sourceID]++
	}

	return inDegree, adjacency
}

// kahnSort runs Kahn's algorithm with deterministic tie-breaking.
func kahnSort(reqSet map[string]deploy.Request, inDegree map[string]int, adjacency map[string][]string) []deploy.Request {
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	var sorted []deploy.Request
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, reqSet[id])

		neighbors := adjacency[id]
		sort.Strings(neighbors)
		for _, neighbor := range neighbors {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
				sort.Strings(queue)
			}
		}
	}
	return sorted
}

// appendCycleOrphans appends requests not included in sorted output (due to cycles).
func appendCycleOrphans(sorted []deploy.Request, requests []deploy.Request) []deploy.Request {
	if len(sorted) >= len(requests) {
		return sorted
	}
	included := make(map[string]bool, len(sorted))
	for _, s := range sorted {
		included[s.GetSubjectId()] = true
	}
	for _, r := range requests {
		if !included[r.GetSubjectId()] {
			sorted = append(sorted, r)
		}
	}
	return sorted
}

// reverseSlice reverses a slice of requests in-place.
func reverseSlice(s []deploy.Request) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
