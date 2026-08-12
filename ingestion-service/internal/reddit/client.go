package reddit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	tokenURL  = "https://www.reddit.com/api/v1/access_token"
	apiBase   = "https://oauth.reddit.com"
	userAgent = "go:hiring-sentiment-ingestion:v1.0 (by /u/Exquisite_27)"
)

// Thing is the subset of Reddit's Listing API response needed here, covering both link (t3) and comment (t1) children uniformly.
type Thing struct {
	Kind string `json:"kind"`
	Data struct {
		Name       string  `json:"name"` // fullname, e.g. t3_abc123 / t1_xyz789
		Selftext   string  `json:"selftext"`
		Body       string  `json:"body"`
		Title      string  `json:"title"`
		CreatedUTC float64 `json:"created_utc"`
	} `json:"data"`
}

type listing struct {
	Data struct {
		Children []Thing `json:"children"`
		After    string  `json:"after"`
	} `json:"data"`
}

type Client struct {
	clientID     string
	clientSecret string
	username     string
	password     string
	httpClient   *http.Client
	logger       *slog.Logger

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time

	// Reddit script apps: ~60 req/min. Leave headroom below the real cap.
	limiter *rate.Limiter
}

func NewClient(clientID, clientSecret, username, password string, logger *slog.Logger) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		username:     username,
		password:     password,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		logger:       logger,
		limiter:      rate.NewLimiter(rate.Every(time.Minute/55), 5), // ~55/min, small burst
	}
}

func (c *Client) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.tokenExpiry) && c.accessToken != "" {
		return nil
	}

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", c.username)
	form.Set("password", c.password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build token request: %w", err)
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request status %d: %s", resp.StatusCode, string(body))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	c.accessToken = tok.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn-30) * time.Second)
	return nil
}

// FetchNew polls the "new" listing for a subreddit, honoring rate limits and retrying on 429 with exponential backoff 
// (starting at the server's Retry-After if present). limit is items requested (Reddit max 100).
func (c *Client) FetchNew(ctx context.Context, subreddit string, limit int) ([]Thing, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	endpoint := fmt.Sprintf("%s/r/%s/new?limit=%d&raw_json=1", apiBase, subreddit, limit)
	backoff := time.Second
	const maxAttempts = 5

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
		c.mu.Unlock()
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt == maxAttempts {
				return nil, fmt.Errorf("fetch %s failed after %d attempts: %w", subreddit, attempt, err)
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			wait := backoff
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, perr := time.ParseDuration(ra + "s"); perr == nil {
					wait = secs
				}
			}
			c.logger.Warn("reddit_rate_limited", "subreddit", subreddit, "attempt", attempt, "backoff_s", wait.Seconds())
			time.Sleep(wait)
			backoff *= 2
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response body: %w", readErr)
		}

		if resp.StatusCode == http.StatusUnauthorized {
			c.mu.Lock()
			c.tokenExpiry = time.Time{}
			c.mu.Unlock()
			if err := c.authenticate(ctx); err != nil {
				return nil, err
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s status %d: %s", subreddit, resp.StatusCode, string(body))
		}

		var l listing
		if err := json.Unmarshal(body, &l); err != nil {
			return nil, fmt.Errorf("parse listing for %s: %w", subreddit, err)
		}
		return l.Data.Children, nil
	}

	return nil, fmt.Errorf("fetch %s: exhausted retries", subreddit)
}
