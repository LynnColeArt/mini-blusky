package agent

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lynn/mini-bluesky/internal/memory"
)

type MissionType string

const (
	MissionResearch MissionType = "research"
	MissionTrack    MissionType = "track"
	MissionReport   MissionType = "report"
	MissionNote     MissionType = "note"
)

type ParsedMission struct {
	Type   MissionType
	Target string
	Raw    string
}

var (
	missionResearchRe = regexp.MustCompile(`(?i)^research\s+(?:topic\s+)?:?\s*(.+)$`)
	missionTrackRe    = regexp.MustCompile(`(?i)^track\s+(?:user\s+)?:?\s*(.+)$`)
	missionReportRe   = regexp.MustCompile(`(?i)^report\s+(?:on\s+)?:?\s*(.+)$`)
	missionNoteRe     = regexp.MustCompile(`(?i)^note\s*:\s*(.+)$`)
)

func (a *Agent) checkDMs(ctx context.Context) error {
	if a.controlUserDID == "" {
		slog.Debug("skipping DM check - no control user configured")
		return nil
	}

	slog.Debug("checking for DMs from control user")

	convos, err := a.bluesky.GetConversations(ctx, 50)
	if err != nil {
		return fmt.Errorf("failed to get conversations: %w", err)
	}

	var controlConvoID string
	for _, convo := range convos {
		messages, err := a.bluesky.GetMessages(ctx, convo.ID, 1)
		if err != nil {
			continue
		}
		for _, msg := range messages {
			if msg.SenderDID == a.controlUserDID {
				controlConvoID = convo.ID
				break
			}
		}
		if controlConvoID != "" {
			break
		}
	}

	if controlConvoID == "" {
		slog.Debug("no conversation found with control user")
		return nil
	}

	a.controlUserConvo = controlConvoID

	messages, err := a.bluesky.GetMessages(ctx, controlConvoID, 20)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}

	for _, msg := range messages {
		if msg.SenderDID != a.controlUserDID {
			continue
		}

		processed, err := a.memory.IsDMProcessed(ctx, msg.ID)
		if err != nil {
			slog.Warn("failed to check DM status", "id", msg.ID, "error", err)
			continue
		}
		if processed {
			continue
		}

		mission := parseMission(msg.Text)
		if mission == nil {
			slog.Debug("DM is not a mission command", "text", msg.Text)
			a.memory.MarkDMProcessed(ctx, msg.ID)
			continue
		}

		slog.Info("received mission from control user",
			"type", mission.Type,
			"target", mission.Target,
		)

		if err := a.createMission(ctx, *mission); err != nil {
			slog.Error("failed to create mission", "error", err)
			continue
		}

		if err := a.memory.MarkDMProcessed(ctx, msg.ID); err != nil {
			slog.Warn("failed to mark DM processed", "id", msg.ID, "error", err)
		}
	}

	return nil
}

func parseMission(text string) *ParsedMission {
	text = strings.TrimSpace(text)

	if matches := missionResearchRe.FindStringSubmatch(text); len(matches) > 1 {
		return &ParsedMission{
			Type:   MissionResearch,
			Target: strings.TrimSpace(matches[1]),
			Raw:    text,
		}
	}

	if matches := missionTrackRe.FindStringSubmatch(text); len(matches) > 1 {
		return &ParsedMission{
			Type:   MissionTrack,
			Target: strings.TrimSpace(matches[1]),
			Raw:    text,
		}
	}

	if matches := missionReportRe.FindStringSubmatch(text); len(matches) > 1 {
		return &ParsedMission{
			Type:   MissionReport,
			Target: strings.TrimSpace(matches[1]),
			Raw:    text,
		}
	}

	if matches := missionNoteRe.FindStringSubmatch(text); len(matches) > 1 {
		return &ParsedMission{
			Type:   MissionNote,
			Target: strings.TrimSpace(matches[1]),
			Raw:    text,
		}
	}

	return nil
}

func (a *Agent) createMission(ctx context.Context, parsed ParsedMission) error {
	mission := memory.Mission{
		ID:         uuid.New().String(),
		Type:       string(parsed.Type),
		Target:     parsed.Target,
		Status:     "pending",
		AssignedAt: time.Now(),
	}
	return a.memory.CreateMission(ctx, mission)
}

