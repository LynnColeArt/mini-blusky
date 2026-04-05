package research

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Fetcher struct {
	client         *http.Client
	maxSize        int64
	userAgent      string
	allowedSchemes map[string]bool
	allowedHosts   map[string]bool
}

type FetchResult struct {
	URL         string
	Title       string
	Content     string
	ContentType string
	FetchedAt   time.Time
	Error       error
}

type Config struct {
	Timeout      time.Duration
	MaxSize      int64
	UserAgent    string
	AllowedHosts []string
}

func DefaultConfig() Config {
	return Config{
		Timeout:   30 * time.Second,
		MaxSize:   1024 * 1024,
		UserAgent: "MiniBlueskyAgent/1.0 (research bot)",
	}
}

func NewFetcher(cfg Config) *Fetcher {
	allowedHosts := make(map[string]bool)
	for _, host := range cfg.AllowedHosts {
		allowedHosts[host] = true
	}

	return &Fetcher{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		maxSize:   cfg.MaxSize,
		userAgent: cfg.UserAgent,
		allowedSchemes: map[string]bool{
			"http":  true,
			"https": true,
		},
		allowedHosts: allowedHosts,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) FetchResult {
	result := FetchResult{
		URL:       rawURL,
		FetchedAt: time.Now(),
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		result.Error = fmt.Errorf("invalid URL: %w", err)
		return result
	}

	if !f.allowedSchemes[parsedURL.Scheme] {
		result.Error = fmt.Errorf("scheme %s not allowed", parsedURL.Scheme)
		return result
	}

	if len(f.allowedHosts) > 0 && !f.allowedHosts[parsedURL.Host] {
		result.Error = fmt.Errorf("host %s not in allowed list", parsedURL.Host)
		return result
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,text/plain")

	resp, err := f.client.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		result.Error = fmt.Errorf("status %d", resp.StatusCode)
		return result
	}

	limitedReader := io.LimitReader(resp.Body, f.maxSize)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		result.Error = fmt.Errorf("failed to read body: %w", err)
		return result
	}

	result.ContentType = resp.Header.Get("Content-Type")

	content := string(body)
	if strings.Contains(result.ContentType, "text/html") {
		result.Title = extractTitle(content)
		result.Content = extractText(content)
	} else {
		result.Content = content
	}

	slog.Debug("fetched URL",
		"url", rawURL,
		"content_length", len(result.Content),
		"title", result.Title,
	)

	return result
}

var (
	titleRegex  = regexp.MustCompile(`<title[^>]*>([^<]+)</title>`)
	scriptRegex = regexp.MustCompile(`<script[^>]*>[\s\S]*?</script>`)
	styleRegex  = regexp.MustCompile(`<style[^>]*>[\s\S]*?</style>`)
	tagRegex    = regexp.MustCompile(`<[^>]+>`)
	spaceRegex  = regexp.MustCompile(`\s+`)
)

func extractTitle(html string) string {
	matches := titleRegex.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func extractText(html string) string {
	text := scriptRegex.ReplaceAllString(html, "")
	text = styleRegex.ReplaceAllString(text, "")
	text = tagRegex.ReplaceAllString(text, " ")
	text = spaceRegex.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func (r FetchResult) IsValid() bool {
	return r.Error == nil && len(r.Content) > 0
}
