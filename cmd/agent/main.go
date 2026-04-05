package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/lynn/mini-bluesky/internal/agent"
	"github.com/lynn/mini-bluesky/internal/bluesky"
	"github.com/lynn/mini-bluesky/internal/embed"
	"github.com/lynn/mini-bluesky/internal/memory"
	"github.com/lynn/mini-bluesky/internal/research"
	"github.com/lynn/mini-bluesky/internal/vision"
)

func main() {
	if err := godotenv.Load(); err != nil {
		slog.Debug("no .env file found, using environment variables")
	}

	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})))

	handle := getEnv("BLUESKY_HANDLE", "")
	password := getEnv("BLUESKY_PASSWORD", "")
	dbPath := getEnv("DATABASE_PATH", "/app/data/agent.db")
	modelPath := getEnv("MODEL_PATH", "/app/models/nomic-embed-text.Q4_K_M.gguf")
	controlUser := getEnv("CONTROL_USER_HANDLE", "")
	personality := getEnv("AGENT_PERSONALITY", "field-agent")
	researchTopicsStr := getEnv("RESEARCH_TOPICS", "")

	if handle == "" || password == "" {
		slog.Error("BLUESKY_HANDLE and BLUESKY_PASSWORD are required")
		os.Exit(1)
	}

	slog.Info("starting mini-bluesky agent",
		"handle", handle,
		"database", dbPath,
	)

	mem, err := memory.New(dbPath)
	if err != nil {
		slog.Error("failed to initialize memory", "error", err)
		os.Exit(1)
	}
	defer mem.Close()

	emb, err := embed.New(embed.DefaultConfig(modelPath))
	if err != nil {
		slog.Warn("failed to load embedding model, using stub", "error", err)
		emb, err = embed.New(embed.Config{})
		if err != nil {
			slog.Error("failed to create stub embedder", "error", err)
			os.Exit(1)
		}
	}
	defer emb.Close()

	bsky := bluesky.NewClient(handle, password)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := bsky.Authenticate(ctx); err != nil {
		slog.Error("failed to authenticate with bluesky", "error", err)
		cancel()
		os.Exit(1)
	}
	cancel()

	slog.Info("authenticated with bluesky", "did", bsky.DID())

	fetcher := research.NewFetcher(research.DefaultConfig())

	visionCfg := vision.DefaultConfig()
	visionClient := vision.NewClient(visionCfg)

	var researchTopics []string
	if researchTopicsStr != "" {
		for _, t := range strings.Split(researchTopicsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				researchTopics = append(researchTopics, t)
			}
		}
	}

	cfg := agent.DefaultConfig()
	cfg.ControlUserHandle = controlUser
	cfg.Personality = personality
	cfg.ResearchTopics = researchTopics

	ag := agent.New(cfg, bsky, mem, emb, fetcher, visionClient)

	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := ag.Init(initCtx); err != nil {
		slog.Error("failed to initialize agent", "error", err)
		initCancel()
		os.Exit(1)
	}
	initCancel()

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	if err := ag.Start(ctx); err != nil && err != context.Canceled {
		slog.Error("agent error", "error", err)
		os.Exit(1)
	}

	slog.Info("agent shutdown complete")
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
