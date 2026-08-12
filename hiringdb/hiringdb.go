package hiringdb

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"hiring-sentiment/shared"
)

// Record is the hiring_records row. 
type Record = shared.HiringRecord

// dynamoAPI is the narrow slice of the DynamoDB client this package actually calls 
type dynamoAPI interface {
	Query(
		ctx context.Context,
		in *dynamodb.QueryInput,
		opts ...func(*dynamodb.Options),
	) (*dynamodb.QueryOutput, error)
	PutItem(
		ctx context.Context,
		in *dynamodb.PutItemInput,
		opts ...func(*dynamodb.Options),
	) (*dynamodb.PutItemOutput, error)
	UpdateItem(
		ctx context.Context,
		in *dynamodb.UpdateItemInput,
		opts ...func(*dynamodb.Options),
	) (*dynamodb.UpdateItemOutput, error)
	TransactWriteItems(
		ctx context.Context,
		in *dynamodb.TransactWriteItemsInput,
		opts ...func(*dynamodb.Options),
	) (*dynamodb.TransactWriteItemsOutput, error)
}

// DB is the hiringdb client. Construct with New, passing a real *dynamodb.Client
type DB struct {
	client    dynamoAPI
	tableName string
}

// New constructs a DB against the given table. 
func New(client dynamoAPI, tableName string) *DB {
	return &DB{client: client, tableName: tableName}
}

// ErrCollision is returned by PutPendingRecord when another record already occupies the same (company, timestamp) key 
// ErrNotFound is returned by GetRecord/UpdateRecordStatus when no record with the given record_id exists.
var (
	ErrCollision = errors.New("hiringdb: company+timestamp key collision")
	ErrNotFound  = errors.New("hiringdb: record_id not found")
)

