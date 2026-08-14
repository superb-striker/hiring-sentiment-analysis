package hiringdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// updateParams accumulates what a set of UpdateOptions asked for. 
// Every field is a pointer (or bool flag) so "not set" is distinguishable from "set to the zero value"
type updateParams struct {
	company           *string
	role              *string
	stage             *string
	sentiment         *string
	complaintCategory *string 
	confidence        *float64
	isHiringRelated   *bool
	processedBy       *string
	clearProcessedBy  bool
	retryCount        *int
	expectedStatus    *string
}

// UpdateOption configures a single UpdateRecordStatus call - functional options pattern used. 
type UpdateOption func(*updateParams)

func WithCompany(v string) UpdateOption           { return func(p *updateParams) { p.company = &v } }
func WithRole(v string) UpdateOption              { return func(p *updateParams) { p.role = &v } }
func WithStage(v string) UpdateOption             { return func(p *updateParams) { p.stage = &v } }
func WithSentiment(v string) UpdateOption         { return func(p *updateParams) { p.sentiment = &v } }
func WithComplaintCategory(v string) UpdateOption { return func(p *updateParams) { p.complaintCategory = &v } }
func WithConfidence(v float64) UpdateOption       { return func(p *updateParams) { p.confidence = &v } }
func WithIsHiringRelated(v bool) UpdateOption     { return func(p *updateParams) { p.isHiringRelated = &v } }
func WithProcessedBy(v string) UpdateOption       { return func(p *updateParams) { p.processedBy = &v } }
func WithRetryCount(v int) UpdateOption           { return func(p *updateParams) { p.retryCount = &v } }

// WithClearProcessedBy removes processed_by entirely (used when a record is requeued and no longer belongs to the worker that had it).
func WithClearProcessedBy() UpdateOption {
	return func(p *updateParams) { p.clearProcessedBy = true }
}

// WithExpectedCurrentStatus adds a ConditionExpression guarding the write:
// it only applies if the record's status is still this value. Omitting it loses
// the optimistic-concurrency protection against a record having moved on
// (e.g. reassigned to another worker) between when the caller decided to
// update it and when this call runs.
func WithExpectedCurrentStatus(v string) UpdateOption {
	return func(p *updateParams) { p.expectedStatus = &v }
}

// UpdateRecordStatus applies whichever fields the given options set. 
// If WithCompany resolves to a value different from the record's current company, 
// this isn't a plain UpdateItem (DynamoDB won't let you change a partition-key attribute) —
// it's an atomic TransactWriteItems that puts the record under its new key and deletes it from the old one,
// so a crash between the two steps can't leave a duplicate or an orphan.
func (db *DB) UpdateRecordStatus(ctx context.Context, recordID string, status string, opts ...UpdateOption) error {
	var p updateParams
	for _, opt := range opts {
		opt(&p)
	}

	current, err := db.getByRecordID(ctx, recordID)
	if err != nil {
		return fmt.Errorf("hiringdb: resolve %s for update: %w", recordID, err)
	}

	targetCompany := current.Company
	if p.company != nil {
		targetCompany = *p.company
	}

	if targetCompany == current.Company {
		return db.updateInPlace(ctx, current, status, p)
	}
	return db.moveAndUpdate(ctx, current, targetCompany, status, p)
}