func (a *Agent) processMissions(ctx context.Context) error {
	missions, err := a.memory.GetPendingMissions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get pending missions: %w", err)
	}

	for _, mission := range missions {
		slog.Info("processing mission", "id", mission.ID, "type", mission.Type, "target", mission.Target)

		var result string
		switch MissionType(mission.Type) {
		case MissionResearch:
			result, err = a.executeResearchMission(ctx, mission.Target)
		case MissionTrack:
			result, err = a.executeTrackMission(ctx, mission.Target)
		case MissionReport:
			result, err = a.executeReportMission(ctx, mission.Target)
		case MissionNote:
			result, err = a.executeNoteMission(ctx, mission.Target)
		default:
			err = fmt.Errorf("unknown mission type: %s", mission.Type)
		}

		if err != nil {
			slog.Error("mission failed", "id", mission.ID, "error", err)
			result = fmt.Sprintf("Mission failed: %s", err.Error())
		}

		if err := a.memory.CompleteMission(ctx, mission.ID, result); err != nil {
			slog.Error("failed to complete mission", "id", mission.ID, "error", err)
		}

		if a.controlUserConvo != "" {
			response := fmt.Sprintf("%s. %s", a.personality.RandomPhrase(), result)
			if err := a.bluesky.SendDM(ctx, a.controlUserConvo, response); err != nil {
				slog.Error("failed to send DM response", "error", err)
			}
		}
	}

	return nil
}

func (a *Agent) executeResearchMission(ctx context.Context, topic string) (string, error) {
	searchURL := fmt.Sprintf("https://duckduckgo.com/html/?q=%s", topic)
	result := a.research.Fetch(ctx, searchURL)

	if !result.IsValid() {
		return "", fmt.Errorf("failed to fetch research for topic: %s", topic)
	}

	postContent := fmt.Sprintf("%s about '%s': %s. %s",
		a.personality.RandomPhrase(),
		topic,
		truncateText(result.Content, 150),
		a.personality.RandomOutro(),
	)

	uri, err := a.bluesky.Post(ctx, postContent)
	if err != nil {
		return "", fmt.Errorf("failed to post research: %w", err)
	}

	return fmt.Sprintf("Research posted: %s", uri), nil
}

func (a *Agent) executeTrackMission(ctx context.Context, target string) (string, error) {
	did := target
	if !strings.HasPrefix(did, "did:") {
		resolved, err := a.bluesky.ResolveHandle(ctx, target)
		if err != nil {
			return "", fmt.Errorf("failed to resolve handle: %w", err)
		}
		did = resolved
	}

	user := memory.User{
		DID:        did,
		Handle:     target,
		SignalTier: memory.SignalHigh,
	}
	if err := a.memory.UpsertUser(ctx, user); err != nil {
		return "", fmt.Errorf("failed to track user: %w", err)
	}

	if err := a.bluesky.Follow(ctx, did); err != nil {
		slog.Warn("failed to follow tracked user", "did", did, "error", err)
	}

	return fmt.Sprintf("Now tracking: %s", target), nil
}

func (a *Agent) executeReportMission(ctx context.Context, topic string) (string, error) {
	stats, err := a.memory.GetDailyStats(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get stats: %w", err)
	}

	report := fmt.Sprintf(
		"Report on '%s': %d high-signal users, %d interactions, %d posts analyzed. Topics: %s",
		topic,
		stats.NewHighSignalUsers,
		stats.TotalInteractions,
		stats.PostsAnalyzed,
		strings.Join(stats.Topics, ", "),
	)

	return report, nil
}

func (a *Agent) executeNoteMission(ctx context.Context, note string) (string, error) {
	mission := memory.Mission{
		ID:         uuid.New().String(),
		Type:       "note",
		Target:     note,
		Status:     "completed",
		AssignedAt: time.Now(),
		Result:     note,
	}

	if err := a.memory.CreateMission(ctx, mission); err != nil {
		return "", fmt.Errorf("failed to save note: %w", err)
	}

	return "Note saved.", nil
}
