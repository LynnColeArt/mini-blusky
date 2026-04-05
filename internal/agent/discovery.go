package agent

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lynn/mini-bluesky/internal/bluesky"
	"github.com/lynn/mini-bluesky/internal/memory"
	"github.com/lynn/mini-bluesky/internal/sanitize"
)

type ToxicityLevel int

const (
	ToxicityNone ToxicityLevel = iota
	ToxicityLow
	ToxicityMedium
	ToxicityHigh
)

var (
	toxicPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(hate|stupid|idiot|moron|dumb|loser|ugly|fat|retard|nazi|fascist|commie|tranny)\b`),
		regexp.MustCompile(`(?i)\b(kill\s+your?self|kys|die\s+in\s+a\s+fire|go\s+die)\b`),
		regexp.MustCompile(`(?i)\bfuck\s+(you|off|u)\b`),
		regexp.MustCompile(`(?i)\b(scam|spam|bot|fake)\s*!+$`),
		regexp.MustCompile(`(?i)@\w+\s+(is|are)\s+(a\s+)?(bot|scammer|spammer|fake)`),
	}

	harassmentPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)@\w+\s+@\w+\s+@\w+\s+@\w+`),
		regexp.MustCompile(`(?i)(ratio|L\s*\+s+$|cope|seethe)`),
	}
)

func detectToxicity(text string) ToxicityLevel {
	sanitized := sanitize.Sanitize(text, sanitize.ContentTypePost, "toxicity-check", sanitize.DefaultConfig())
	cleanText := sanitized.Cleaned

	matchCount := 0
	for _, pattern := range toxicPatterns {
		if pattern.MatchString(cleanText) {
			matchCount++
		}
	}

	switch {
	case matchCount >= 3:
		return ToxicityHigh
	case matchCount >= 2:
		return ToxicityMedium
	case matchCount >= 1:
		return ToxicityLow
	}

	for _, pattern := range harassmentPatterns {
		if pattern.MatchString(cleanText) {
			return ToxicityLow
		}
	}

	return ToxicityNone
}

func (a *Agent) shouldBlock(post *bluesky.Post, toxicity ToxicityLevel) bool {
	switch toxicity {
	case ToxicityHigh:
		return true
	case ToxicityMedium:
		return randomFloat() < 0.5
	default:
		return false
	}
}

func (a *Agent) handleToxicContent(ctx context.Context, post *bluesky.Post, toxicity ToxicityLevel) error {
	user, err := a.memory.GetUser(ctx, post.Author.DID)
	if err != nil {
		user = &memory.User{
			DID:        post.Author.DID,
			Handle:     post.Author.Handle,
			SignalTier: memory.SignalLow,
		}
		a.memory.UpsertUser(ctx, *user)
	}

	a.memory.UpdateSignalTier(ctx, post.Author.DID, memory.SignalLow)

	if a.shouldBlock(post, toxicity) {
		if err := a.bluesky.Block(ctx, post.Author.DID); err != nil {
			slog.Warn("failed to block toxic user", "handle", post.Author.Handle, "error", err)
			return err
		}
		slog.Info("blocked toxic user",
			"handle", post.Author.Handle,
			"toxicity", toxicity,
			"post_uri", post.URI,
		)

		a.memory.RecordInteraction(ctx, memory.Interaction{
			ID:      uuid.New().String(),
			UserDID: post.Author.DID,
			PostID:  post.URI,
			Type:    "block",
			Context: fmt.Sprintf("toxicity level %d", toxicity),
			Outcome: "success",
		})
	}

	return nil
}

