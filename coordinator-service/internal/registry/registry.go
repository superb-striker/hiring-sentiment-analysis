// Package registry tracks known workers, their heartbeat health, and what batch each one currently holds.
package registry

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"hiring-sentiment/shared"
)

// BatchAssignment tracks what a worker is currently holding,
// so that if the worker dies mid-batch the caller knows exactly which records to requeue.
type BatchAssignment struct {
	BatchID    string
	RecordIDs  []string
	AssignedAt time.Time
}

type Worker struct {
	ID             string
	URL            string
	LastHeartbeat  time.Time
	ReportedStatus string       // last value the worker itself sent
	Dead           bool
	Current        *BatchAssignment
}

// IsAvailable reports whether this worker can take a new batch right now.
func (w *Worker) IsAvailable(now time.Time) bool {
	if w.Dead || w.URL == "" {
		return false
	}
	if now.Sub(w.LastHeartbeat) > shared.HeartbeatTTL {
		return false
	}
	if w.Current != nil {
		return false
	}
	return w.ReportedStatus == "idle" || w.ReportedStatus == ""
}

type Registry struct {
	mu      sync.Mutex
	workers map[string]*Worker
	order   []string        // stable order for round-robin
	rrPos   int
}

// New seeds the registry from configured worker_id -> url pairs.
// Workers are unavailable until they call Heartbeat at least once.
func New(idToURL map[string]string) *Registry {
	r := &Registry{workers: make(map[string]*Worker)}
	for id, url := range idToURL {
		r.workers[id] = &Worker{ID: id, URL: url}
		r.order = append(r.order, id)
	}
	return r
}

// ParseWorkerURLs parses WORKER_URLS in "worker_id@http://host:port" form, comma-separated.
func ParseWorkerURLs(raw string) (map[string]string, error) {
	result := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "@", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid WORKER_URLS entry %q, expected worker_id@url", entry)
		}
		result[parts[0]] = parts[1]
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("WORKER_URLS produced no workers")
	}
	return result, nil
}

func (r *Registry) Heartbeat(hb shared.Heartbeat, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	w, ok := r.workers[hb.WorkerID]
	if !ok {
		// Unknown worker heartbeating in (not in WORKER_URLS config) track it read-only,
		// but it never receives assignments since there's no URL for it.
		w = &Worker{ID: hb.WorkerID}
		r.workers[hb.WorkerID] = w
		r.order = append(r.order, hb.WorkerID)
	}
	w.LastHeartbeat = now
	w.ReportedStatus = hb.Status
	w.Dead = false
	if hb.CurrentBatch == "" {
		// Worker reports idle/no batch; trust it and clear our side too, in case a /result POST was lost.
		w.Current = nil
	}
}

// PickAvailable returns the next available worker using round-robin over the configured order,
// or nil if none are free right now.
func (r *Registry) PickAvailable(now time.Time) *Worker {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(r.order)
	if n == 0 {
		return nil
	}
	for i := range n {
		idx := (r.rrPos + i) % n
		w := r.workers[r.order[idx]]
		if w.IsAvailable(now) {
			r.rrPos = (idx + 1) % n
			return w
		}
	}
	return nil
}

func (r *Registry) Assign(workerID, batchID string, recordIDs []string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[workerID]
	if !ok {
		return
	}
	w.Current = &BatchAssignment{BatchID: batchID, RecordIDs: recordIDs, AssignedAt: now}
	w.ReportedStatus = "busy"
}

// Unassign clears a worker's current batch, e.g. after /result or a failed POST /assign that never reached the worker.
func (r *Registry) Unassign(workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[workerID]; ok {
		w.Current = nil
		w.ReportedStatus = "idle"
	}
}

// SweepDead finds workers whose heartbeat is stale, marks them dead, and
// returns the batch assignments that need to be requeued as a result.
func (r *Registry) SweepDead(now time.Time) []*BatchAssignment {
	r.mu.Lock()
	defer r.mu.Unlock()

	var orphaned []*BatchAssignment
	for _, w := range r.workers {
		if w.Dead {
			continue
		}
		if w.LastHeartbeat.IsZero() || now.Sub(w.LastHeartbeat) <= shared.HeartbeatTTL {
			continue
		}
		w.Dead = true
		if w.Current != nil {
			orphaned = append(orphaned, w.Current)
			w.Current = nil
		}
	}
	return orphaned
}

// Snapshot returns a copy of all known workers, e.g. for a debug endpoint.
func (r *Registry) Snapshot() []Worker {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Worker, 0, len(r.workers))
	for _, id := range r.order {
		out = append(out, *r.workers[id])
	}
	return out
}
