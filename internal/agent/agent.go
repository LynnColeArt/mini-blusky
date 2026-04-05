package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lynn/mini-bluesky/internal/bluesky"
	"github.com/lynn/mini-bluesky/internal/embed"
	"github.com/lynn/mini-bluesky/internal/memory"
	"github.com/lynn/mini-bluesky/internal/research"
	"github.com/lynn/mini-bluesky/internal/sanitize"
	"github.com/lynn/mini-bluesky/internal/vision"
)

type Action string

const (
	ActionNone   Action = "none"
	ActionLike   Action = "like"
	ActionReply  Action = "reply"
	ActionFollow Action = "follow"
	ActionPost   Action = "post"
)

type Decision struct {
	Action   Action
	TargetID string
	Reason   string
	Score    float64
}

type Config struct {
	TickInterval               time.Duration
	ReflectionInterval         time.Duration
	ResearchInterval           time.Duration
	DiscoveryInterval          time.Duration
	UnfollowCheckInterval      time.Duration
	DMCheckInterval            time.Duration
	HighSignalThreshold        float64
	LowSignalThreshold         float64
	MaxActionsPerTick          int
	FollowProbability          float64
	LikeProbability            float64
	ReplyProbability           float64
	PostProbability            float64
	UnfollowAfter              time.Duration
	InteractionDepthWeight     float64
	SimilarityWeight           float64
	RecencyWeight              float64
	EngagementWeight           float64
	ControlUserHandle          string
	Personality                string
	ResearchTopics             []string
	EnableSelfDirectedResearch bool
}

func DefaultConfig() Config {
	return Config{
		TickInterval:           5 * time.Minute,
		ReflectionInterval:     24 * time.Hour,
		ResearchInterval:       12 * time.Hour,
		DiscoveryInterval:      8 * time.Hour,
		UnfollowCheckInterval:  6 * time.Hour,
		DMCheckInterval:        2 * time.Minute,
		HighSignalThreshold:    0.7,
		LowSignalThreshold:     0.3,
		MaxActionsPerTick:      10,
		FollowProbability:      0.1,
		LikeProbability:        0.3,
		ReplyProbability:       0.05,
		PostProbability:        0.05,
		UnfollowAfter:          7 * 24 * time.Hour,
		InteractionDepthWeight: 0.4,
		SimilarityWeight:       0.3,
		RecencyWeight:          0.2,
		EngagementWeight:       0.1,
		ControlUserHandle:      "",
		Personality:            "field-agent",
		ResearchTopics:         []string{},
	}
}

type Agent struct {
	config            Config
	bluesky           *bluesky.Client
	memory            *memory.Memory
	embed             *embed.Model
	research          *research.Fetcher
	vision            *vision.Client
	sanitizer         sanitize.Config
	running           bool
	mu                sync.Mutex
	cancelFunc        context.CancelFunc
	lastReflection    time.Time
	lastUnfollowCheck time.Time
	lastResearchPost  time.Time
	lastDMCheck       time.Time
	personality       Personality
	controlUserDID    string
	controlUserConvo  string
}

func New(
	cfg Config,
	bsky *bluesky.Client,
	mem *memory.Memory,
	emb *embed.Model,
	res *research.Fetcher,
	vis *vision.Client,
) *Agent {
	return &Agent{
		config:      cfg,
		bluesky:     bsky,
		memory:      mem,
		embed:       emb,
		research:    res,
		vision:      vis,
		sanitizer:   sanitize.DefaultConfig(),
		personality: GetPersonality(cfg.Personality),
	}
}

func (a *Agent) Init(ctx context.Context) error {
	if a.config.ControlUserHandle != "" {
		did, err := a.bluesky.ResolveHandle(ctx, a.config.ControlUserHandle)
		if err != nil {
			return fmt.Errorf("failed to resolve control user handle: %w", err)
		}
		a.controlUserDID = did
		slog.Info("resolved control user", "handle", a.config.ControlUserHandle, "did", did)
	}
	return nil
}

