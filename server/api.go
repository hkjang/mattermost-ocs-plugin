package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type runBotAPIRequest struct {
	BotID          string         `json:"bot_id"`
	ChannelID      string         `json:"channel_id"`
	RootID         string         `json:"root_id"`
	Prompt         string         `json:"prompt"`
	IncludeContext bool           `json:"include_context"`
	Inputs         map[string]any `json:"inputs"`
	ReuseSessionID string         `json:"reuse_session_id"`
}

type pluginStatusResponse struct {
	PluginID                  string                    `json:"plugin_id"`
	BaseURL                   string                    `json:"base_url"`
	BotCount                  int                       `json:"bot_count"`
	AllowHosts                []string                  `json:"allow_hosts"`
	Bots                      []BotDefinition           `json:"bots"`
	ManagedBots               []botSyncEntry            `json:"managed_bots"`
	BotSync                   botSyncState              `json:"bot_sync"`
	StreamingEnabled          bool                      `json:"streaming_enabled"`
	StreamingUpdateIntervalMS int64                     `json:"streaming_update_interval_ms"`
	DefaultProviderID         string                    `json:"default_provider_id,omitempty"`
	DefaultModelID            string                    `json:"default_model_id,omitempty"`
	DefaultAgentID            string                    `json:"default_agent_id,omitempty"`
	SessionReuseScope         string                    `json:"session_reuse_scope,omitempty"`
	SessionIdleExpireMinutes  int64                     `json:"session_idle_expire_minutes,omitempty"`
	ConfigError               string                    `json:"config_error,omitempty"`
	Connection                *openCodeConnectionStatus `json:"connection,omitempty"`
}

type adminConfigResponse struct {
	Config storedPluginConfig `json:"config"`
	Source string             `json:"source"`
}

type sessionActionRequest struct {
	SessionID       string `json:"session_id"`
	ConversationKey string `json:"conversation_key"`
}

