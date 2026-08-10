package contracts

// HiringRecord mirrors the hiring_records DynamoDB item. Not every service
// populates every field - ingestion writes company/timestamp/record_id/
// source_subreddit/raw_text/status only; the worker fills in the rest.
type HiringRecord struct {
	Company           string  `dynamodbav:"company"`
	Timestamp         string  `dynamodbav:"timestamp"`
	RecordID          string  `dynamodbav:"record_id"`
	SourceSubreddit   string  `dynamodbav:"source_subreddit"`
	RawText           string  `dynamodbav:"raw_text"`
	Role              *string `dynamodbav:"role,omitempty"`
	Stage             *string `dynamodbav:"stage,omitempty"`
	Sentiment         *string `dynamodbav:"sentiment,omitempty"`
	ComplaintCategory *string `dynamodbav:"complaint_category,omitempty"`
	Confidence        float64 `dynamodbav:"confidence"`
	Status            string  `dynamodbav:"status"`
	ProcessedBy       *string `dynamodbav:"processed_by,omitempty"`
	RetryCount        int     `dynamodbav:"retry_count"`
}

// Status enum values for HiringRecord.Status.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusComplete   = "complete"
	StatusFailed     = "failed"
)

var ValidStages = map[string]bool{
	"applied": true, "phone_screen": true, "technical_interview": true,
	"onsite": true, "ghosted": true, "rejected": true, "offer": true,
	"negotiating": true, "unknown": true,
}

var ValidSentiments = map[string]bool{
	"frustrated": true, "hopeful": true, "neutral": true,
	"celebratory": true, "angry": true,
}

// TableName is the shared hiring_records table.
const TableName = "hiring_records"

// Global secondary indexes for easy lookups.
const (
	StatusIndex            = "status-index"
	ComplaintCategoryIndex = "complaint_category-index"
	RecordIDIndex          = "record_id-index"
)