func (a *Agent) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("agent already running")
	}
	a.running = true
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	a.cancelFunc = cancel

	ticker := time.NewTicker(a.config.TickInterval)
	defer ticker.Stop()

	reflectionTicker := time.NewTicker(a.config.ReflectionInterval)
	defer reflectionTicker.Stop()

	unfollowTicker := time.NewTicker(a.config.UnfollowCheckInterval)
	defer unfollowTicker.Stop()

	researchTicker := time.NewTicker(a.config.ResearchInterval)
	defer researchTicker.Stop()

	discoveryTicker := time.NewTicker(a.config.DiscoveryInterval)
	defer discoveryTicker.Stop()

	dmTicker := time.NewTicker(a.config.DMCheckInterval)
	defer dmTicker.Stop()

	slog.Info("agent started",
		"tick_interval", a.config.TickInterval,
		"reflection_interval", a.config.ReflectionInterval,
		"research_interval", a.config.ResearchInterval,
		"discovery_interval", a.config.DiscoveryInterval,
		"unfollow_check_interval", a.config.UnfollowCheckInterval,
		"dm_check_interval", a.config.DMCheckInterval,
		"control_user", a.config.ControlUserHandle,
		"personality", a.personality.Name,
	)

	if err := a.tick(ctx); err != nil {
		slog.Error("initial tick failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("agent stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := a.tick(ctx); err != nil {
				slog.Error("tick failed", "error", err)
			}
		case <-reflectionTicker.C:
			if err := a.reflect(ctx); err != nil {
				slog.Error("reflection failed", "error", err)
			}
		case <-unfollowTicker.C:
			if err := a.pruneLowSignalUsers(ctx); err != nil {
				slog.Error("unfollow check failed", "error", err)
			}
		case <-researchTicker.C:
			if err := a.postResearch(ctx); err != nil {
				slog.Error("research post failed", "error", err)
			}
		case <-dmTicker.C:
			if err := a.checkDMs(ctx); err != nil {
				slog.Error("DM check failed", "error", err)
			}
			if err := a.processMissions(ctx); err != nil {
				slog.Error("mission processing failed", "error", err)
			}
		case <-discoveryTicker.C:
			if err := a.postSelfDirectedResearch(ctx); err != nil {
				slog.Error("discovery post failed", "error", err)
			}
		}
	}
}

func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelFunc != nil {
		a.cancelFunc()
	}
	a.running = false
}

func (a *Agent) tick(ctx context.Context) error {
	slog.Debug("running agent tick")

	feed, err := a.bluesky.GetTimeline(ctx, 50, "")
	if err != nil {
		return fmt.Errorf("failed to get timeline: %w", err)
	}

	decisions := make([]Decision, 0, a.config.MaxActionsPerTick)

	for _, item := range feed.Feed {
		sanitized := sanitize.Sanitize(
			item.Post.Record.Text,
			sanitize.ContentTypePost,
			item.Post.URI,
			a.sanitizer,
		)
		if sanitized.IsEmpty() {
			continue
		}

		post := memory.Post{
			ID:        item.Post.URI,
			AuthorDID: item.Post.Author.DID,
			Content:   sanitized.Cleaned,
			CreatedAt: item.Post.IndexedAt,
		}

		emb, err := a.embed.Embed(sanitized.Cleaned)
		if err != nil {
			slog.Warn("failed to embed post", "uri", item.Post.URI, "error", err)
			continue
		}
		post.Embedding = emb

		if err := a.memory.StorePost(ctx, post); err != nil {
			slog.Warn("failed to store post", "uri", item.Post.URI, "error", err)
		}

		if decision := a.decide(ctx, item.Post, emb); decision.Action != ActionNone {
			decisions = append(decisions, decision)
			if len(decisions) >= a.config.MaxActionsPerTick {
				break
			}
		}
	}

	for _, d := range decisions {
		if err := a.execute(ctx, d); err != nil {
			slog.Error("failed to execute action",
				"action", d.Action,
				"target", d.TargetID,
				"error", err,
			)
		} else {
			slog.Info("executed action",
				"action", d.Action,
				"target", d.TargetID,
				"reason", d.Reason,
			)
		}
	}

	return nil
}

func (a *Agent) decide(ctx context.Context, post bluesky.Post, embedding []float32) Decision {
	user, err := a.memory.GetUser(ctx, post.Author.DID)
	if err != nil {
		user = &memory.User{
			DID:        post.Author.DID,
			Handle:     post.Author.Handle,
			SignalTier: memory.SignalLow,
		}
		a.memory.UpsertUser(ctx, *user)
	}

	signalScore := a.calculateSignalScore(ctx, user, embedding)

	slog.Debug("signal score calculated",
		"handle", user.Handle,
		"score", signalScore,
		"tier", user.SignalTier,
	)

	if signalScore > a.config.HighSignalThreshold && user.SignalTier != memory.SignalHigh {
		a.memory.UpdateSignalTier(ctx, user.DID, memory.SignalHigh)
		slog.Info("promoted user to high signal", "handle", user.Handle)
	} else if signalScore < a.config.LowSignalThreshold && user.SignalTier != memory.SignalLow {
		a.memory.UpdateSignalTier(ctx, user.DID, memory.SignalLow)
		slog.Info("demoted user to low signal", "handle", user.Handle)
	}

	if user.SignalTier == memory.SignalHigh {
		if randomFloat() < a.config.ReplyProbability {
			return Decision{
				Action:   ActionReply,
				TargetID: post.URI,
				Reason:   fmt.Sprintf("engaging with high signal user (%s)", user.Handle),
				Score:    signalScore,
			}
		}

		if randomFloat() < a.config.LikeProbability {
			return Decision{
				Action:   ActionLike,
				TargetID: post.URI,
				Reason:   fmt.Sprintf("high signal user (%s)", user.Handle),
				Score:    signalScore,
			}
		}

		if randomFloat() < a.config.FollowProbability && user.InteractionCount < 5 {
			return Decision{
				Action:   ActionFollow,
				TargetID: user.DID,
				Reason:   fmt.Sprintf("discovered high signal user (%s)", user.Handle),
				Score:    signalScore,
			}
		}
	}

	return Decision{Action: ActionNone}
}

