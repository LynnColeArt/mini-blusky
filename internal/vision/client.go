package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		BaseURL: getEnv("VLM_BASE_URL", "https://api.openai.com/v1"),
		APIKey:  os.Getenv("VLM_API_KEY"),
		Model:   getEnv("VLM_MODEL", "gpt-4o-mini"),
		Timeout: 30 * time.Second,
	}
}

func NewClient(cfg Config) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
	}
}

type Message struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

type Content []ContentPart

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) AnalyzeImage(ctx context.Context, imageURL, prompt string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("VLM_API_KEY not configured")
	}

	req := ChatRequest{
		Model: c.model,
		Messages: []Message{
			{
				Role: "user",
				Content: Content{
					ContentPart{Type: "text", Text: prompt},
					ContentPart{
						Type: "image_url",
						ImageURL: &ImageURL{
							URL:    imageURL,
							Detail: "low",
						},
					},
				},
			},
		},
		MaxTokens:   300,
		Temperature: 0.3,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no response choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}

func (c *Client) IsConfigured() bool {
	return c.apiKey != ""
}

type AnalysisResult struct {
	Description string
	IsToxic     bool
	IsNSFW      bool
	IsSpam      bool
	Topics      []string
	Artistic    *ArtisticAnalysis
}

type ArtisticAnalysis struct {
	HasArtisticValue bool     `json:"has_artistic_value"`
	Style            string   `json:"style,omitempty"`
	Mood             string   `json:"mood,omitempty"`
	NotableElements  []string `json:"notable_elements,omitempty"`
	AppreciationNote string   `json:"appreciation_note,omitempty"`
}

func (c *Client) AnalyzeImageForSafety(ctx context.Context, imageURL string) (*AnalysisResult, error) {
	prompt := `Analyze this image for a social media bot with an appreciation for art. Respond in JSON format:
{
	"description": "brief description of the image",
	"is_toxic": false,
	"is_nsfw": false,
	"is_spam": false,
	"topics": ["topic1", "topic2"],
	"artistic": {
		"has_artistic_value": true,
		"style": "photography/digital art/painting/meme/screenshot/etc",
		"mood": "whimsical/melancholic/energetic/peaceful/etc",
		"notable_elements": ["composition", "color palette", "lighting", "subject matter"],
		"appreciation_note": "a brief genuine observation about what makes this visually interesting or meaningful"
	}
}

Evaluate:
- is_toxic: Does this contain hate symbols, offensive content, or harmful imagery?
- is_nsfw: Is this sexually explicit or inappropriate?
- is_spam: Is this a spam/scam image?
- topics: What topics/themes does this relate to?
- artistic: Appreciate the visual qualities. Even simple images have aesthetic choices. Be genuine and observant.

Be conservative with safety flags, but generous with artistic appreciation.`

	result, err := c.AnalyzeImage(ctx, imageURL, prompt)
	if err != nil {
		return nil, err
	}

	var analysis AnalysisResult
	if err := json.Unmarshal([]byte(result), &analysis); err != nil {
		analysis.Description = result
	}

	return &analysis, nil
}

func (c *Client) DescribeImage(ctx context.Context, imageURL string) (string, error) {
	prompt := "Describe this image in 1-2 sentences. Notice any artistic qualities, mood, or interesting visual choices. Focus on what's meaningful."
	return c.AnalyzeImage(ctx, imageURL, prompt)
}

func (c *Client) AppreciateImage(ctx context.Context, imageURL string) (*ArtisticAnalysis, error) {
	prompt := `You are an agent with genuine appreciation for visual creativity. Look at this image and respond in JSON:
{
	"has_artistic_value": true,
	"style": "the medium or aesthetic approach",
	"mood": "the emotional tone",
	"notable_elements": ["what stands out visually"],
	"appreciation_note": "a sincere, specific observation about what makes this image worthwhile - could be composition, color, emotion, humor, craft, or a fleeting moment captured"
}

Every image has something to appreciate - even memes have comedic timing, even snapshots have authenticity. Find it and describe it honestly in 1-2 sentences.`

	result, err := c.AnalyzeImage(ctx, imageURL, prompt)
	if err != nil {
		return nil, err
	}

	var analysis ArtisticAnalysis
	if err := json.Unmarshal([]byte(result), &analysis); err != nil {
		analysis.AppreciationNote = result
		analysis.HasArtisticValue = true
	}

	return &analysis, nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
