package hiringdb

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type stubDynamo struct {
	queryOutput  *dynamodb.QueryOutput
	queryOutputs []*dynamodb.QueryOutput // if set, takes precedence; one per successive Query call
	queryCalls   int
	queryErr     error

	putErr error

	updateErr       error
	updateCalls     int
	lastUpdateInput *dynamodb.UpdateItemInput

	transactErr   error
	transactCalls int
	lastTransact  *dynamodb.TransactWriteItemsInput
}

func (s *stubDynamo) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	if s.queryOutputs != nil {
		out := s.queryOutputs[s.queryCalls]
		s.queryCalls++
		return out, nil
	}
	return s.queryOutput, nil
}

func (s *stubDynamo) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if s.putErr != nil {
		return nil, s.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (s *stubDynamo) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	s.updateCalls++
	s.lastUpdateInput = in
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (s *stubDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	s.transactCalls++
	s.lastTransact = in
	if s.transactErr != nil {
		return nil, s.transactErr
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func attrS(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func recordItem(company, timestamp, recordID, status string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"company": attrS(company), "timestamp": attrS(timestamp),
		"record_id": attrS(recordID), "status": attrS(status),
	}
}

func TestPutPendingRecord(t *testing.T) {
	t.Run("success forces status to pending", func(t *testing.T) {
		stub := &stubDynamo{}
		db := New(stub, "hiring_records")
		err := db.PutPendingRecord(context.Background(), Record{
			Company: "unknown", Timestamp: "2026-09-12T10:00:00Z", RecordID: "t3_a", Status: "garbage",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("collision maps to ErrCollision", func(t *testing.T) {
		stub := &stubDynamo{putErr: &types.ConditionalCheckFailedException{}}
		db := New(stub, "hiring_records")
		err := db.PutPendingRecord(context.Background(), Record{Company: "unknown", Timestamp: "2026-09-12T10:00:00Z", RecordID: "t3_dup"})
		if !errors.Is(err, ErrCollision) {
			t.Fatalf("expected ErrCollision, got %v", err)
		}
	})
}

func TestRecordExists(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{
			Items: []map[string]types.AttributeValue{recordItem("unknown", "2026-09-12T10:00:00Z", "t3_a", "pending")},
		}}
		db := New(stub, "hiring_records")
		exists, err := db.RecordExists(context.Background(), "t3_a")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected exists = true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{Items: nil}}
		db := New(stub, "hiring_records")
		exists, err := db.RecordExists(context.Background(), "t3_missing")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Error("expected exists = false")
		}
	})
}

func TestGetRecord_NotFound(t *testing.T) {
	stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{Items: nil}}
	db := New(stub, "hiring_records")
	_, err := db.GetRecord(context.Background(), "t3_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateRecordStatus_SameCompany_UpdatesInPlace(t *testing.T) {
	stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{
		Items: []map[string]types.AttributeValue{recordItem("unknown", "2026-09-12T10:00:00Z", "t3_a", "processing")},
	}}
	db := New(stub, "hiring_records")

	err := db.UpdateRecordStatus(context.Background(), "t3_a", "complete",
		WithStage("ghosted"), WithSentiment("frustrated"), WithComplaintCategory("ghosting"),
		WithConfidence(0.9), WithExpectedCurrentStatus("processing"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.updateCalls != 1 {
		t.Errorf("expected 1 UpdateItem call, got %d", stub.updateCalls)
	}
	if stub.transactCalls != 0 {
		t.Errorf("expected 0 TransactWriteItems calls when company is unchanged, got %d", stub.transactCalls)
	}
}

func TestUpdateRecordStatus_CompanyChanged_MovesViaTransaction(t *testing.T) {
	stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{
		Items: []map[string]types.AttributeValue{recordItem("unknown", "2026-09-12T10:00:00Z", "t3_a", "processing")},
	}}
	db := New(stub, "hiring_records")

	err := db.UpdateRecordStatus(context.Background(), "t3_a", "complete",
		WithCompany("amazon"), WithRole("software engineer"), WithStage("phone_screen"),
		WithSentiment("hopeful"), WithConfidence(0.85), WithExpectedCurrentStatus("processing"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.updateCalls != 0 {
		t.Errorf("expected 0 UpdateItem calls when company changes, got %d", stub.updateCalls)
	}
	if stub.transactCalls != 1 {
		t.Fatalf("expected 1 TransactWriteItems call, got %d", stub.transactCalls)
	}

	items := stub.lastTransact.TransactItems
	if len(items) != 2 || items[0].Put == nil || items[1].Delete == nil {
		t.Fatalf("expected exactly [Put, Delete], got %+v", items)
	}
	putCompany := items[0].Put.Item["company"].(*types.AttributeValueMemberS).Value
	if putCompany != "amazon" {
		t.Errorf("expected put item company = amazon, got %q", putCompany)
	}
	deleteCompany := items[1].Delete.Key["company"].(*types.AttributeValueMemberS).Value
	if deleteCompany != "unknown" {
		t.Errorf("expected delete key company = unknown (the ORIGINAL key), got %q", deleteCompany)
	}
}

func TestUpdateRecordStatus_RetryCountAndClearProcessedBy(t *testing.T) {
	stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{
		Items: []map[string]types.AttributeValue{recordItem("unknown", "2026-09-12T10:00:00Z", "t3_a", "processing")},
	}}
	db := New(stub, "hiring_records")

	err := db.UpdateRecordStatus(context.Background(), "t3_a", "pending",
		WithRetryCount(2), WithClearProcessedBy(), WithExpectedCurrentStatus("processing"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.updateCalls != 1 {
		t.Errorf("expected 1 UpdateItem call, got %d", stub.updateCalls)
	}
}

func TestUpdateRecordStatus_IsHiringRelatedWritesBoolAttribute(t *testing.T) {
	stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{
		Items: []map[string]types.AttributeValue{recordItem("unknown", "2026-09-12T10:00:00Z", "t3_offtopic", "processing")},
	}}
	db := New(stub, "hiring_records")

	err := db.UpdateRecordStatus(context.Background(), "t3_offtopic", "complete",
		WithIsHiringRelated(false), WithStage("unknown"), WithSentiment("neutral"),
		WithExpectedCurrentStatus("processing"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastUpdateInput == nil {
		t.Fatal("expected UpdateItem to be called")
	}
	val, ok := stub.lastUpdateInput.ExpressionAttributeValues[":ihr"].(*types.AttributeValueMemberBOOL)
	if !ok {
		t.Fatalf("expected :ihr to be a BOOL attribute value, got %T", stub.lastUpdateInput.ExpressionAttributeValues[":ihr"])
	}
	if val.Value != false {
		t.Errorf("expected is_hiring_related=false to be written, got %v", val.Value)
	}
}

func TestUpdateRecordStatus_UnresolvableRecord(t *testing.T) {
	stub := &stubDynamo{queryOutput: &dynamodb.QueryOutput{Items: nil}}
	db := New(stub, "hiring_records")

	err := db.UpdateRecordStatus(context.Background(), "t3_missing", "failed")
	if err == nil {
		t.Fatal("expected an error for an unresolvable record_id")
	}
}

func TestQueryAllByStatus_FollowsPagination(t *testing.T) {
	page1 := &dynamodb.QueryOutput{
		Items:            []map[string]types.AttributeValue{recordItem("amazon", "2026-09-01T00:00:00Z", "t3_a", "complete")},
		LastEvaluatedKey: map[string]types.AttributeValue{"company": attrS("amazon")}, // non-empty -> there's a page 2
	}
	page2 := &dynamodb.QueryOutput{
		Items:            []map[string]types.AttributeValue{recordItem("google", "2026-09-02T00:00:00Z", "t3_b", "complete")},
		LastEvaluatedKey: nil, // done
	}
	stub := &stubDynamo{queryOutputs: []*dynamodb.QueryOutput{page1, page2}}
	db := New(stub, "hiring_records")

	var seen []string
	err := db.QueryAllByStatus(context.Background(), "complete", 1, func(records []Record) error {
		for _, r := range records {
			seen = append(seen, r.RecordID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != 2 || seen[0] != "t3_a" || seen[1] != "t3_b" {
		t.Errorf("expected both pages visited in order, got %v", seen)
	}
	if stub.queryCalls != 2 {
		t.Errorf("expected exactly 2 Query calls (one per page), got %d", stub.queryCalls)
	}
}

func TestQueryAllByStatus_PropagatesCallbackError(t *testing.T) {
	page1 := &dynamodb.QueryOutput{
		Items:            []map[string]types.AttributeValue{recordItem("amazon", "2026-09-01T00:00:00Z", "t3_a", "complete")},
		LastEvaluatedKey: map[string]types.AttributeValue{"company": attrS("amazon")},
	}
	stub := &stubDynamo{queryOutputs: []*dynamodb.QueryOutput{page1, {}}}
	db := New(stub, "hiring_records")

	boom := errors.New("boom")
	err := db.QueryAllByStatus(context.Background(), "complete", 1, func(records []Record) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected callback error to propagate, got %v", err)
	}
	if stub.queryCalls != 1 {
		t.Errorf("expected iteration to stop after the callback error, got %d calls", stub.queryCalls)
	}
}
