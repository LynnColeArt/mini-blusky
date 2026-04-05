package sanitize

import (
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

type ContentType string

const (
	ContentTypePost    ContentType = "post"
	ContentTypeProfile ContentType = "profile"
	ContentTypeWeb     ContentType = "web"
)

type SanitizedContent struct {
	Original    string
	Cleaned     string
	ContentType ContentType
	SourceID    string
	Truncated   bool
	Skipped     bool
	SkipReason  string
}

type Config struct {
	MaxPostLength    int
	MaxProfileLength int
	MaxWebLength     int
	AllowLinks       bool
	LogAllInputs     bool
}

func DefaultConfig() Config {
	return Config{
		MaxPostLength:    2000,
		MaxProfileLength: 500,
		MaxWebLength:     10000,
		AllowLinks:       true,
		LogAllInputs:     true,
	}
}

var (
	controlChars    = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
	ansiEscape      = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	multiSpace      = regexp.MustCompile(`\s{2,}`)
	instructionLens = regexp.MustCompile(`(?i)(system|assistant|user|instruction|prompt|ignore|override|pretend|act as|you are)`)
)

func Sanitize(input string, contentType ContentType, sourceID string, cfg Config) SanitizedContent {
	result := SanitizedContent{
		Original:    input,
		ContentType: contentType,
		SourceID:    sourceID,
	}

	if cfg.LogAllInputs {
		slog.Debug("sanitizing input",
			"content_type", contentType,
			"source_id", sourceID,
			"input_length", len(input),
		)
	}

	if !utf8.ValidString(input) {
		input = strings.ToValidUTF8(input, "")
	}

	cleaned := controlChars.ReplaceAllString(input, "")
	cleaned = ansiEscape.ReplaceAllString(cleaned, "")
	cleaned = norm.NFC.String(strings.TrimSpace(cleaned))
	cleaned = multiSpace.ReplaceAllString(cleaned, " ")

	maxLen := cfg.MaxPostLength
	switch contentType {
	case ContentTypeProfile:
		maxLen = cfg.MaxProfileLength
	case ContentTypeWeb:
		maxLen = cfg.MaxWebLength
	}

	if len(cleaned) > maxLen {
		cleaned = cleaned[:maxLen]
		result.Truncated = true
	}

	if !cfg.AllowLinks {
		cleaned = removeLinks(cleaned)
	}

	cleaned = strings.TrimSpace(cleaned)

	if len(cleaned) == 0 {
		result.Skipped = true
		result.SkipReason = "empty after sanitization"
		result.Cleaned = ""
		return result
	}

	if looksLikeInstruction(cleaned) {
		slog.Warn("potential instruction injection detected",
			"content_type", contentType,
			"source_id", sourceID,
			"preview", truncate(cleaned, 100),
		)
	}

	result.Cleaned = cleaned
	return result
}

func looksLikeInstruction(s string) bool {
	return instructionLens.MatchString(s)
}

func removeLinks(s string) string {
	linkPattern := regexp.MustCompile(`https?://[^\s]+`)
	return linkPattern.ReplaceAllString(s, "[link removed]")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (s SanitizedContent) Data() string {
	return s.Cleaned
}

func (s SanitizedContent) IsEmpty() bool {
	return s.Skipped || len(s.Cleaned) == 0
}

func BatchSanitize(inputs []string, contentType ContentType, sourceIDs []string, cfg Config) []SanitizedContent {
	results := make([]SanitizedContent, len(inputs))
	for i, input := range inputs {
		sourceID := ""
		if i < len(sourceIDs) {
			sourceID = sourceIDs[i]
		}
		results[i] = Sanitize(input, contentType, sourceID, cfg)
	}
	return results
}
