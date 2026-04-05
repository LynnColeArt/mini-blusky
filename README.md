# Mini Bluesky Agent

An autonomous Bluesky agent with vector memory, signal-based relationship tracking, and configurable personality. Built in Go with DuckDB for storage and llama.cpp for embeddings.

## What It Does

- **Reads and analyzes** posts from your Bluesky timeline
- **Tracks relationships** by classifying users into high/low signal tiers based on interaction depth and content relevance
- **Engages autonomously** - likes, replies, and follows based on signal quality
- **Posts reflections** daily, tagging a control user with observations
- **Accepts DM missions** from a designated control user (research topics, track users, save notes)
- **Discovers trending topics** from high-signal accounts and posts research
- **Detects and blocks** toxic content and harassment
- **Appreciates art** in images via VLM integration (optional)

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Agent Process                            │
├──────────────┬──────────────┬──────────────┬───────────────────┤
│   Bluesky    │    Memory    │    Embed     │      Vision       │
│   Client     │   (DuckDB)   │  (llama.cpp) │   (OpenAI API)    │
└──────┬───────┴──────┬───────┴──────┬───────┴─────────┬─────────┘
       │              │              │                 │
       └──────────────┴──────┬───────┴─────────────────┘
                             │
                  ┌──────────▼──────────┐
                  │  Sanitization Layer │  ← All external data
                  │   (Untrusted Data)  │
                  └──────────┬──────────┘
                             │
              ┌──────────────▼──────────────┐
              │       Agent Core Loop       │
              │  ┌───────────────────────┐  │
              │  │   Signal Grouping     │  │
              │  │     High / Low        │  │
              │  └───────────────────────┘  │
              └─────────────────────────────┘
```

### Components

| Package | Purpose |
|---------|---------|
| `internal/bluesky` | AT Protocol client for posting, following, DMs, blocking |
| `internal/memory` | DuckDB storage with vector columns for semantic search |
| `internal/embed` | llama.cpp CGO bindings for local embeddings (stub fallback available) |
| `internal/vision` | OpenAI-compatible VLM for image analysis |
| `internal/agent` | Core decision loop, signal scoring, mission handling |
| `internal/sanitize` | Input sanitization to prevent prompt injection |
| `internal/research` | Web fetching and text extraction |

## Signal Scoring

Users are classified into signal tiers based on:

```
signal_score = (interaction_depth × 0.4) + 
               (embedding_similarity × 0.3) + 
               (recency_factor × 0.2) + 
               (engagement_rate × 0.1)
```

- **High signal (≥0.7)**: Agent engages with their posts, replies, follows
- **Low signal (≤0.3)**: Agent may unfollow after 7 days of inactivity

Signal tier affects:
- Like probability (30% for high-signal posts)
- Reply probability (5% for high-signal posts)
- Follow probability (10% for new high-signal users)
- Topic discovery weight (high-signal posts influence trending topics)

## Personality System

Three built-in personalities affect the agent's voice:

| Personality | Tone | Example Reply |
|-------------|------|---------------|
| `field-agent` | Observational, detached, mysterious | "Noted." |
| `friendly` | Warm, curious, approachable | "Thanks for sharing!" |
| `analyst` | Analytical, precise, data-driven | "Data point noted." |

Personalities provide templates for:
- Reply messages
- Daily reflection intros/outros
- Research post phrasing

## DM Mission System

Send commands via DM to the agent (only from `CONTROL_USER_HANDLE`):

| Command | Example | Action |
|---------|---------|--------|
| `research topic: <X>` | `research topic: decentralized identity` | Fetches info, posts summary |
| `track user: <handle>` | `track user: alice.bsky.social` | Marks as high-signal, follows |
| `report on: <topic>` | `report on: network stats` | DMs you current stats |
| `note: <text>` | `note: remember to check X` | Saves note to memory |

Agent responds via DM with results. All other DMs are ignored.

## Toxic Content Protection

Pattern-based detection for:
- Hate speech and slurs
- Harassment and brigading
- Spam indicators

Response levels:
- **High toxicity**: Automatic block
- **Medium toxicity**: 50% chance of block, always demoted to low-signal
- **Low toxicity**: Demoted to low-signal

## Security: Prompt Injection Prevention

All external data (posts, profiles, web content) is:
1. Classified as **UNTRUSTED**
2. Passed through sanitization layer
3. Never used as instruction, only as data

The agent's decisions are code-driven, not prompt-driven. External content cannot influence agent behavior beyond signal scoring.

## Setup

### Prerequisites

- Go 1.22+
- Docker (for containerized deployment)
- Bluesky account with app password
- (Optional) GGUF embedding model for real embeddings
- (Optional) VLM API key for image analysis

### Local Development

1. Clone and configure:
```bash
git clone <repo>
cd mini-bluesky2
cp .env.example .env
# Edit .env with your credentials
```

2. Run with stub embeddings (no model download needed):
```bash
go run ./cmd/agent
```

3. Or build and run:
```bash
go build -o agent ./cmd/agent
./agent
```

### Docker Deployment

1. Download embedding model (optional - stubs work without it):
```bash
./scripts/download-model.sh
```

2. Build and run:
```bash
docker-compose up --build
```

The Docker build compiles llama.cpp from source for native embeddings.

### Configuration

Environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `BLUESKY_HANDLE` | Yes | - | Agent's Bluesky handle |
| `BLUESKY_PASSWORD` | Yes | - | App password (not account password) |
| `CONTROL_USER_HANDLE` | No | - | Handle for DM missions and reflection tags |
| `AGENT_PERSONALITY` | No | `field-agent` | Personality: field-agent, friendly, analyst |
| `RESEARCH_TOPICS` | No | - | Comma-separated topics for scheduled research |
| `VLM_BASE_URL` | No | `https://api.openai.com/v1` | VLM API base URL |
| `VLM_MODEL` | No | `gpt-4o-mini` | Vision model name |
| `VLM_API_KEY` | No | - | API key for image analysis |
| `DATABASE_PATH` | No | `/app/data/agent.db` | DuckDB file path |
| `MODEL_PATH` | No | `/app/models/nomic-embed-text.Q4_K_M.gguf` | GGUF model path |
| `LOG_LEVEL` | No | `info` | debug, info, warn, error |

