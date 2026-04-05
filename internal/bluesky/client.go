package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	BaseURL = "https://bsky.social"
)

var (
	ErrRateLimited  = errors.New("rate limited")
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
	ErrServerError  = errors.New("server error")
	ErrPermanent    = errors.New("permanent error")
	ErrRetryable    = errors.New("retryable error")
)

type APIError struct {
	StatusCode int
	Message    string
	Retryable  bool
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Message)
}

func (e *APIError) Unwrap() error {
	if e.Retryable {
		return ErrRetryable
	}
	return ErrPermanent
}

type Client struct {
	client    *http.Client
	baseURL   string
	handle    string
	password  string
	accessJWT string
	did       string
	retries   int
	baseDelay time.Duration
	maxDelay  time.Duration
}

type ClientOption func(*Client)

func WithRetry(retries int, baseDelay, maxDelay time.Duration) ClientOption {
	return func(c *Client) {
		c.retries = retries
		c.baseDelay = baseDelay
		c.maxDelay = maxDelay
	}
}

type Session struct {
	Did       string `json:"did"`
	Handle    string `json:"handle"`
	AccessJwt string `json:"accessJwt"`
}

type Post struct {
	URI       string     `json:"uri"`
	CID       string     `json:"cid"`
	Author    Author     `json:"author"`
	Record    PostRecord `json:"record"`
	IndexedAt time.Time  `json:"indexedAt"`
}

