package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"hiring-sentiment/coordinator/internal/registry"
	"hiring-sentiment/hiringdb"
	"hiring-sentiment/shared"
)

type stubResultStore struct {
	getResult     hiringdb.Record
	getErr        error
	updateErr     error
	updateCalls   int
	lastRecordID  string
	lastStatus    string
}

func (s *stubResultStore) GetRecord(_ context.Context, recordID string) (hiringdb.Record, error) {
	if s.getErr != nil {
		return hiringdb.Record{}, s.getErr
	}
	return s.getResult, nil
}

func (s *stubResultStore) UpdateRecordStatus(_ context.Context, recordID string, status string, _ ...hiringdb.UpdateOption) error {
	s.updateCalls++
	s.lastRecordID = recordID
	s.lastStatus = status
	return s.updateErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1})) // silence in tests
}

func TestHandleHeartbeat(t *testing.T) {
	reg := registry.New(map[string]string{"worker_1": "http://worker1"})
	srv := NewServer(reg, &stubResultStore{}, testLogger())

	body, _ := json.Marshal(shared.Heartbeat{WorkerID: "worker_1", Status: "idle", Timestamp: time.Now().UTC().Format(time.RFC3339)})
	req := httptest.NewRequest("POST", "/heartbeat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	snap := reg.Snapshot()
	if len(snap) != 1 || snap[0].ReportedStatus != "idle" {
		t.Errorf("expected worker_1 idle in registry, got %+v", snap)
	}
}

func TestHandleHeartbeat_MissingWorkerID(t *testing.T) {
	reg := registry.New(map[string]string{})
	srv := NewServer(reg, &stubResultStore{}, testLogger())

	body, _ := json.Marshal(shared.Heartbeat{Status: "idle"})
	req := httptest.NewRequest("POST", "/heartbeat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("expected 400 for missing worker_id, got %d", rec.Code)
	}
}

func TestHandleResult_Complete_DoesNotTouchStore(t *testing.T) {
	reg := registry.New(map[string]string{"worker_1": "http://worker1"})
	reg.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "busy", CurrentBatch: "batch_1"}, time.Now().UTC())
	reg.Assign("worker_1", "batch_1", []string{"t3_a"}, time.Now().UTC())

	stub := &stubResultStore{}
	srv := NewServer(reg, stub, testLogger())

	result := shared.TaskResult{
		BatchID: "batch_1", WorkerID: "worker_1",
		Results: []shared.TaskResultItem{{RecordID: "t3_a", Status: shared.StatusComplete}},
	}
	body, _ := json.Marshal(result)
	req := httptest.NewRequest("POST", "/result", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if stub.updateCalls != 0 {
		t.Errorf("expected no UpdateRecordStatus calls for a complete result, got %d", stub.updateCalls)
	}
	snap := reg.Snapshot()
	if snap[0].Current != nil {
		t.Error("expected worker's current batch to be cleared after /result")
	}
}

func TestHandleResult_Failed_TriggersRequeue(t *testing.T) {
	reg := registry.New(map[string]string{"worker_1": "http://worker1"})
	reg.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "busy", CurrentBatch: "batch_1"}, time.Now().UTC())
	reg.Assign("worker_1", "batch_1", []string{"t3_a"}, time.Now().UTC())

	stub := &stubResultStore{getResult: hiringdb.Record{Company: "unknown", Timestamp: "2026-09-12T10:00:00Z", RecordID: "t3_a", RetryCount: 0}}
	srv := NewServer(reg, stub, testLogger())

	result := shared.TaskResult{
		BatchID: "batch_1", WorkerID: "worker_1",
		Results: []shared.TaskResultItem{{RecordID: "t3_a", Status: shared.StatusFailed, Error: "llm timeout"}},
	}
	body, _ := json.Marshal(result)
	req := httptest.NewRequest("POST", "/result", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if stub.updateCalls != 1 {
		t.Fatalf("expected exactly 1 UpdateRecordStatus call, got %d", stub.updateCalls)
	}
	if stub.lastRecordID != "t3_a" {
		t.Errorf("requeued wrong record: %q", stub.lastRecordID)
	}
	if stub.lastStatus != shared.StatusPending {
		t.Errorf("expected first failure to go back to pending, got %q", stub.lastStatus)
	}
}

func TestHandleResult_Failed_ExceedsRetries_MarksFailed(t *testing.T) {
	reg := registry.New(map[string]string{"worker_1": "http://worker1"})
	reg.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "busy", CurrentBatch: "batch_1"}, time.Now().UTC())
	reg.Assign("worker_1", "batch_1", []string{"t3_a"}, time.Now().UTC())

	stub := &stubResultStore{getResult: hiringdb.Record{Company: "unknown", Timestamp: "2026-09-12T10:00:00Z", RecordID: "t3_a", RetryCount: shared.MaxRetries}}
	srv := NewServer(reg, stub, testLogger())

	result := shared.TaskResult{
		BatchID: "batch_1", WorkerID: "worker_1",
		Results: []shared.TaskResultItem{{RecordID: "t3_a", Status: shared.StatusFailed, Error: "llm timeout"}},
	}
	body, _ := json.Marshal(result)
	req := httptest.NewRequest("POST", "/result", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if stub.lastStatus != shared.StatusFailed {
		t.Errorf("expected retry_count exceeding MaxRetries to mark failed, got %q", stub.lastStatus)
	}
}

func TestHandleWorkers_ReturnsSnapshot(t *testing.T) {
	reg := registry.New(map[string]string{"worker_1": "http://worker1"})
	reg.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "idle"}, time.Now().UTC())
	srv := NewServer(reg, &stubResultStore{}, testLogger())

	req := httptest.NewRequest("GET", "/workers", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var workers []registry.Worker
	if err := json.Unmarshal(rec.Body.Bytes(), &workers); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(workers) != 1 || workers[0].ID != "worker_1" {
		t.Errorf("unexpected workers response: %+v", workers)
	}
}

func TestHandleHealth(t *testing.T) {
	reg := registry.New(map[string]string{})
	srv := NewServer(reg, &stubResultStore{}, testLogger())

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}