### Creating a Bluesky App Password

1. Go to Settings → Privacy and Security
2. Click "App passwords"
3. Create new password with label "Mini Bluesky Agent"
4. Use this password for `BLUESKY_PASSWORD`

## Schedules

The agent runs multiple independent loops:

| Loop | Interval | Action |
|------|----------|--------|
| Timeline check | 5 min | Read posts, score users, engage |
| Reflection | 24 hours | Post daily summary tagging control user |
| Research | 12 hours | Post about configured topics |
| Discovery | 8 hours | Find trending topics from high-signal users, post research |
| Unfollow check | 6 hours | Prune stale low-signal follows |
| DM check | 2 min | Check for missions from control user |

## Memory Schema

DuckDB tables:

```sql
-- Users and their signal classification
users (
  did VARCHAR PRIMARY KEY,
  handle VARCHAR,
  signal_tier VARCHAR,  -- 'high' or 'low'
  interaction_count INTEGER,
  last_interaction TIMESTAMP,
  embedding FLOAT[768]
)

-- Posts with embeddings for similarity search
posts (
  id VARCHAR PRIMARY KEY,
  author_did VARCHAR,
  content TEXT,
  embedding FLOAT[768],
  signal_score FLOAT,
  created_at TIMESTAMP
)

-- Interaction history
interactions (
  id VARCHAR PRIMARY KEY,
  user_did VARCHAR,
  post_id VARCHAR,
  type VARCHAR,  -- 'like', 'reply', 'follow', 'block'
  context TEXT,
  embedding FLOAT[768],
  outcome VARCHAR,
  created_at TIMESTAMP
)

-- DM missions
missions (
  id VARCHAR PRIMARY KEY,
  type VARCHAR,  -- 'research', 'track', 'report', 'note'
  target TEXT,
  status VARCHAR,  -- 'pending', 'completed'
  assigned_at TIMESTAMP,
  completed_at TIMESTAMP,
  result TEXT
)
```

## File Structure

```
cmd/agent/main.go           - Entry point
internal/
  agent/
    agent.go                - Core loop, decision logic
    personality.go          - Personality templates
    missions.go             - DM mission parsing and execution
    discovery.go            - Topic discovery and toxicity detection
  bluesky/client.go         - AT Protocol client
  memory/
    memory.go               - DuckDB operations
    missions.go             - Mission storage
  embed/
    embed.go                - Stub embeddings (default)
    embed_llama.go          - llama.cpp CGO (build tag: llama)
  vision/client.go          - VLM API client
  research/fetch.go         - Web research
  sanitize/sanitize.go      - Input sanitization
scripts/download-model.sh   - Model download helper
Dockerfile                  - Multi-stage build with llama.cpp
docker-compose.yaml
.env.example
PLAN.md                     - Architecture documentation
AGENTS.md                   - Build/test commands
```

## Building with Real Embeddings

Default build uses stub embeddings (random normalized vectors). For semantic search:

```bash
# Build with llama.cpp support
go build -tags llama -o agent ./cmd/agent

# Download model
./scripts/download-model.sh

# Run
MODEL_PATH=./models/nomic-embed-text-v1.5.Q4_K_M.gguf ./agent
```

The Docker build includes llama.cpp automatically.

## Logs

JSON structured logging to stdout. Set `LOG_LEVEL=debug` for verbose output including:
- Post analysis decisions
- Signal tier changes
- Mission processing
- API retry attempts

## Extending

### Add a New Personality

Edit `internal/agent/personality.go`:

```go
"my-personality": {
    Name: "my-personality",
    Tone: "descriptive tone",
    ReplyTemplates: []string{"Response 1", "Response 2"},
    Intros: []string{"Intro 1", "Intro 2"},
    Outros: []string{"Outro 1", "Outro 2"},
    Phrases: []string{"Phrase 1", "Phrase 2"},
},
```

### Add a New Mission Type

1. Add parser regex in `internal/agent/missions.go`
2. Add `execute*Mission` method
3. Wire into `processMissions` switch statement

### Add New Decision Factors

Modify `calculateSignalScore` in `internal/agent/agent.go` to include additional weighting factors.

## Troubleshooting

**"failed to authenticate with bluesky"**
- Verify handle and app password are correct
- Ensure using app password, not account password

**"failed to load model"**
- Check MODEL_PATH points to valid GGUF file
- Or run without model (uses stub embeddings)

**Agent not responding to DMs**
- Verify CONTROL_USER_HANDLE matches your handle exactly
- Check DM was sent after agent started
- Check logs for "received mission from control user"

**Embeddings seem random**
- By default, stub embeddings are used
- Build with `-tags llama` and provide MODEL_PATH for real embeddings

**VLM not working**
- Set VLM_API_KEY environment variable
- Verify VLM_BASE_URL is correct for your provider
- Check model name in VLM_MODEL
