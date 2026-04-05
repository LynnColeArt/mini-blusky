package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

type SignalTier string

const (
	SignalHigh SignalTier = "high"
	SignalLow  SignalTier = "low"
)

type User struct {
	DID              string
	Handle           string
	SignalTier       SignalTier
	InteractionCount int
	LastInteraction  time.Time
	Embedding        []float32
}

type Post struct {
	ID          string
	AuthorDID   string
	Content     string
	Embedding   []float32
	SignalScore float64
	CreatedAt   time.Time
}

type Interaction struct {
	ID        string
	UserDID   string
	PostID    string
	Type      string
	Context   string
	Embedding []float32
	Outcome   string
	CreatedAt time.Time
}

type Memory struct {
	db *sql.DB
}

func New(dbPath string) (*Memory, error) {
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open duckdb: %w", err)
	}

	m := &Memory{db: db}
	if err := m.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	if err := m.CreateMissionTable(); err != nil {
		return nil, fmt.Errorf("failed to create mission tables: %w", err)
	}

	return m, nil
}

func (m *Memory) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		did VARCHAR PRIMARY KEY,
		handle VARCHAR,
		signal_tier VARCHAR DEFAULT 'low',
		interaction_count INTEGER DEFAULT 0,
		last_interaction TIMESTAMP,
		embedding FLOAT[768],
		created_at TIMESTAMP DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS posts (
		id VARCHAR PRIMARY KEY,
		author_did VARCHAR,
		content TEXT,
		embedding FLOAT[768],
		signal_score FLOAT DEFAULT 0.0,
		created_at TIMESTAMP,
		FOREIGN KEY (author_did) REFERENCES users(did)
	);

	CREATE TABLE IF NOT EXISTS interactions (
		id VARCHAR PRIMARY KEY,
		user_did VARCHAR,
		post_id VARCHAR,
		type VARCHAR,
		context TEXT,
		embedding FLOAT[768],
		outcome VARCHAR,
		created_at TIMESTAMP DEFAULT NOW(),
		FOREIGN KEY (user_did) REFERENCES users(did),
		FOREIGN KEY (post_id) REFERENCES posts(id)
	);

	CREATE INDEX IF NOT EXISTS idx_users_signal ON users(signal_tier);
	CREATE INDEX IF NOT EXISTS idx_posts_created ON posts(created_at);
	CREATE INDEX IF NOT EXISTS idx_interactions_user ON interactions(user_did);
	`

	_, err := m.db.Exec(schema)
	return err
}

func (m *Memory) Close() error {
	return m.db.Close()
}

func (m *Memory) UpsertUser(ctx context.Context, u User) error {
	query := `
	INSERT INTO users (did, handle, signal_tier, interaction_count, last_interaction, embedding)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT (did) DO UPDATE SET
		handle = EXCLUDED.handle,
		signal_tier = EXCLUDED.signal_tier,
		interaction_count = EXCLUDED.interaction_count,
		last_interaction = EXCLUDED.last_interaction,
		embedding = EXCLUDED.embedding
	`
	_, err := m.db.ExecContext(ctx, query,
		u.DID, u.Handle, string(u.SignalTier), u.InteractionCount, u.LastInteraction, u.Embedding)
	return err
}

func (m *Memory) GetUser(ctx context.Context, did string) (*User, error) {
	query := `SELECT did, handle, signal_tier, interaction_count, last_interaction FROM users WHERE did = ?`
	row := m.db.QueryRowContext(ctx, query, did)

	var u User
	var tierStr string
	err := row.Scan(&u.DID, &u.Handle, &tierStr, &u.InteractionCount, &u.LastInteraction)
	if err != nil {
		return nil, err
	}
	u.SignalTier = SignalTier(tierStr)
	return &u, nil
}

func (m *Memory) GetHighSignalUsers(ctx context.Context, limit int) ([]User, error) {
	query := `SELECT did, handle, signal_tier, interaction_count, last_interaction 
			  FROM users WHERE signal_tier = 'high' ORDER BY interaction_count DESC LIMIT ?`

	rows, err := m.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var tierStr string
		if err := rows.Scan(&u.DID, &u.Handle, &tierStr, &u.InteractionCount, &u.LastInteraction); err != nil {
			return nil, err
		}
		u.SignalTier = SignalTier(tierStr)
		users = append(users, u)
	}
	return users, nil
}

func (m *Memory) RecordInteraction(ctx context.Context, i Interaction) error {
	query := `
	INSERT INTO interactions (id, user_did, post_id, type, context, embedding, outcome, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := m.db.ExecContext(ctx, query,
		i.ID, i.UserDID, i.PostID, i.Type, i.Context, i.Embedding, i.Outcome, i.CreatedAt)
	return err
}

