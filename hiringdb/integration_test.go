// Integration tests for hiringdb, run against a REAL local DynamoDB (dynamodb-local) 
// Skipped by default (no docker/network dependency for `go test ./...` in CI or a quick local run).
// To actually run them:
//	docker run -d -p 8000:8000 amazon/dynamodb-local:latest
//	cd ../infra/terraform && terraform apply -var="dynamodb_endpoint=http://localhost:8000" -auto-approve && cd -
//	DYNAMODB_ENDPOINT=http://localhost:8000 go test ./... -run Integration -v
package hiringdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"hiring-sentiment/shared"
)

func integrationDB(t *testing.T) *DB {
	t.Helper()
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set; skipping integration test (see file header for how to run these)")
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	return New(client, shared.TableName)
}

// uniqueRecordID keeps repeated test runs against a persistent (non-inMemory) local table from colliding with leftover data.
func uniqueRecordID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func TestIntegration_PutAndGetAndExists(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	recordID := uniqueRecordID("t3_put")

	err := db.PutPendingRecord(ctx, Record{
		Company: "unknown", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RecordID: recordID, SourceSubreddit: "jobs", RawText: "integration test record",
	})
	if err != nil {
		t.Fatalf("PutPendingRecord: %v", err)
	}

	exists, err := db.RecordExists(ctx, recordID)
	if err != nil {
		t.Fatalf("RecordExists: %v", err)
	}
	if !exists {
		t.Fatal("expected record to exist after PutPendingRecord")
	}

	rec, err := db.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Status != shared.StatusPending {
		t.Errorf("expected status pending, got %q", rec.Status)
	}
	if rec.RawText != "integration test record" {
		t.Errorf("unexpected raw_text: %q", rec.RawText)
	}
}

func TestIntegration_PutPendingRecord_Collision(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	first := Record{Company: "unknown", Timestamp: ts, RecordID: uniqueRecordID("t3_first")}
	second := Record{Company: "unknown", Timestamp: ts, RecordID: uniqueRecordID("t3_second")} // same key, different record_id

	if err := db.PutPendingRecord(ctx, first); err != nil {
		t.Fatalf("first PutPendingRecord: %v", err)
	}
	err := db.PutPendingRecord(ctx, second)
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("expected ErrCollision on same (company, timestamp) key, got %v", err)
	}
}

func TestIntegration_RecordExists_NotFound(t *testing.T) {
	db := integrationDB(t)
	exists, err := db.RecordExists(context.Background(), uniqueRecordID("t3_never_written"))
	if err != nil {
		t.Fatalf("RecordExists: %v", err)
	}
	if exists {
		t.Error("expected exists = false for a record_id that was never written")
	}
}

func TestIntegration_QueryPendingRecords(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	recordID := uniqueRecordID("t3_pending")

	err := db.PutPendingRecord(ctx, Record{
		Company: "unknown", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RecordID: recordID, SourceSubreddit: "cscareerquestions", RawText: "another integration test record",
	})
	if err != nil {
		t.Fatalf("PutPendingRecord: %v", err)
	}

	records, err := db.QueryPendingRecords(ctx, 200)
	if err != nil {
		t.Fatalf("QueryPendingRecords: %v", err)
	}
	found := false
	for _, r := range records {
		if r.RecordID == recordID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %s to appear in QueryPendingRecords results", recordID)
	}
}

func TestIntegration_UpdateRecordStatus_InPlace(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	recordID := uniqueRecordID("t3_inplace")

	if err := db.PutPendingRecord(ctx, Record{
		Company: "unknown", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RecordID: recordID, RawText: "ghosted after onsite",
	}); err != nil {
		t.Fatalf("PutPendingRecord: %v", err)
	}

	err := db.UpdateRecordStatus(ctx, recordID, shared.StatusComplete,
		WithStage("ghosted"), WithSentiment("frustrated"), WithComplaintCategory("ghosting"), WithConfidence(0.9),
		WithExpectedCurrentStatus(shared.StatusPending),
	)
	if err != nil {
		t.Fatalf("UpdateRecordStatus: %v", err)
	}

	rec, err := db.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Company != "unknown" {
		t.Errorf("expected company to remain unknown, got %q", rec.Company)
	}
	if rec.Status != shared.StatusComplete {
		t.Errorf("expected status complete, got %q", rec.Status)
	}
	if rec.Stage == nil || *rec.Stage != "ghosted" {
		t.Errorf("expected stage=ghosted, got %v", rec.Stage)
	}
}

func TestIntegration_UpdateRecordStatus_MovesPartitionKey(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	recordID := uniqueRecordID("t3_move")

	if err := db.PutPendingRecord(ctx, Record{
		Company: "unknown", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RecordID: recordID, RawText: "got an offer from amazon",
	}); err != nil {
		t.Fatalf("PutPendingRecord: %v", err)
	}

	err := db.UpdateRecordStatus(ctx, recordID, shared.StatusComplete,
		WithCompany("amazon"), WithRole("software engineer"), WithStage("offer"), WithSentiment("celebratory"),
		WithConfidence(0.95), WithExpectedCurrentStatus(shared.StatusPending),
	)
	if err != nil {
		t.Fatalf("UpdateRecordStatus: %v", err)
	}

	rec, err := db.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord after move: %v", err)
	}
	if rec.Company != "amazon" {
		t.Errorf("expected record to have moved to company=amazon, got %q", rec.Company)
	}
	if rec.Status != shared.StatusComplete {
		t.Errorf("expected status complete, got %q", rec.Status)
	}
}

func TestIntegration_UpdateRecordStatus_ConditionFailsOnWrongExpectedStatus(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	recordID := uniqueRecordID("t3_condition")

	if err := db.PutPendingRecord(ctx, Record{
		Company: "unknown", Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RecordID: recordID, RawText: "some record",
	}); err != nil {
		t.Fatalf("PutPendingRecord: %v", err)
	}

	// Record is actually "pending", but we claim to expect "processing" - the conditional update should fail.
	err := db.UpdateRecordStatus(ctx, recordID, shared.StatusFailed, WithExpectedCurrentStatus(shared.StatusProcessing))
	if err == nil {
		t.Fatal("expected an error when the expected-status condition doesn't match")
	}
}
