package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	sessionMapKeyPrefix  = "session_map:"
	sessionMetaKeyPrefix = "session_meta:"
)

type conversationSession struct {
	ConversationKey string `json:"conversation_key"`
	SessionID       string `json:"session_id"`
	BotID           string `json:"bot_id"`
	UserID          string `json:"user_id"`
	ChannelID       string `json:"channel_id"`
	RootID          string `json:"root_id"`
	Title           string `json:"title,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	LastUsedAt      int64  `json:"last_used_at"`
}

func (p *Plugin) getOrCreateConversationSession(
	ctx context.Context,
	cfg *runtimeConfiguration,
	bot BotDefinition,
	channel *model.Channel,
	request BotRunRequest,
	prompt string,
) (conversationSession, error) {
	conversationKey := buildConversationKey(cfg, channel, request, bot)
	if conversationKey == "" {
		return conversationSession{}, fmt.Errorf("failed to build conversation key")
	}

	existing, err := p.getConversationSession(conversationKey)
	if err != nil {
		return conversationSession{}, err
	}
	if existing.SessionID != "" && !isSessionExpired(existing, cfg.SessionIdleExpire) {
		existing.LastUsedAt = time.Now().UnixMilli()
		if saveErr := p.saveConversationSession(existing); saveErr != nil {
			p.API.LogWarn("Failed to refresh session metadata", "error", saveErr, "session_id", existing.SessionID)
		}
		return existing, nil
	}

	if existing.SessionID != "" {
		if deleteErr := p.deleteOpenCodeSession(ctx, cfg, existing.SessionID); deleteErr != nil && cfg.EnableDebugLogs {
			p.API.LogDebug("Failed to dispose expired OpenCode session", "session_id", existing.SessionID, "error", deleteErr.Error())
		}
	}

	title := buildConversationTitle(channel, request, prompt, bot)
	createdSession, err := p.createOpenCodeSession(ctx, cfg, title)
	if err != nil {
		return conversationSession{}, err
	}

	next := conversationSession{
		ConversationKey: conversationKey,
		SessionID:       createdSession.ID,
		BotID:           bot.ID,
		UserID:          request.UserID,
		ChannelID:       request.ChannelID,
		RootID:          request.RootID,
		Title:           title,
		CreatedAt:       time.Now().UnixMilli(),
		LastUsedAt:      time.Now().UnixMilli(),
	}

	if err := p.saveConversationSession(next); err != nil {
		return conversationSession{}, err
	}

	return next, nil
}

func (p *Plugin) adoptConversationSession(
	_ context.Context,
	cfg *runtimeConfiguration,
	bot BotDefinition,
	channel *model.Channel,
	request BotRunRequest,
	prompt string,
) (conversationSession, error) {
	sessionID := strings.TrimSpace(request.ReuseSessionID)
	if sessionID == "" {
		return conversationSession{}, fmt.Errorf("requested session id is empty")
	}

	existing, err := p.getConversationSessionByID(sessionID)
	if err != nil {
		return conversationSession{}, err
	}
	if existing.SessionID == "" {
		return conversationSession{}, fmt.Errorf("requested session %q is not available", sessionID)
	}
	if existing.BotID != "" && !strings.EqualFold(existing.BotID, bot.ID) {
		return conversationSession{}, fmt.Errorf("requested session belongs to a different bot")
	}
	if existing.UserID != "" && existing.UserID != request.UserID {
		return conversationSession{}, fmt.Errorf("requested session belongs to a different user")
	}

	conversationKey := buildConversationKey(cfg, channel, request, bot)
	if conversationKey == "" {
		return conversationSession{}, fmt.Errorf("failed to build conversation key")
	}

	if existing.ConversationKey != "" && existing.ConversationKey != conversationKey {
		if err := p.deleteConversationSessionMap(existing.ConversationKey); err != nil {
			return conversationSession{}, err
		}
	}

	now := time.Now().UnixMilli()
	existing.ConversationKey = conversationKey
	existing.BotID = bot.ID
	existing.UserID = request.UserID
	existing.ChannelID = request.ChannelID
	existing.RootID = request.RootID
	if existing.CreatedAt == 0 {
		existing.CreatedAt = now
	}
	existing.LastUsedAt = now
	if strings.TrimSpace(existing.Title) == "" {
		existing.Title = buildConversationTitle(channel, request, prompt, bot)
	}

	if err := p.saveConversationSession(existing); err != nil {
		return conversationSession{}, err
	}

	return existing, nil
}

func (p *Plugin) resetConversationSession(ctx context.Context, cfg *runtimeConfiguration, conversationKey string) {
	current, err := p.getConversationSession(conversationKey)
	if err == nil && current.SessionID != "" {
		_ = p.deleteOpenCodeSession(ctx, cfg, current.SessionID)
	}
	_ = p.deleteConversationSession(conversationKey, current.SessionID)
}

func buildConversationKey(cfg *runtimeConfiguration, channel *model.Channel, request BotRunRequest, bot BotDefinition) string {
	if channel != nil && channel.Type == model.ChannelTypeDirect {
		return strings.ToLower(strings.Join([]string{"dm", request.ChannelID, bot.ID}, ":"))
	}

	reuseScope := defaultSessionReuseScope
	if cfg != nil {
		reuseScope = cfg.SessionReuseScope
	}
	rootID := strings.TrimSpace(request.RootID)
	switch reuseScope {
	case "channel":
		return strings.ToLower(strings.Join([]string{"channel", request.ChannelID, bot.ID}, ":"))
	case "dm":
		return strings.ToLower(strings.Join([]string{"dm", request.ChannelID, bot.ID}, ":"))
	default:
		if rootID == "" {
			rootID = defaultIfEmpty(strings.TrimSpace(request.TriggerPostID), request.ChannelID+":"+request.UserID)
		}
		return strings.ToLower(strings.Join([]string{"thread", request.ChannelID, rootID, bot.ID}, ":"))
	}
}

func buildConversationTitle(channel *model.Channel, request BotRunRequest, prompt string, bot BotDefinition) string {
	parts := []string{}
	if channel != nil {
		switch channel.Type {
		case model.ChannelTypeDirect:
			parts = append(parts, "Direct Message")
		default:
			parts = append(parts, defaultIfEmpty(strings.TrimSpace(channel.DisplayName), channel.Name))
		}
	}
	if request.UserName != "" {
		parts = append(parts, request.UserName)
	}
	if bot.DisplayName != "" {
		parts = append(parts, bot.DisplayName)
	}
	if prompt != "" {
		parts = append(parts, truncateString(prompt, 48))
	}

	title := strings.TrimSpace(strings.Join(parts, " | "))
	if title == "" {
		title = "Mattermost conversation"
	}
	return truncateString(title, 120)
}

func isSessionExpired(item conversationSession, idleExpire time.Duration) bool {
	if item.SessionID == "" || idleExpire <= 0 {
		return false
	}
	lastUsedAt := time.UnixMilli(item.LastUsedAt)
	if item.LastUsedAt == 0 {
		lastUsedAt = time.UnixMilli(item.CreatedAt)
	}
	if lastUsedAt.IsZero() {
		return false
	}
	return time.Since(lastUsedAt) > idleExpire
}

func (p *Plugin) getConversationSession(conversationKey string) (conversationSession, error) {
	if conversationKey == "" {
		return conversationSession{}, nil
	}
	data, appErr := p.API.KVGet(sessionMapKeyPrefix + conversationKey)
	if appErr != nil {
		return conversationSession{}, fmt.Errorf("failed to load session map: %w", appErr)
	}
	if len(data) == 0 {
		return conversationSession{}, nil
	}

	var item conversationSession
	if err := json.Unmarshal(data, &item); err != nil {
		return conversationSession{}, fmt.Errorf("failed to decode session map: %w", err)
	}
	return item, nil
}

func (p *Plugin) getConversationSessionByID(sessionID string) (conversationSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return conversationSession{}, nil
	}

	data, appErr := p.API.KVGet(sessionMetaKeyPrefix + sessionID)
	if appErr != nil {
		return conversationSession{}, fmt.Errorf("failed to load session metadata: %w", appErr)
	}
	if len(data) == 0 {
		return conversationSession{}, nil
	}

	var item conversationSession
	if err := json.Unmarshal(data, &item); err != nil {
		return conversationSession{}, fmt.Errorf("failed to decode session metadata: %w", err)
	}
	return item, nil
}

func (p *Plugin) listConversationSessions(limit int) ([]conversationSession, error) {
	keys := []string{}
	for page := 0; ; page++ {
		pageKeys, appErr := p.API.KVList(page, 200)
		if appErr != nil {
			return nil, fmt.Errorf("failed to list session metadata keys: %w", appErr)
		}
		if len(pageKeys) == 0 {
			break
		}
		keys = append(keys, pageKeys...)
		if len(pageKeys) < 200 {
			break
		}
	}

	items := make([]conversationSession, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, sessionMetaKeyPrefix) {
			continue
		}
		data, appErr := p.API.KVGet(key)
		if appErr != nil || len(data) == 0 {
			continue
		}

		var item conversationSession
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}
		if item.SessionID == "" {
			continue
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].LastUsedAt > items[j].LastUsedAt
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (p *Plugin) saveConversationSession(item conversationSession) error {
	if item.ConversationKey == "" || item.SessionID == "" {
		return nil
	}
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to encode session map: %w", err)
	}
	if appErr := p.API.KVSet(sessionMapKeyPrefix+item.ConversationKey, data); appErr != nil {
		return fmt.Errorf("failed to persist session map: %w", appErr)
	}
	if metaErr := p.saveSessionMeta(item); metaErr != nil {
		return metaErr
	}
	return nil
}

func (p *Plugin) saveSessionMeta(item conversationSession) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to encode session metadata: %w", err)
	}
	if appErr := p.API.KVSet(sessionMetaKeyPrefix+item.SessionID, data); appErr != nil {
		return fmt.Errorf("failed to persist session metadata: %w", appErr)
	}
	return nil
}

func (p *Plugin) deleteConversationSession(conversationKey, sessionID string) error {
	if conversationKey != "" {
		if err := p.deleteConversationSessionMap(conversationKey); err != nil {
			return err
		}
	}
	if sessionID != "" {
		if appErr := p.API.KVDelete(sessionMetaKeyPrefix + sessionID); appErr != nil {
			return fmt.Errorf("failed to delete session metadata: %w", appErr)
		}
	}
	return nil
}

func (p *Plugin) deleteConversationSessionMap(conversationKey string) error {
	conversationKey = strings.TrimSpace(conversationKey)
	if conversationKey == "" {
		return nil
	}
	if appErr := p.API.KVDelete(sessionMapKeyPrefix + conversationKey); appErr != nil {
		return fmt.Errorf("failed to delete session map: %w", appErr)
	}
	return nil
}
