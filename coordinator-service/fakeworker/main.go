// A minimal worker stub for exercising the coordinator's assignment and failure-detection logic without real classification.
// It accepts /assign, "processes" the batch after a configurable delay, and reports results.
// Set FAIL_RATE (0.0-1.0) to simulate per-record failures, or
// STOP_HEARTBEAT=true to simulate a dead worker (coordinator should requeue its batch after ~30s).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"hiring-sentiment/shared"
)

func main() {
	workerID := getEnv("WORKER_ID", "worker_1")
	coordinatorURL := getEnv("COORDINATOR_URL", "http://localhost:8080")
	port := getEnv("PORT", "9001")
	failRate, _ := strconv.ParseFloat(getEnv("FAIL_RATE", "0"), 64)
	stopHeartbeat := getEnv("STOP_HEARTBEAT", "false") == "true"
	processingDelay := 2 * time.Second

	var mu sync.Mutex
	currentBatch := ""

	go func() {
		if stopHeartbeat {
			fmt.Printf("[%s] heartbeats disabled via STOP_HEARTBEAT\n", workerID)
			return
		}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			batch := currentBatch
			status := "idle"
			if batch != "" {
				status = "busy"
			}
			mu.Unlock()

			hb := shared.Heartbeat{WorkerID: workerID, Status: status, Timestamp: time.Now().UTC().Format(time.RFC3339), CurrentBatch: batch}
			postJSON(coordinatorURL+"/heartbeat", hb)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /assign", func(w http.ResponseWriter, r *http.Request) {
		var assignment shared.TaskAssignment
		if err := json.NewDecoder(r.Body).Decode(&assignment); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		currentBatch = assignment.BatchID
		mu.Unlock()

		fmt.Printf("[%s] assigned %s with %d records\n", workerID, assignment.BatchID, len(assignment.RecordIDs))
		w.WriteHeader(http.StatusAccepted)

		go func() {
			time.Sleep(processingDelay)
			results := make([]shared.TaskResultItem, 0, len(assignment.RecordIDs))
			for _, rid := range assignment.RecordIDs {
				if rand.Float64() < failRate {
					results = append(results, shared.TaskResultItem{RecordID: rid, Status: shared.StatusFailed, Error: "simulated LLM timeout"})
				} else {
					results = append(results, shared.TaskResultItem{RecordID: rid, Status: shared.StatusComplete})
				}
			}
			postJSON(coordinatorURL+"/result", shared.TaskResult{BatchID: assignment.BatchID, WorkerID: workerID, Results: results})

			mu.Lock()
			currentBatch = ""
			mu.Unlock()
			fmt.Printf("[%s] completed %s\n", workerID, assignment.BatchID)
		}()
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	fmt.Printf("[%s] listening on :%s, posting heartbeats to %s\n", workerID, port, coordinatorURL)
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func postJSON(url string, payload any) {
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "post %s failed: %v\n", url, err)
		return
	}
	resp.Body.Close()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
