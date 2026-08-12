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

// QueryAllByStatus streams every record with the given status through fn, one page at a time, 
// following DynamoDB's LastEvaluatedKey pagination internally. fn is called once per page (up to pageSize records); 
// returning an error from fn stops iteration and QueryAllByStatus returns that error.
func (db *DB) QueryAllByStatus(ctx context.Context, status string, pageSize int32, fn func([]Record) error) error {
	var exclusiveStartKey map[string]types.AttributeValue

	for {
		out, err := db.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(db.tableName),
			IndexName:              aws.String(shared.StatusIndex),
			KeyConditionExpression: aws.String("#s = :status"),
			ExpressionAttributeNames: map[string]string{
				"#s": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":status": &types.AttributeValueMemberS{Value: status},
			},
			Limit:             aws.Int32(pageSize),
			ExclusiveStartKey: exclusiveStartKey,
		})
		if err != nil {
			return fmt.Errorf("hiringdb: query %s for status=%s: %w", shared.StatusIndex, status, err)
		}

		if len(out.Items) > 0 {
			records := make([]Record, 0, len(out.Items))
			for _, item := range out.Items {
				var r Record
				if err := attributevalue.UnmarshalMap(item, &r); err != nil {
					return fmt.Errorf("hiringdb: unmarshal record: %w", err)
				}
				records = append(records, r)
			}
			if err := fn(records); err != nil {
				return err
			}
		}

		if len(out.LastEvaluatedKey) == 0 {
			return nil
		}
		exclusiveStartKey = out.LastEvaluatedKey
	}
}
