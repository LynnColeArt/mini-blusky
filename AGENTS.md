# Agent Instructions

## Build Commands
```bash
# Build with stub embeddings (for local dev)
go build ./...

# Build with llama.cpp embeddings (requires llama.cpp libs)
# See Dockerfile for build requirements
go build -tags llama -o agent ./cmd/agent
```

## Test Commands
```bash
go test ./...
```

## Lint/Typecheck
```bash
go vet ./...
```

## Run
```bash
# Local dev (uses stub embeddings)
go run ./cmd/agent

# With llama.cpp (after building)
./agent
```

## Docker
```bash
# Download model first
./scripts/download-model.sh

# Build and run (includes llama.cpp)
docker-compose up --build
```

## Environment Variables
- `BLUESKY_HANDLE` - Your Bluesky handle
- `BLUESKY_PASSWORD` - App password (not account password)
- `CONTROL_USER_HANDLE` - Handle to tag in reflections AND send DM missions (optional)
- `AGENT_PERSONALITY` - Personality: field-agent, friendly, analyst (default: field-agent)
- `RESEARCH_TOPICS` - Comma-separated topics for research posts (optional)
- `VLM_BASE_URL` - Base URL for VLM API (default: https://api.openai.com/v1)
- `VLM_MODEL` - Model for vision (e.g., gpt-4o-mini)
- `VLM_API_KEY` - API key for vision (optional, required for image analysis)
- `DATABASE_PATH` - Path to DuckDB file (default: /app/data/agent.db)
- `MODEL_PATH` - Path to GGUF embedding model (default: /app/models/nomic-embed-text.Q4_K_M.gguf)
- `LOG_LEVEL` - debug, info, warn, error (default: info)

## DM Mission Commands

DM the agent (from `CONTROL_USER_HANDLE` account) with these commands:

| Command | Example | What it does |
|---------|---------|--------------|
| `research topic: <X>` | `research topic: decentralized identity` | Researches and posts about topic |
| `track user: <handle>` | `track user: alice.bsky.social` | Marks user as high-signal, follows them |
| `report on: <topic>` | `report on: network stats` | Replies with current stats |
| `note: <text>` | `note: check X later` | Saves a note to memory |

Agent responds via DM with results. Only `CONTROL_USER_HANDLE` can send missions.
