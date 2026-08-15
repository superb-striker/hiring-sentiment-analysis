// Package api holds the coordinator's HTTP handlers. 
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"hiring-sentiment/coordinator/internal/registry"
	"hiring-sentiment/hiringdb"
	"hiring-sentiment/shared"
)

// resultStore is the narrow slice of hiringdb.DB that result-handling
// needs.
type resultStore interface {
	GetRecord(ctx context.Context, recordID string) (hiringdb.Record, error)
	UpdateRecordStatus(ctx context.Context, recordID string, status string, opts ...hiringdb.UpdateOption) error
}

type Server struct {
	registry *registry.Registry
	store    resultStore
	logger   *slog.Logger
}

func NewServer(reg *registry.Registry, st resultStore, logger *slog.Logger) *Server {
	return &Server{registry: reg, store: st, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /result", s.handleResult)
	mux.HandleFunc("GET /workers", s.handleWorkers)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb shared.Heartbeat
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&hb); err != nil {
		http.Error(w, "invalid heartbeat payload", http.StatusBadRequest)
		return
	}
	if hb.WorkerID == "" {
		http.Error(w, "worker_id is required", http.StatusBadRequest)
		return
	}

	s.registry.Heartbeat(hb, time.Now().UTC())
	s.logger.Info("heartbeat_received", "worker_id", hb.WorkerID, "status", hb.Status, "current_batch", hb.CurrentBatch)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	var result shared.TaskResult
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&result); err != nil {
		http.Error(w, "invalid result payload", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	for _, item := range result.Results {
		s.handleResultItem(ctx, result.BatchID, result.WorkerID, item)
	}

	// Whatever the outcome, this worker's batch is over -free it for more work.
	s.registry.Unassign(result.WorkerID)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResultItem(ctx context.Context, batchID, workerID string, item shared.TaskResultItem) {
	switch item.Status {
	case shared.StatusComplete:
		// Worker is assumed to have written status=complete + extracted fields directly to DynamoDB. 
		s.logger.Info("record_complete", "batch_id", batchID, "worker_id", workerID, "record_id", item.RecordID)
	case shared.StatusFailed:
		rec, err := s.store.GetRecord(ctx, item.RecordID)
		if err != nil {
			s.logger.Error("result_lookup_failed", "record_id", item.RecordID, "error", err.Error())
			return
		}
		newRetryCount := rec.RetryCount + 1
		finalStatus := shared.StatusPending
		if newRetryCount > shared.MaxRetries {
			finalStatus = shared.StatusFailed
		}
		err = s.store.UpdateRecordStatus(ctx, item.RecordID, finalStatus,
			hiringdb.WithRetryCount(newRetryCount), hiringdb.WithClearProcessedBy(),
			hiringdb.WithExpectedCurrentStatus(shared.StatusProcessing),
		)
		if err != nil {
			s.logger.Error("result_requeue_failed", "record_id", item.RecordID, "error", err.Error())
			return
		}
		s.logger.Warn("record_failed", "batch_id", batchID, "worker_id", workerID, "record_id", item.RecordID,
			"worker_error", item.Error, "new_status", finalStatus, "retry_count", newRetryCount)
	default:
		s.logger.Warn("unknown_result_status", "record_id", item.RecordID, "status", item.Status)
	}
}

func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.registry.Snapshot())
}