func (a *Agent) calculateSignalScore(ctx context.Context, user *memory.User, embedding []float32) float64 {
	interactionScore := math.Log1p(float64(user.InteractionCount)) / 5.0
	if interactionScore > 1 {
		interactionScore = 1
	}

	recencyScore := 0.0
	if !user.LastInteraction.IsZero() {
		hoursAgo := time.Since(user.LastInteraction).Hours()
		recencyScore = 1.0 / (1.0 + hoursAgo/24.0)
	}

	engagementScore := 0.0
	if user.InteractionCount > 10 {
		engagementScore = 0.5
	}
	if user.InteractionCount > 50 {
		engagementScore = 1.0
	}

	similarityScore := 0.5

	total := a.config.InteractionDepthWeight*interactionScore +
		a.config.SimilarityWeight*similarityScore +
		a.config.RecencyWeight*recencyScore +
		a.config.EngagementWeight*engagementScore

	return math.Max(0, math.Min(1, total))
}

func (a *Agent) execute(ctx context.Context, d Decision) error {
	id := uuid.New().String()

	switch d.Action {
	case ActionLike:
		post, err := a.getPost(ctx, d.TargetID)
		if err != nil {
			return err
		}
		if err := a.bluesky.Like(ctx, d.TargetID, post.CID); err != nil {
			return err
		}
		a.memory.IncrementInteraction(ctx, post.Author.DID)
		a.memory.RecordInteraction(ctx, memory.Interaction{
			ID:      id,
			UserDID: post.Author.DID,
			PostID:  d.TargetID,
			Type:    "like",
			Context: d.Reason,
			Outcome: "success",
		})

	case ActionFollow:
		if err := a.bluesky.Follow(ctx, d.TargetID); err != nil {
			return err
		}
		a.memory.IncrementInteraction(ctx, d.TargetID)
		a.memory.RecordInteraction(ctx, memory.Interaction{
			ID:      id,
			UserDID: d.TargetID,
			Type:    "follow",
			Context: d.Reason,
			Outcome: "success",
		})

	case ActionReply:
		post, err := a.getPost(ctx, d.TargetID)
		if err != nil {
			return err
		}
		replyText := a.generateReply(post)
		sanitized := sanitize.Sanitize(replyText, sanitize.ContentTypePost, "reply", a.sanitizer)
		if sanitized.IsEmpty() {
			return fmt.Errorf("reply content empty after sanitization")
		}
		_, err = a.bluesky.Reply(ctx, sanitized.Cleaned, d.TargetID, post.CID)
		if err != nil {
			return err
		}
		a.memory.IncrementInteraction(ctx, post.Author.DID)
		a.memory.RecordInteraction(ctx, memory.Interaction{
			ID:      id,
			UserDID: post.Author.DID,
			PostID:  d.TargetID,
			Type:    "reply",
			Context: d.Reason,
			Outcome: "success",
		})

	case ActionPost:
		return fmt.Errorf("post action not implemented")
	}

	return nil
}

func (a *Agent) generateReply(post *bluesky.Post) string {
	return a.personality.RandomReply()
}

func (a *Agent) getPost(ctx context.Context, uri string) (*bluesky.Post, error) {
	return a.bluesky.GetPost(ctx, uri)
}

