package hiringdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"hiring-sentiment/shared"
)

// PutPendingRecord writes a new record with status forced to "pending". Returns ErrCollision on a collision.
func (db *DB) PutPendingRecord(ctx context.Context, record Record) error {
	record.Status = shared.StatusPending

	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("hiringdb: marshal record %s: %w", record.RecordID, err)
	}

	_, err = db.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(db.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(company) AND attribute_not_exists(#ts)"),
		ExpressionAttributeNames: map[string]string{
			"#ts": "timestamp",
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return ErrCollision
		}
		return fmt.Errorf("hiringdb: put pending record %s: %w", record.RecordID, err)
	}
	return nil
}

func (db *DB) getByRecordID(ctx context.Context, recordID string) (Record, error) {
	out, err := db.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(db.tableName),
		IndexName:              aws.String(shared.RecordIDIndex),
		KeyConditionExpression: aws.String("record_id = :rid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":rid": &types.AttributeValueMemberS{Value: recordID},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return Record{}, fmt.Errorf("hiringdb: query %s for %s: %w", shared.RecordIDIndex, recordID, err)
	}
	if len(out.Items) == 0 {
		return Record{}, ErrNotFound
	}
	var r Record
	if err := attributevalue.UnmarshalMap(out.Items[0], &r); err != nil {
		return Record{}, fmt.Errorf("hiringdb: unmarshal record %s: %w", recordID, err)
	}
	return r, nil
}

// GetRecord fetches a record by its Reddit record_id.
func (db *DB) GetRecord(ctx context.Context, recordID string) (Record, error) {
	return db.getByRecordID(ctx, recordID)
}

// RecordExists reports whether a record_id has already been written - for ingestion's dedup check.
func (db *DB) RecordExists(ctx context.Context, recordID string) (bool, error) {
	_, err := db.getByRecordID(ctx, recordID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