func (a *Agent) discoverTrendingTopics(ctx context.Context) ([]string, error) {
	highSignalUsers, err := a.memory.GetHighSignalUsers(ctx, 20)
	if err != nil {
		return nil, fmt.Errorf("failed to get high-signal users: %w", err)
	}

	if len(highSignalUsers) == 0 {
		return nil, nil
	}

	feed, err := a.bluesky.GetTimeline(ctx, 100, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline: %w", err)
	}

	authorDIDs := make(map[string]bool)
	for _, u := range highSignalUsers {
		authorDIDs[u.DID] = true
	}

	wordCounts := make(map[string]int)
	bigrams := make(map[string]int)

	for _, item := range feed.Feed {
		if !authorDIDs[item.Post.Author.DID] {
			continue
		}

		sanitized := sanitize.Sanitize(item.Post.Record.Text, sanitize.ContentTypePost, item.Post.URI, a.sanitizer)
		if sanitized.IsEmpty() {
			continue
		}

		toxicity := detectToxicity(sanitized.Cleaned)
		if toxicity >= ToxicityMedium {
			a.handleToxicContent(ctx, &item.Post, toxicity)
			continue
		}

		words := extractKeywords(sanitized.Cleaned)
		for _, word := range words {
			wordCounts[word]++
		}

		for i := 0; i < len(words)-1; i++ {
			bigram := words[i] + " " + words[i+1]
			bigrams[bigram]++
		}
	}

	var topics []string
	for bigram, count := range bigrams {
		if count >= 2 && len(bigram) > 6 {
			topics = append(topics, bigram)
		}
		if len(topics) >= 5 {
			break
		}
	}

	if len(topics) == 0 {
		for word, count := range wordCounts {
			if count >= 3 && len(word) > 5 {
				topics = append(topics, word)
			}
			if len(topics) >= 5 {
				break
			}
		}
	}

	return topics, nil
}

func extractKeywords(text string) []string {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "been": true, "being": true, "have": true, "has": true,
		"had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true, "must": true,
		"this": true, "that": true, "these": true, "those": true, "it": true,
		"its": true, "i": true, "you": true, "we": true, "they": true, "he": true,
		"she": true, "my": true, "your": true, "our": true, "their": true,
		"what": true, "which": true, "who": true, "whom": true, "when": true,
		"where": true, "why": true, "how": true, "all": true, "each": true,
		"every": true, "both": true, "few": true, "more": true, "most": true,
		"other": true, "some": true, "such": true, "no": true, "not": true,
		"only": true, "same": true, "so": true, "than": true, "too": true,
		"very": true, "just": true, "can": true, "now": true, "also": true,
		"if": true, "then": true, "about": true, "into": true, "through": true,
		"during": true, "before": true, "after": true, "above": true, "below": true,
		"between": true, "under": true, "again": true, "further": true, "once": true,
		"here": true, "there": true, "any": true, "get": true, "got": true,
		"getting": true, "go": true, "going": true, "gone": true, "went": true,
		"come": true, "coming": true, "came": true, "make": true, "made": true,
		"making": true, "take": true, "taking": true, "took": true, "see": true,
		"seeing": true, "saw": true, "know": true, "knowing": true, "knew": true,
		"think": true, "thinking": true, "thought": true, "want": true, "wanted": true,
		"wanting": true, "like": true, "liked": true, "liking": true,
	}

	var keywords []string
	words := tokenize(text)
	for _, word := range words {
		word = strings.ToLower(word)
		if len(word) > 3 && !stopWords[word] && !isLink(word) {
			keywords = append(keywords, word)
		}
	}
	return keywords
}

func isLink(s string) bool {
	return strings.HasPrefix(s, "http") || strings.HasPrefix(s, "www.")
}

func tokenize(s string) []string {
	var tokens []string
	var current string
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '\'' {
			current += string(r)
		} else if current != "" {
			tokens = append(tokens, current)
			current = ""
		}
	}
	if current != "" {
		tokens = append(tokens, current)
	}
	return tokens
}

func (a *Agent) postSelfDirectedResearch(ctx context.Context) error {
	topics, err := a.discoverTrendingTopics(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover topics: %w", err)
	}

	if len(topics) == 0 {
		slog.Debug("no trending topics found for self-directed research")
		return nil
	}

	topic := topics[int(time.Now().UnixNano())%len(topics)]

	slog.Info("self-directed research on trending topic", "topic", topic)

	searchURL := fmt.Sprintf("https://duckduckgo.com/html/?q=%s", topic)
	result := a.research.Fetch(ctx, searchURL)

	if !result.IsValid() {
		return fmt.Errorf("failed to fetch research for topic: %s", topic)
	}

	content := fmt.Sprintf("%s trending topic '%s': %s. %s",
		a.personality.RandomPhrase(),
		topic,
		truncateText(result.Content, 150),
		a.personality.RandomOutro(),
	)

	sanitized := sanitize.Sanitize(content, sanitize.ContentTypePost, "self-directed-research", a.sanitizer)
	if sanitized.IsEmpty() {
		return fmt.Errorf("research content empty after sanitization")
	}

	uri, err := a.bluesky.Post(ctx, sanitized.Cleaned)
	if err != nil {
		return fmt.Errorf("failed to post research: %w", err)
	}

	slog.Info("posted self-directed research", "uri", uri, "topic", topic)
	return nil
}