func (m *Memory) StorePost(ctx context.Context, p Post) error {
	query := `
	INSERT INTO posts (id, author_did, content, embedding, signal_score, created_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT (id) DO UPDATE SET
		signal_score = EXCLUDED.signal_score
	`
	_, err := m.db.ExecContext(ctx, query,
		p.ID, p.AuthorDID, p.Content, p.Embedding, p.SignalScore, p.CreatedAt)
	return err
}

func (m *Memory) UpdateSignalTier(ctx context.Context, did string, tier SignalTier) error {
	query := `UPDATE users SET signal_tier = ? WHERE did = ?`
	_, err := m.db.ExecContext(ctx, query, string(tier), did)
	return err
}

func (m *Memory) IncrementInteraction(ctx context.Context, did string) error {
	query := `
	UPDATE users SET 
		interaction_count = interaction_count + 1,
		last_interaction = NOW()
	WHERE did = ?
	`
	_, err := m.db.ExecContext(ctx, query, did)
	return err
}

func (m *Memory) SimilarPosts(ctx context.Context, embedding []float32, limit int) ([]Post, error) {
	query := `
	SELECT id, author_did, content, signal_score, created_at 
	FROM posts 
	ORDER BY array_cosine_similarity(embedding, ?::FLOAT[768]) DESC 
	LIMIT ?
	`
	rows, err := m.db.QueryContext(ctx, query, embedding, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.AuthorDID, &p.Content, &p.SignalScore, &p.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func (m *Memory) GetRecentInteractions(ctx context.Context, limit int) ([]Interaction, error) {
	query := `
	SELECT id, user_did, post_id, type, context, outcome, created_at 
	FROM interactions 
	ORDER BY created_at DESC 
	LIMIT ?
	`
	rows, err := m.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var interactions []Interaction
	for rows.Next() {
		var i Interaction
		if err := rows.Scan(&i.ID, &i.UserDID, &i.PostID, &i.Type, &i.Context, &i.Outcome, &i.CreatedAt); err != nil {
			return nil, err
		}
		interactions = append(interactions, i)
	}
	return interactions, nil
}

type DailyStats struct {
	NewHighSignalUsers int
	TotalInteractions  int
	PostsAnalyzed      int
	Topics             []string
}

func (m *Memory) GetDailyStats(ctx context.Context) (DailyStats, error) {
	stats := DailyStats{}

	err := m.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users 
		WHERE signal_tier = 'high' 
		AND created_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&stats.NewHighSignalUsers)
	if err != nil {
		stats.NewHighSignalUsers = 0
	}

	err = m.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM interactions 
		WHERE created_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&stats.TotalInteractions)
	if err != nil {
		stats.TotalInteractions = 0
	}

	err = m.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM posts 
		WHERE created_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&stats.PostsAnalyzed)
	if err != nil {
		stats.PostsAnalyzed = 0
	}

	stats.Topics = m.extractTopics(ctx)

	return stats, nil
}

func (m *Memory) extractTopics(ctx context.Context) []string {
	rows, err := m.db.QueryContext(ctx, `
		SELECT content FROM posts 
		WHERE created_at >= NOW() - INTERVAL '24 hours'
		LIMIT 100
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	wordCounts := make(map[string]int)
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			continue
		}
		words := tokenize(content)
		for _, word := range words {
			if len(word) > 4 {
				wordCounts[word]++
			}
		}
	}

	var topics []string
	for word, count := range wordCounts {
		if count >= 2 {
			topics = append(topics, word)
		}
		if len(topics) >= 5 {
			break
		}
	}

	return topics
}

func tokenize(s string) []string {
	var tokens []string
	var current string
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
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