type sessionListItem struct {
	ConversationKey string `json:"conversation_key"`
	SessionID       string `json:"session_id"`
	BotID           string `json:"bot_id"`
	BotUsername     string `json:"bot_username,omitempty"`
	BotName         string `json:"bot_name,omitempty"`
	UserID          string `json:"user_id"`
	UserName        string `json:"user_name,omitempty"`
	ChannelID       string `json:"channel_id"`
	ChannelName     string `json:"channel_name,omitempty"`
	RootID          string `json:"root_id"`
	Title           string `json:"title,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	LastUsedAt      int64  `json:"last_used_at"`
	Expired         bool   `json:"expired"`
}

func (p *Plugin) initRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(p.MattermostAuthorizationRequired)

	apiRouter := router.PathPrefix("/api/v1").Subrouter()
	apiRouter.HandleFunc("/config", p.handleAdminConfig).Methods(http.MethodGet)
	apiRouter.HandleFunc("/status", p.handleStatus).Methods(http.MethodGet)
	apiRouter.HandleFunc("/bots", p.handleBots).Methods(http.MethodGet)
	apiRouter.HandleFunc("/history", p.handleHistory).Methods(http.MethodGet)
	apiRouter.HandleFunc("/run", p.handleRunBot).Methods(http.MethodPost)
	apiRouter.HandleFunc("/test", p.handleTestConnection).Methods(http.MethodPost)
	apiRouter.HandleFunc("/sessions", p.handleSessions).Methods(http.MethodGet)
	apiRouter.HandleFunc("/sessions/reset", p.handleResetSession).Methods(http.MethodPost)
	apiRouter.HandleFunc("/sessions/abort", p.handleAbortSession).Methods(http.MethodPost)
	apiRouter.HandleFunc("/coding/workspace", p.handleCodingWorkspace).Methods(http.MethodGet)
	apiRouter.HandleFunc("/coding/search", p.handleCodingSearch).Methods(http.MethodGet)
	apiRouter.HandleFunc("/coding/task/{task_id}", p.handleCodingTask).Methods(http.MethodGet)
	apiRouter.HandleFunc("/coding/task/command/run", p.handleCodingCommandRun).Methods(http.MethodPost)

	return router
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	p.router.ServeHTTP(w, r)
}

func (p *Plugin) MattermostAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mattermost-User-ID") == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) handleStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := p.getConfiguration()
	status := pluginStatusResponse{
		PluginID:    manifest.Id,
		BaseURL:     strings.TrimSpace(cfg.Config),
		Bots:        []BotDefinition{},
		ManagedBots: []botSyncEntry{},
		BotSync:     p.getBotSyncState(),
	}

	runtimeCfg, err := cfg.normalize()
	if err != nil {
		status.ConfigError = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}

	status.BotCount = len(runtimeCfg.BotDefinitions)
	status.AllowHosts = runtimeCfg.AllowHosts
	status.BaseURL = runtimeCfg.OpenCodeBaseURL
	status.Bots = sanitizeBotDefinitions(runtimeCfg.BotDefinitions)
	status.ManagedBots = status.BotSync.Entries
	status.StreamingEnabled = runtimeCfg.EnableStreaming
	status.StreamingUpdateIntervalMS = runtimeCfg.StreamingUpdateInterval.Milliseconds()
	status.DefaultProviderID = runtimeCfg.DefaultProviderID
	status.DefaultModelID = runtimeCfg.DefaultModelID
	status.DefaultAgentID = runtimeCfg.DefaultAgentID
	status.SessionReuseScope = runtimeCfg.SessionReuseScope
	status.SessionIdleExpireMinutes = int64(runtimeCfg.SessionIdleExpire / time.Minute)
	writeJSON(w, http.StatusOK, status)
}

func (p *Plugin) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !p.requireSystemAdmin(w, r, "only system administrators can access plugin configuration") {
		return
	}

	stored, source, err := p.getConfiguration().getStoredPluginConfig()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, adminConfigResponse{
		Config: stored,
		Source: source,
	})
}

func (p *Plugin) handleBots(w http.ResponseWriter, r *http.Request) {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"bots": sanitizeBotDefinitions(cfg.BotDefinitions)})
		return
	}

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		writeError(w, http.StatusBadRequest, appErr)
		return
	}
	team := p.getTeamForChannel(channel)
	user, appErr := p.API.GetUser(r.Header.Get("Mattermost-User-ID"))
	if appErr != nil {
		writeError(w, http.StatusBadRequest, appErr)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bots": sanitizeBotDefinitions(cfg.getAllowedBots(user, channel, team)),
	})
}

func (p *Plugin) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	history, err := p.getExecutionHistory(r.Header.Get("Mattermost-User-ID"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": history})
}

func (p *Plugin) handleRunBot(w http.ResponseWriter, r *http.Request) {
	var request runBotAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := p.executeBotAndPost(r.Context(), BotRunRequest{
		BotID:          request.BotID,
		UserID:         r.Header.Get("Mattermost-User-ID"),
		ChannelID:      request.ChannelID,
		RootID:         request.RootID,
		Prompt:         request.Prompt,
		IncludeContext: request.IncludeContext,
		Inputs:         request.Inputs,
		ReuseSessionID: request.ReuseSessionID,
		Source:         "webapp",
	})
	if err != nil {
		if result == nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (p *Plugin) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	if !p.requireSystemAdmin(w, r, "only system administrators can test OpenCode connectivity") {
		return
	}

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	status, err := p.testOpenCodeConnection(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, status)
}

func (p *Plugin) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !p.requireSystemAdmin(w, r, "only system administrators can inspect OpenCode sessions") {
		return
	}

	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sessions, err := p.listConversationSessions(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": p.decorateConversationSessions(cfg, sessions),
	})
}

func (p *Plugin) handleResetSession(w http.ResponseWriter, r *http.Request) {
	if !p.requireSystemAdmin(w, r, "only system administrators can reset OpenCode sessions") {
		return
	}

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var request sessionActionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	item, err := p.resolveRequestedSession(request)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	conversationKey := defaultIfEmpty(item.ConversationKey, strings.TrimSpace(request.ConversationKey))
	if conversationKey == "" && item.SessionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("session_id or conversation_key is required"))
		return
	}

	p.resetConversationSession(r.Context(), cfg, conversationKey)
	if conversationKey == "" {
		if err := p.deleteConversationSession("", item.SessionID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Plugin) handleAbortSession(w http.ResponseWriter, r *http.Request) {
	if !p.requireSystemAdmin(w, r, "only system administrators can abort OpenCode sessions") {
		return
	}

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var request sessionActionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		item, err := p.resolveRequestedSession(request)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		sessionID = item.SessionID
	}
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("session_id is required"))
		return
	}

	ok, err := p.abortOpenCodeSession(r.Context(), cfg, sessionID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": ok})
}

func (p *Plugin) handleCodingWorkspace(w http.ResponseWriter, r *http.Request) {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	botID := strings.TrimSpace(r.URL.Query().Get("bot_id"))
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	if botID == "" {
		writeError(w, http.StatusBadRequest, errors.New("bot_id is required"))
		return
	}
	bot, err := p.authorizeCodingBotRequest(cfg, r.Header.Get("Mattermost-User-ID"), channelID, botID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	snapshot, err := p.inspectCodingWorkspace(r.Context(), cfg, *bot)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (p *Plugin) handleCodingSearch(w http.ResponseWriter, r *http.Request) {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	botID := strings.TrimSpace(r.URL.Query().Get("bot_id"))
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 10
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsedLimit, convErr := strconv.Atoi(rawLimit); convErr == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	bot, err := p.authorizeCodingBotRequest(cfg, r.Header.Get("Mattermost-User-ID"), channelID, botID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}

	results, err := p.searchCodingWorkspace(r.Context(), cfg, *bot, query, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

func (p *Plugin) handleCodingTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(mux.Vars(r)["task_id"])
	if taskID == "" {
		writeError(w, http.StatusBadRequest, errors.New("task_id is required"))
		return
	}

	task, err := p.getCodingTask(taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if task.ID == "" {
		writeError(w, http.StatusNotFound, errors.New("task was not found"))
		return
	}
	if !p.canAccessCodingTask(r.Header.Get("Mattermost-User-ID"), task) {
		writeError(w, http.StatusForbidden, errors.New("you do not have access to this coding task"))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (p *Plugin) handleCodingCommandRun(w http.ResponseWriter, r *http.Request) {
	var request codingCommandActionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	task, err := p.getCodingTask(request.TaskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if task.ID == "" {
		writeError(w, http.StatusNotFound, errors.New("task was not found"))
		return
	}
	userID := r.Header.Get("Mattermost-User-ID")
	if !p.canAccessCodingTask(userID, task) {
		writeError(w, http.StatusForbidden, errors.New("you do not have access to this coding task"))
		return
	}

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	bot := cfg.getBotByID(task.BotID)
	if bot == nil || !bot.isCodingBot() {
		writeError(w, http.StatusBadRequest, errors.New("coding bot configuration is unavailable"))
		return
	}

	updatedTask, err := p.executeCodingCommand(r.Context(), cfg, *bot, task, request.CommandID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if updatedTask.PostID != "" {
		post, appErr := p.API.GetPost(updatedTask.PostID)
		if appErr == nil && post != nil {
			_, _ = p.attachCodingTaskToPost(post, updatedTask)
		}
	}
	writeJSON(w, http.StatusOK, updatedTask)
}

func (p *Plugin) requireSystemAdmin(w http.ResponseWriter, r *http.Request, message string) bool {
	userID := r.Header.Get("Mattermost-User-ID")
	if p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return true
	}
	writeError(w, http.StatusForbidden, errors.New(message))
	return false
}

func (p *Plugin) resolveRequestedSession(request sessionActionRequest) (conversationSession, error) {
	if sessionID := strings.TrimSpace(request.SessionID); sessionID != "" {
		item, err := p.getConversationSessionByID(sessionID)
		if err != nil {
			return conversationSession{}, err
		}
		if item.SessionID == "" {
			return conversationSession{}, errors.New("session was not found")
		}
		return item, nil
	}

	conversationKey := strings.TrimSpace(request.ConversationKey)
	if conversationKey == "" {
		return conversationSession{}, errors.New("session_id or conversation_key is required")
	}

	item, err := p.getConversationSession(conversationKey)
	if err != nil {
		return conversationSession{}, err
	}
	if item.SessionID == "" {
		return conversationSession{}, errors.New("session was not found")
	}
	return item, nil
}

func (p *Plugin) decorateConversationSessions(cfg *runtimeConfiguration, sessions []conversationSession) []sessionListItem {
	items := make([]sessionListItem, 0, len(sessions))
	userNames := map[string]string{}
	channelNames := map[string]string{}

	for _, session := range sessions {
		botName := ""
		botUsername := ""
		if cfg != nil {
			if bot := cfg.getBotByID(session.BotID); bot != nil {
				botName = bot.DisplayName
				botUsername = bot.Username
			}
		}

		userName := ""
		if session.UserID != "" {
			if cached, ok := userNames[session.UserID]; ok {
				userName = cached
			} else if user, appErr := p.API.GetUser(session.UserID); appErr == nil && user != nil {
				userName = user.Username
				userNames[session.UserID] = userName
			}
		}

		channelName := ""
		if session.ChannelID != "" {
			if cached, ok := channelNames[session.ChannelID]; ok {
				channelName = cached
			} else if channel, appErr := p.API.GetChannel(session.ChannelID); appErr == nil && channel != nil {
				channelName = defaultIfEmpty(channel.DisplayName, channel.Name)
				channelNames[session.ChannelID] = channelName
			}
		}

		items = append(items, sessionListItem{
			ConversationKey: session.ConversationKey,
			SessionID:       session.SessionID,
			BotID:           session.BotID,
			BotUsername:     botUsername,
			BotName:         botName,
			UserID:          session.UserID,
			UserName:        userName,
			ChannelID:       session.ChannelID,
			ChannelName:     channelName,
			RootID:          session.RootID,
			Title:           session.Title,
			CreatedAt:       session.CreatedAt,
			LastUsedAt:      session.LastUsedAt,
			Expired:         cfg != nil && isSessionExpired(session, cfg.SessionIdleExpire),
		})
	}

	return items
}

func (p *Plugin) canAccessCodingTask(userID string, task CodingTask) bool {
	if userID == "" {
		return false
	}
	if userID == task.UserID {
		return true
	}
	return p.client.User.HasPermissionTo(userID, model.PermissionManageSystem)
}

func (p *Plugin) authorizeCodingBotRequest(cfg *runtimeConfiguration, userID, channelID, botID string) (*BotDefinition, error) {
	bot := cfg.getBotByID(botID)
	if bot == nil {
		return nil, fmt.Errorf("unknown bot %q", botID)
	}
	if !bot.isCodingBot() {
		return nil, fmt.Errorf("bot %q is not configured for coding mode", botID)
	}
	if p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return bot, nil
	}
	if channelID == "" {
		return nil, errors.New("channel_id is required")
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to load channel: %w", appErr)
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to load user: %w", appErr)
	}
	team := p.getTeamForChannel(channel)
	if !bot.isAllowedFor(user, channel, team) {
		return nil, errors.New("this bot is not available in the selected channel")
	}
	return bot, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	writeJSON(w, statusCode, map[string]string{"error": err.Error()})
}

func sanitizeBotDefinitions(items []BotDefinition) []BotDefinition {
	sanitized := make([]BotDefinition, 0, len(items))
	for _, item := range items {
		next := item
		sanitized = append(sanitized, next)
	}
	return sanitized
}
