package hiringdb

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"hiring-sentiment/shared"
)

// QueryPendingRecords pulls up to `limit` pending records off the status-index GSI, 
// oldest first (so the coordinator's round-robin assignment naturally processes the backlog in arrival order).
func (db *DB) QueryPendingRecords(ctx context.Context, limit int) ([]Record, error) {
	out, err := db.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(db.tableName),
		IndexName:              aws.String(shared.StatusIndex),
		KeyConditionExpression: aws.String("#s = :pending"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending": &types.AttributeValueMemberS{Value: shared.StatusPending},
		},
		Limit:            aws.Int32(int32(limit)),
		ScanIndexForward: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("hiringdb: query %s pending: %w", shared.StatusIndex, err)
	}

	records := make([]Record, 0, len(out.Items))
	for _, item := range out.Items {
		var r Record
		if err := attributevalue.UnmarshalMap(item, &r); err != nil {
			return nil, fmt.Errorf("hiringdb: unmarshal pending record: %w", err)
		}
		records = append(records, r)
	}
	return records, nil
}