func (db *DB) updateInPlace(ctx context.Context, current Record, status string, p updateParams) error {
	setClauses := []string{"#s = :status"}
	removeClauses := []string{}
	names := map[string]string{"#s": "status"}
	values := map[string]types.AttributeValue{":status": &types.AttributeValueMemberS{Value: status}}

	if p.role != nil {
		names["#role"] = "role"
		setClauses = append(setClauses, "#role = :role")
		values[":role"] = &types.AttributeValueMemberS{Value: *p.role}
	}
	if p.stage != nil {
		names["#stage"] = "stage"
		setClauses = append(setClauses, "#stage = :stage")
		values[":stage"] = &types.AttributeValueMemberS{Value: *p.stage}
	}
	if p.sentiment != nil {
		names["#sentiment"] = "sentiment"
		setClauses = append(setClauses, "#sentiment = :sentiment")
		values[":sentiment"] = &types.AttributeValueMemberS{Value: *p.sentiment}
	}
	if p.complaintCategory != nil {
		names["#cc"] = "complaint_category"
		if *p.complaintCategory == "" {
			setClauses = append(setClauses, "#cc = :ccNull")
			values[":ccNull"] = &types.AttributeValueMemberNULL{Value: true}
		} else {
			setClauses = append(setClauses, "#cc = :cc")
			values[":cc"] = &types.AttributeValueMemberS{Value: *p.complaintCategory}
		}
	}
	if p.confidence != nil {
		names["#conf"] = "confidence"
		setClauses = append(setClauses, "#conf = :conf")
		values[":conf"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%.4f", *p.confidence)}
	}
	if p.isHiringRelated != nil {
		names["#ihr"] = "is_hiring_related"
		setClauses = append(setClauses, "#ihr = :ihr")
		values[":ihr"] = &types.AttributeValueMemberBOOL{Value: *p.isHiringRelated}
	}
	if p.processedBy != nil {
		setClauses = append(setClauses, "processed_by = :processedBy")
		values[":processedBy"] = &types.AttributeValueMemberS{Value: *p.processedBy}
	}
	if p.clearProcessedBy {
		removeClauses = append(removeClauses, "processed_by")
	}
	if p.retryCount != nil {
		names["#rc"] = "retry_count"
		setClauses = append(setClauses, "#rc = :rc")
		values[":rc"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", *p.retryCount)}
	}

	expr := "SET " + joinClauses(setClauses)
	if len(removeClauses) > 0 {
		expr += " REMOVE " + joinClauses(removeClauses)
	}

	var condition *string
	if p.expectedStatus != nil {
		condition = aws.String("#s = :expectedStatus")
		values[":expectedStatus"] = &types.AttributeValueMemberS{Value: *p.expectedStatus}
	}

	_, err := db.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(db.tableName),
		Key: map[string]types.AttributeValue{
			"company":   &types.AttributeValueMemberS{Value: current.Company},
			"timestamp": &types.AttributeValueMemberS{Value: current.Timestamp},
		},
		UpdateExpression:          aws.String(expr),
		ConditionExpression:       condition,
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if err != nil {
		return fmt.Errorf("hiringdb: update %s in place: %w", current.RecordID, err)
	}
	return nil
}

// moveAndUpdate atomically moves a record to a new partition key (newCompany) 
// while applying the same field updates updateInPlace would have.
// The delete side is conditioned on expectedStatus if the caller gave one,
// otherwise falls back to attribute_exists(record_id) so an already-vanished item can't be blindly deleted.
func (db *DB) moveAndUpdate(ctx context.Context, current Record, newCompany, status string, p updateParams) error {
	newItem := current
	newItem.Company = newCompany
	newItem.Status = status
	if p.role != nil {
		newItem.Role = p.role
	}
	if p.stage != nil {
		newItem.Stage = p.stage
	}
	if p.sentiment != nil {
		newItem.Sentiment = p.sentiment
	}
	if p.complaintCategory != nil {
		if *p.complaintCategory == "" {
			newItem.ComplaintCategory = nil
		} else {
			newItem.ComplaintCategory = p.complaintCategory
		}
	}
	if p.confidence != nil {
		newItem.Confidence = *p.confidence
	}
	if p.isHiringRelated != nil {
		newItem.IsHiringRelated = p.isHiringRelated
	}
	if p.processedBy != nil {
		newItem.ProcessedBy = p.processedBy
	}
	if p.clearProcessedBy {
		newItem.ProcessedBy = nil
	}
	if p.retryCount != nil {
		newItem.RetryCount = *p.retryCount
	}

	newItemAV, err := attributevalue.MarshalMap(newItem)
	if err != nil {
		return fmt.Errorf("hiringdb: marshal moved record %s: %w", current.RecordID, err)
	}

	deleteCondition := "attribute_exists(record_id)"
	deleteValues := map[string]types.AttributeValue{}
	deleteNames := map[string]string{}
	if p.expectedStatus != nil {
		deleteCondition = "#s = :expectedStatus"
		deleteNames["#s"] = "status"
		deleteValues[":expectedStatus"] = &types.AttributeValueMemberS{Value: *p.expectedStatus}
	}

	_, err = db.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           aws.String(db.tableName),
					Item:                newItemAV,
					ConditionExpression: aws.String("attribute_not_exists(company) AND attribute_not_exists(#ts)"),
					ExpressionAttributeNames: map[string]string{
						"#ts": "timestamp",
					},
				},
			},
			{
				Delete: &types.Delete{
					TableName: aws.String(db.tableName),
					Key: map[string]types.AttributeValue{
						"company":   &types.AttributeValueMemberS{Value: current.Company},
						"timestamp": &types.AttributeValueMemberS{Value: current.Timestamp},
					},
					ConditionExpression:       aws.String(deleteCondition),
					ExpressionAttributeNames:  nonEmptyOrNil(deleteNames),
					ExpressionAttributeValues: nonEmptyOrNilValues(deleteValues),
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("hiringdb: move+update %s to company=%s: %w", current.RecordID, newCompany, err)
	}
	return nil
}

func joinClauses(clauses []string) string {
	var b strings.Builder
	for i, c := range clauses {
		b.WriteString(c)
		if i < (len(clauses) - 1) {
			b.WriteString(", ")
		}
	}
	out := b.String()
	return out
}

func nonEmptyOrNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func nonEmptyOrNilValues(m map[string]types.AttributeValue) map[string]types.AttributeValue {
	if len(m) == 0 {
		return nil
	}
	return m
}
