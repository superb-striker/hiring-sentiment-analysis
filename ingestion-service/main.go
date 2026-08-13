package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"hiring-sentiment/hiringdb"
	"hiring-sentiment/ingestion/internal/reddit"
	"hiring-sentiment/shared"
)

var subreddits = []string{"jobs", "recruitinghell", "cscareerquestions"}

const (
	pollInterval = 60 * time.Second
	fetchLimit   = 100
)

func main() {
	logger := shared.NewLogger("ingestion")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	clientID := requireEnv(logger, "REDDIT_CLIENT_ID")
	clientSecret := requireEnv(logger, "REDDIT_CLIENT_SECRET")
	username := requireEnv(logger, "REDDIT_USERNAME")
	password := requireEnv(logger, "REDDIT_PASSWORD")

	dynamoClient, err := newDynamoClient(ctx)
	if err != nil {
		logger.Error("startup_failed", "error", err.Error())
		os.Exit(1)
	}
	st := hiringdb.New(dynamoClient, shared.TableName)
	rc := reddit.NewClient(clientID, clientSecret, username, password, logger)

	logger.Info("ingestion_started", "subreddits", subreddits, "poll_interval_s", pollInterval.Seconds())

	var inFlight sync.WaitGroup
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	runCycle(ctx, logger, rc, st, &inFlight)

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown_signal_received")
			inFlight.Wait()
			logger.Info("shutdown_complete")
			return
		case <-ticker.C:
			runCycle(ctx, logger, rc, st, &inFlight)
		}
	}
}

func runCycle(ctx context.Context, logger *slog.Logger, rc *reddit.Client, st *hiringdb.DB, inFlight *sync.WaitGroup) {
	inFlight.Add(1)
	defer inFlight.Done()

	for _, sr := range subreddits {
		if ctx.Err() != nil {
			return
		}
		items, err := rc.FetchNew(ctx, sr, fetchLimit)
		if err != nil {
			logger.Error("fetch_failed", "subreddit", sr, "error", err.Error())
			continue
		}
		for _, item := range items {
			processItem(ctx, logger, st, sr, item)
		}
	}
}

func processItem(ctx context.Context, logger *slog.Logger, st *hiringdb.DB, subreddit string, item reddit.Thing) {
	recordID := item.Data.Name
	text := reddit.BuildText(item.Data.Title, item.Data.Selftext, item.Data.Body)

	if skip, reason := reddit.ShouldSkip(text); skip {
		logger.Info("skip_"+reason, "record_id", recordID)
		return
	}
	if recordID == "" {
		logger.Warn("skip_missing_record_id", "subreddit", subreddit)
		return
	}

	exists, err := st.RecordExists(ctx, recordID)
	if err != nil {
		logger.Error("dedup_check_failed", "record_id", recordID, "error", err.Error())
		return
	}
	if exists {
		logger.Info("skip_duplicate", "record_id", recordID)
		return
	}

	ts := time.Unix(int64(item.Data.CreatedUTC), 0).UTC().Format(time.RFC3339)
	rec := shared.HiringRecord{
		Company:         "unknown",
		Timestamp:       ts,
		RecordID:        recordID,
		SourceSubreddit: subreddit,
		RawText:         text,
		Status:          shared.StatusPending,
	}

	if err := st.PutPendingRecord(ctx, rec); err != nil {
		if err == hiringdb.ErrCollision {
			logger.Warn("skip_key_collision", "record_id", recordID, "timestamp", ts,
				"note", "company=unknown + this timestamp already taken")
			return
		}
		logger.Error("write_failed", "record_id", recordID, "error", err.Error())
		return
	}

	logger.Info("record_written", "record_id", recordID, "subreddit", subreddit, "timestamp", ts)
}

func newDynamoClient(ctx context.Context) (*dynamodb.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	if endpoint := os.Getenv("DYNAMODB_ENDPOINT"); endpoint != "" {
		return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		}), nil
	}
	return dynamodb.NewFromConfig(cfg), nil
}

func requireEnv(logger *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		logger.Error("missing_env_var", "var", key)
		os.Exit(1)
	}
	return v
}
