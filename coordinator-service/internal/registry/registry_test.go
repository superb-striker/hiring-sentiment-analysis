package registry

import (
	"testing"
	"time"

	"hiring-sentiment/shared"
)

func TestParseWorkerURLs(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "two workers",
			raw:  "worker_1@http://worker1:8080,worker_2@http://worker2:8080",
			want: map[string]string{"worker_1": "http://worker1:8080", "worker_2": "http://worker2:8080"},
		},
		{
			name: "single worker with surrounding whitespace",
			raw:  " worker_1@http://worker1:8080 ",
			want: map[string]string{"worker_1": "http://worker1:8080"},
		},
		{name: "empty string is an error", raw: "", wantErr: true},
		{name: "missing @ is an error", raw: "http://worker1:8080", wantErr: true},
		{name: "missing url half is an error", raw: "worker_1@", wantErr: true},
		{name: "missing id half is an error", raw: "@http://worker1:8080", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseWorkerURLs(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseWorkerURLs(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d workers, want %d", len(got), len(tc.want))
			}
			for id, url := range tc.want {
				if got[id] != url {
					t.Errorf("worker %q url = %q, want %q", id, got[id], url)
				}
			}
		})
	}
}

func TestWorker_IsAvailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		w    Worker
		want bool
	}{
		{
			name: "idle and recently heartbeated is available",
			w:    Worker{URL: "http://x", ReportedStatus: "idle", LastHeartbeat: now.Add(-5 * time.Second)},
			want: true,
		},
		{
			name: "no URL configured is never available",
			w:    Worker{ReportedStatus: "idle", LastHeartbeat: now.Add(-5 * time.Second)},
			want: false,
		},
		{
			name: "marked dead is never available",
			w:    Worker{URL: "http://x", Dead: true, ReportedStatus: "idle", LastHeartbeat: now.Add(-5 * time.Second)},
			want: false,
		},
		{
			name: "stale heartbeat is not available",
			w:    Worker{URL: "http://x", ReportedStatus: "idle", LastHeartbeat: now.Add(-31 * time.Second)},
			want: false,
		},
		{
			name: "exactly at TTL boundary is still available",
			w:    Worker{URL: "http://x", ReportedStatus: "idle", LastHeartbeat: now.Add(-shared.HeartbeatTTL)},
			want: true,
		},
		{
			name: "busy with a current batch is not available",
			w:    Worker{URL: "http://x", ReportedStatus: "busy", LastHeartbeat: now.Add(-1 * time.Second), Current: &BatchAssignment{BatchID: "b1"}},
			want: false,
		},
		{
			name: "never heartbeated (zero time) is not available",
			w:    Worker{URL: "http://x", ReportedStatus: "idle"},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.IsAvailable(now); got != tc.want {
				t.Errorf("IsAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRegistry_PickAvailable_RoundRobin(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	r := New(map[string]string{"worker_1": "http://w1", "worker_2": "http://w2"})
	r.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "idle"}, now)
	r.Heartbeat(shared.Heartbeat{WorkerID: "worker_2", Status: "idle"}, now)

	first := r.PickAvailable(now)
	if first == nil {
		t.Fatal("expected a worker, got nil")
	}
	// Assign it so it's no longer idle, then the next pick must be the other one.
	r.Assign(first.ID, "batch_1", []string{"t3_a"}, now)

	second := r.PickAvailable(now)
	if second == nil {
		t.Fatal("expected a worker, got nil")
	}
	if second.ID == first.ID {
		t.Errorf("expected round-robin to pick a different worker, got %q both times", first.ID)
	}
}

func TestRegistry_PickAvailable_NoneAvailable(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	r := New(map[string]string{"worker_1": "http://w1"})
	// No heartbeat sent yet -worker_1 should not be pickable.
	if got := r.PickAvailable(now); got != nil {
		t.Errorf("expected nil, got worker %q", got.ID)
	}
}

func TestRegistry_SweepDead_RequeuesOrphanedBatch(t *testing.T) {
	start := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	r := New(map[string]string{"worker_1": "http://w1"})
	r.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "busy", CurrentBatch: "batch_1"}, start)
	r.Assign("worker_1", "batch_1", []string{"t3_a", "t3_b"}, start)

	later := start.Add(31 * time.Second)
	orphaned := r.SweepDead(later)

	if len(orphaned) != 1 {
		t.Fatalf("expected 1 orphaned batch, got %d", len(orphaned))
	}
	if orphaned[0].BatchID != "batch_1" || len(orphaned[0].RecordIDs) != 2 {
		t.Errorf("unexpected orphaned batch: %+v", orphaned[0])
	}

	// A second sweep shouldn't re-report the same worker as newly dead.
	again := r.SweepDead(later.Add(time.Second))
	if len(again) != 0 {
		t.Errorf("expected no new orphans on second sweep, got %d", len(again))
	}
}

func TestRegistry_Unassign(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	r := New(map[string]string{"worker_1": "http://w1"})
	r.Heartbeat(shared.Heartbeat{WorkerID: "worker_1", Status: "idle"}, now)
	r.Assign("worker_1", "batch_1", []string{"t3_a"}, now)

	r.Unassign("worker_1")

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 worker in snapshot, got %d", len(snap))
	}
	if snap[0].Current != nil {
		t.Error("expected Current to be cleared after Unassign")
	}
	if snap[0].ReportedStatus != "idle" {
		t.Errorf("expected status idle after Unassign, got %q", snap[0].ReportedStatus)
	}
}