func (a *Agent) pruneLowSignalUsers(ctx context.Context) error {
	slog.Debug("checking for stale follows to unfollow")

	records, err := a.bluesky.ListFollowRecords(ctx)
	if err != nil {
		return fmt.Errorf("failed to list follow records: %w", err)
	}

	unfollowed := 0
	for _, record := range records {
		user, err := a.memory.GetUser(ctx, record.SubjectDID)
		if err != nil {
			continue
		}

		if user.SignalTier == memory.SignalLow &&
			!user.LastInteraction.IsZero() &&
			time.Since(user.LastInteraction) > a.config.UnfollowAfter {

			if err := a.bluesky.Unfollow(ctx, record.URI); err != nil {
				slog.Warn("failed to unfollow", "did", record.SubjectDID, "error", err)
				continue
			}
			unfollowed++
			slog.Info("unfollowed low-signal user",
				"handle", user.Handle,
				"last_interaction", user.LastInteraction,
			)
		}
	}

	if unfollowed > 0 {
		slog.Info("pruned low-signal follows", "count", unfollowed)
	}

	return nil
}

func (a *Agent) reflect(ctx context.Context) error {
	if a.config.ControlUserHandle == "" {
		slog.Debug("skipping reflection - no control user configured")
		return nil
	}

	slog.Info("generating daily reflection")

	stats, err := a.memory.GetDailyStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to get daily stats: %w", err)
	}

	content := a.generateReflectionContent(stats)

	sanitized := sanitize.Sanitize(content, sanitize.ContentTypePost, "reflection", a.sanitizer)
	if sanitized.IsEmpty() {
		return fmt.Errorf("reflection content empty after sanitization")
	}

	uri, err := a.bluesky.Post(ctx, sanitized.Cleaned)
	if err != nil {
		return fmt.Errorf("failed to post reflection: %w", err)
	}

	a.lastReflection = time.Now()
	slog.Info("posted daily reflection", "uri", uri, "control_user", a.config.ControlUserHandle)

	return nil
}

func (a *Agent) generateReflectionContent(stats memory.DailyStats) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("%s %s", a.personality.RandomIntro(), formatTimestamp(time.Now())))

	if a.config.ControlUserHandle != "" {
		parts = append(parts, fmt.Sprintf(". @%s ", a.config.ControlUserHandle))
	}

	var observations []string

	if stats.NewHighSignalUsers > 0 {
		observations = append(observations,
			fmt.Sprintf("%s %d new high-signal accounts", a.personality.RandomPhrase(), stats.NewHighSignalUsers))
	}

	if stats.TotalInteractions > 0 {
		observations = append(observations,
			fmt.Sprintf("Processed %d interactions", stats.TotalInteractions))
	}

	if stats.PostsAnalyzed > 0 {
		observations = append(observations,
			fmt.Sprintf("Analyzed %d posts", stats.PostsAnalyzed))
	}

	if len(stats.Topics) > 0 {
		observations = append(observations,
			fmt.Sprintf("Themes: %s", joinTopics(stats.Topics, 3)))
	}

	if len(observations) == 0 {
		observations = append(observations, "Quiet period. Monitoring continues.")
	}

	parts = append(parts, observations...)
	parts = append(parts, a.personality.RandomOutro())

	return strings.Join(parts, " ")
}

func (a *Agent) postResearch(ctx context.Context) error {
	if len(a.config.ResearchTopics) == 0 {
		slog.Debug("skipping research post - no topics configured")
		return nil
	}

	slog.Info("generating research post")

	topic := a.config.ResearchTopics[int(time.Now().UnixNano())%len(a.config.ResearchTopics)]

	searchURL := fmt.Sprintf("https://duckduckgo.com/html/?q=%s", url.QueryEscape(topic))
	result := a.research.Fetch(ctx, searchURL)

	if !result.IsValid() {
		return fmt.Errorf("failed to fetch research for topic: %s", topic)
	}

	content := a.generateResearchContent(topic, result)

	sanitized := sanitize.Sanitize(content, sanitize.ContentTypePost, "research", a.sanitizer)
	if sanitized.IsEmpty() {
		return fmt.Errorf("research content empty after sanitization")
	}

	uri, err := a.bluesky.Post(ctx, sanitized.Cleaned)
	if err != nil {
		return fmt.Errorf("failed to post research: %w", err)
	}

	a.lastResearchPost = time.Now()
	slog.Info("posted research", "uri", uri, "topic", topic)

	return nil
}

func (a *Agent) generateResearchContent(topic string, result research.FetchResult) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("%s about '%s':", a.personality.RandomPhrase(), topic))

	summary := truncateText(result.Content, 200)

	parts = append(parts, summary)
	parts = append(parts, a.personality.RandomOutro())

	return strings.Join(parts, " ")
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatTimestamp(t time.Time) string {
	return t.Format("2006-01-02")
}

func joinTopics(topics []string, max int) string {
	if len(topics) > max {
		topics = topics[:max]
	}
	return strings.Join(topics, ", ")
}

func randomFloat() float64 {
	return float64(time.Now().UnixNano()%1000) / 1000.0
}