type Author struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type PostRecord struct {
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type FeedResponse struct {
	Feed   []FeedItem `json:"feed"`
	Cursor string     `json:"cursor"`
}

type FeedItem struct {
	Post Post `json:"post"`
}

type CreatePostRequest struct {
	Collection string         `json:"collection"`
	Repo       string         `json:"repo"`
	Record     PostRecordData `json:"record"`
}

type PostRecordData struct {
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	Type      string `json:"$type"`
}

func NewClient(handle, password string, opts ...ClientOption) *Client {
	c := &Client{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:   BaseURL,
		handle:    handle,
		password:  password,
		retries:   3,
		baseDelay: 1 * time.Second,
		maxDelay:  30 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Authenticate(ctx context.Context) error {
	payload := map[string]string{
		"identifier": c.handle,
		"password":   c.password,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal auth payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/xrpc/com.atproto.server.createSession", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return fmt.Errorf("failed to decode session: %w", err)
	}

	c.accessJWT = session.AccessJwt
	c.did = session.Did
	return nil
}

func (c *Client) GetTimeline(ctx context.Context, limit int, cursor string) (*FeedResponse, error) {
	url := fmt.Sprintf("%s/xrpc/app.bsky.feed.getTimeline?limit=%d", c.baseURL, limit)
	if cursor != "" {
		url += "&cursor=" + cursor
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create timeline request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessJWT)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("timeline request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("timeline failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var feedResp FeedResponse
	if err := json.NewDecoder(resp.Body).Decode(&feedResp); err != nil {
		return nil, fmt.Errorf("failed to decode timeline: %w", err)
	}

	return &feedResp, nil
}

func (c *Client) Post(ctx context.Context, text string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	record := PostRecordData{
		Text:      text,
		CreatedAt: now,
		Type:      "app.bsky.feed.post",
	}

	payload := map[string]interface{}{
		"collection": "app.bsky.feed.post",
		"repo":       c.did,
		"record":     record,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal post payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessJWT)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("post request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("post failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		URI string `json:"uri"`
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode post response: %w", err)
	}

	return result.URI, nil
}

func (c *Client) Follow(ctx context.Context, subjectDID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	payload := map[string]interface{}{
		"collection": "app.bsky.graph.follow",
		"repo":       c.did,
		"record": map[string]interface{}{
			"subject":   subjectDID,
			"createdAt": now,
			"$type":     "app.bsky.graph.follow",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal follow payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create follow request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessJWT)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("follow request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("follow failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *Client) Like(ctx context.Context, postURI, postCID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	payload := map[string]interface{}{
		"collection": "app.bsky.feed.like",
		"repo":       c.did,
		"record": map[string]interface{}{
			"subject": map[string]string{
				"uri": postURI,
				"cid": postCID,
			},
			"createdAt": now,
			"$type":     "app.bsky.feed.like",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal like payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create like request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessJWT)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("like request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("like failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *Client) Reply(ctx context.Context, text, parentURI, parentCID string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	rootURI := parentURI
	rootCID := parentCID

	record := map[string]interface{}{
		"text":      text,
		"createdAt": now,
		"$type":     "app.bsky.feed.post",
		"reply": map[string]interface{}{
			"root": map[string]string{
				"uri": rootURI,
				"cid": rootCID,
			},
			"parent": map[string]string{
				"uri": parentURI,
				"cid": parentCID,
			},
		},
	}

	payload := map[string]interface{}{
		"collection": "app.bsky.feed.post",
		"repo":       c.did,
		"record":     record,
	}

	var result struct {
		URI string `json:"uri"`
		CID string `json:"cid"`
	}
	err := c.doWithRetry(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.createRecord", payload, &result)
	if err != nil {
		return "", err
	}
	return result.URI, nil
}

func (c *Client) Unfollow(ctx context.Context, followRecordURI string) error {
	rkey := extractRecordKey(followRecordURI)
	if rkey == "" {
		return fmt.Errorf("invalid follow record URI: %s", followRecordURI)
	}

	payload := map[string]interface{}{
		"collection": "app.bsky.graph.follow",
		"repo":       c.did,
		"rkey":       rkey,
	}

	return c.doWithRetry(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.deleteRecord", payload, nil)
}

type FollowRecord struct {
	URI        string
	SubjectDID string
}

func (c *Client) ListFollowRecords(ctx context.Context) ([]FollowRecord, error) {
	var result struct {
		Records []struct {
			URI   string `json:"uri"`
			Value struct {
				Subject string `json:"subject"`
			} `json:"value"`
		} `json:"records"`
	}

	url := fmt.Sprintf("%s/xrpc/com.atproto.repo.listRecords?repo=%s&collection=app.bsky.graph.follow&limit=100",
		c.baseURL, c.did)

	err := c.doWithRetry(ctx, "GET", url, nil, &result)
	if err != nil {
		return nil, err
	}

	records := make([]FollowRecord, len(result.Records))
	for i, r := range result.Records {
		records[i] = FollowRecord{
			URI:        r.URI,
			SubjectDID: r.Value.Subject,
		}
	}
	return records, nil
}

func (c *Client) UnfollowByDID(ctx context.Context, subjectDID string) error {
	records, err := c.ListFollowRecords(ctx)
	if err != nil {
		return fmt.Errorf("failed to list follow records: %w", err)
	}

	for _, r := range records {
		if r.SubjectDID == subjectDID {
			return c.Unfollow(ctx, r.URI)
		}
	}

	return fmt.Errorf("no follow record found for subject: %s", subjectDID)
}

func (c *Client) GetFollows(ctx context.Context, actor string, limit int, cursor string) ([]string, string, error) {
	params := url.Values{}
	params.Set("actor", actor)
	params.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	var result struct {
		Follows []struct {
			Did string `json:"did"`
		} `json:"follows"`
		Cursor string `json:"cursor"`
	}

	err := c.doWithRetry(ctx, "GET", c.baseURL+"/xrpc/app.bsky.graph.getFollows?"+params.Encode(), nil, &result)
	if err != nil {
		return nil, "", err
	}

	dids := make([]string, len(result.Follows))
	for i, f := range result.Follows {
		dids[i] = f.Did
	}
	return dids, result.Cursor, nil
}

func extractRecordKey(uri string) string {
	parts := splitAtProtoURI(uri)
	if len(parts) >= 5 {
		return parts[4]
	}
	return ""
}

func splitAtProtoURI(uri string) []string {
	return splitString(uri, "/")
}

func splitString(s, sep string) []string {
	var result []string
	for {
		idx := indexOf(s, sep)
		if idx == -1 {
			if len(s) > 0 {
				result = append(result, s)
			}
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func (c *Client) DID() string {
	return c.did
}

func (c *Client) Handle() string {
	return c.handle
}

type PostsResponse struct {
	Posts []Post `json:"posts"`
}

func (c *Client) GetPosts(ctx context.Context, uris []string) ([]Post, error) {
	if len(uris) == 0 {
		return nil, nil
	}

	params := url.Values{}
	for _, uri := range uris {
		params.Add("uris", uri)
	}

	apiURL := fmt.Sprintf("%s/xrpc/app.bsky.feed.getPosts?%s", c.baseURL, params.Encode())

	var resp PostsResponse
	err := c.doWithRetry(ctx, "GET", apiURL, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp.Posts, nil
}

func (c *Client) GetPost(ctx context.Context, uri string) (*Post, error) {
	posts, err := c.GetPosts(ctx, []string{uri})
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, fmt.Errorf("post not found: %s", uri)
	}
	return &posts[0], nil
}

func (c *Client) doWithRetry(ctx context.Context, method, url string, body interface{}, result interface{}) error {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			delay := c.baseDelay * time.Duration(1<<(attempt-1))
			if delay > c.maxDelay {
				delay = c.maxDelay
			}
			slog.Debug("retrying request", "attempt", attempt, "delay", delay, "url", url)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		var reqBody io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("failed to marshal request body: %w", err)
			}
			reqBody = bytes.NewReader(b)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.accessJWT != "" {
			req.Header.Set("Authorization", "Bearer "+c.accessJWT)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if isRetryableNetworkError(err) {
				continue
			}
			return lastErr
		}

		if resp.StatusCode == 200 {
			if result != nil {
				if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
					resp.Body.Close()
					return fmt.Errorf("failed to decode response: %w", err)
				}
			}
			resp.Body.Close()
			return nil
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		apiErr := classifyError(resp.StatusCode, string(respBody))
		lastErr = apiErr

		if !apiErr.Retryable {
			return apiErr
		}

		if resp.StatusCode == 429 {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			slog.Warn("rate limited", "retry_after", retryAfter, "url", url)
			if retryAfter > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryAfter):
				}
			}
		}
	}

	return lastErr
}

func classifyError(statusCode int, message string) *APIError {
	switch {
	case statusCode == 429:
		return &APIError{StatusCode: statusCode, Message: message, Retryable: true}
	case statusCode >= 500:
		return &APIError{StatusCode: statusCode, Message: message, Retryable: true}
	case statusCode == 401:
		return &APIError{StatusCode: statusCode, Message: "unauthorized", Retryable: false}
	case statusCode == 404:
		return &APIError{StatusCode: statusCode, Message: "not found", Retryable: false}
	case statusCode >= 400 && statusCode < 500:
		return &APIError{StatusCode: statusCode, Message: message, Retryable: false}
	default:
		return &APIError{StatusCode: statusCode, Message: message, Retryable: true}
	}
}

func isRetryableNetworkError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	var seconds int
	if _, err := fmt.Sscanf(header, "%d", &seconds); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}
	return errors.Is(err, ErrRetryable)
}

func IsPermanent(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return !apiErr.Retryable
	}
	return errors.Is(err, ErrPermanent)
}

const ChatBaseURL = "https://chat.bsky.social"

type DMMessage struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	SenderDID string    `json:"sender"`
	SentAt    time.Time `json:"sentAt"`
}

type DMConversation struct {
	ID string `json:"id"`
}

func (c *Client) GetConversations(ctx context.Context, limit int) ([]DMConversation, error) {
	apiURL := fmt.Sprintf("%s/xrpc/chat.bsky.convo.listConvos?limit=%d", ChatBaseURL, limit)

	var result struct {
		Convos []struct {
			ID string `json:"id"`
		} `json:"convos"`
	}

	err := c.doWithRetry(ctx, "GET", apiURL, nil, &result)
	if err != nil {
		return nil, err
	}

	convos := make([]DMConversation, len(result.Convos))
	for i, conv := range result.Convos {
		convos[i] = DMConversation{ID: conv.ID}
	}
	return convos, nil
}

func (c *Client) GetMessages(ctx context.Context, convoID string, limit int) ([]DMMessage, error) {
	params := url.Values{}
	params.Set("convoId", convoID)
	params.Set("limit", fmt.Sprintf("%d", limit))

	apiURL := fmt.Sprintf("%s/xrpc/chat.bsky.convo.getMessages?%s", ChatBaseURL, params.Encode())

	var result struct {
		Messages []struct {
			ID     string `json:"id"`
			Text   string `json:"text"`
			Sender struct {
				DID string `json:"did"`
			} `json:"sender"`
			SentAt time.Time `json:"sentAt"`
		} `json:"messages"`
	}

	err := c.doWithRetry(ctx, "GET", apiURL, nil, &result)
	if err != nil {
		return nil, err
	}

	messages := make([]DMMessage, len(result.Messages))
	for i, m := range result.Messages {
		messages[i] = DMMessage{
			ID:        m.ID,
			Text:      m.Text,
			SenderDID: m.Sender.DID,
			SentAt:    m.SentAt,
		}
	}
	return messages, nil
}

func (c *Client) SendDM(ctx context.Context, convoID, text string) error {
	payload := map[string]interface{}{
		"convoId": convoID,
		"message": map[string]interface{}{
			"text": text,
		},
	}

	return c.doWithRetry(ctx, "POST", ChatBaseURL+"/xrpc/chat.bsky.convo.sendMessage", payload, nil)
}

func (c *Client) ResolveHandle(ctx context.Context, handle string) (string, error) {
	apiURL := fmt.Sprintf("%s/xrpc/com.atproto.identity.resolveHandle?handle=%s", c.baseURL, handle)

	var result struct {
		DID string `json:"did"`
	}

	err := c.doWithRetry(ctx, "GET", apiURL, nil, &result)
	if err != nil {
		return "", err
	}
	return result.DID, nil
}

func (c *Client) Block(ctx context.Context, subjectDID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	payload := map[string]interface{}{
		"collection": "app.bsky.graph.block",
		"repo":       c.did,
		"record": map[string]interface{}{
			"subject":   subjectDID,
			"createdAt": now,
			"$type":     "app.bsky.graph.block",
		},
	}

	return c.doWithRetry(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.createRecord", payload, nil)
}

func (c *Client) Unblock(ctx context.Context, blockRecordURI string) error {
	rkey := extractRecordKey(blockRecordURI)
	if rkey == "" {
		return fmt.Errorf("invalid block record URI: %s", blockRecordURI)
	}

	payload := map[string]interface{}{
		"collection": "app.bsky.graph.block",
		"repo":       c.did,
		"rkey":       rkey,
	}

	return c.doWithRetry(ctx, "POST", c.baseURL+"/xrpc/com.atproto.repo.deleteRecord", payload, nil)
}

func (c *Client) ListBlockRecords(ctx context.Context) ([]FollowRecord, error) {
	var result struct {
		Records []struct {
			URI   string `json:"uri"`
			Value struct {
				Subject string `json:"subject"`
			} `json:"value"`
		} `json:"records"`
	}

	apiURL := fmt.Sprintf("%s/xrpc/com.atproto.repo.listRecords?repo=%s&collection=app.bsky.graph.block&limit=500",
		c.baseURL, c.did)

	err := c.doWithRetry(ctx, "GET", apiURL, nil, &result)
	if err != nil {
		return nil, err
	}

	records := make([]FollowRecord, len(result.Records))
	for i, r := range result.Records {
		records[i] = FollowRecord{
			URI:        r.URI,
			SubjectDID: r.Value.Subject,
		}
	}
	return records, nil
}

func (c *Client) IsBlocked(ctx context.Context, subjectDID string) (bool, error) {
	records, err := c.ListBlockRecords(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range records {
		if r.SubjectDID == subjectDID {
			return true, nil
		}
	}
	return false, nil
}
