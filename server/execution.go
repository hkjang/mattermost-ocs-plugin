package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type BotRunRequest struct {
	BotID          string         `json:"bot_id"`
	UserID         string         `json:"user_id"`
	UserName       string         `json:"user_name"`
	ChannelID      string         `json:"channel_id"`
	RootID         string         `json:"root_id"`
	Prompt         string         `json:"prompt"`
	IncludeContext bool           `json:"include_context"`
	Inputs         map[string]any `json:"inputs"`
	ReuseSessionID string         `json:"reuse_session_id"`
	Source         string         `json:"source"`
	TriggerPostID  string         `json:"trigger_post_id"`
}

type BotRunResult struct {
	CorrelationID string `json:"correlation_id"`
	BotID         string `json:"bot_id"`
	BotUsername   string `json:"bot_username"`
	BotName       string `json:"bot_name"`
	BotMode       string `json:"bot_mode,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	PostID        string `json:"post_id,omitempty"`
	Status        string `json:"status"`
	Output        string `json:"output,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	Retryable     bool   `json:"retryable"`
}

type streamingPostUpdater struct {
	plugin        *Plugin
	post          *model.Post
	account       botAccount
	correlationID string
	sessionID     string
	agentID       string
	modelID       string
	interval      time.Duration
	lastRendered  string
	lastUpdateAt  time.Time
	started       bool
	finished      bool
}

const (
	postStreamingControlStart = "start"
	postStreamingControlEnd   = "end"
	openCodeBotPostType       = "custom_opencode_bot"
)

func (p *Plugin) executeBotAndPost(ctx context.Context, request BotRunRequest) (*BotRunResult, error) {
	startedAt := time.Now()
	correlationID := uuid.NewString()

	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		return nil, err
	}
	if request.Inputs == nil {
		request.Inputs = map[string]any{}
	}

	channel, appErr := p.API.GetChannel(request.ChannelID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to load channel: %w", appErr)
	}
	user, appErr := p.API.GetUser(request.UserID)
	if appErr != nil {
		return nil, fmt.Errorf("failed to load user: %w", appErr)
	}
	request.UserName = user.Username
	team := p.getTeamForChannel(channel)

	bot := cfg.getBotByID(request.BotID)
	if bot == nil {
		return nil, fmt.Errorf("unknown bot %q", request.BotID)
	}
	if !bot.isAllowedFor(user, channel, team) {
		return nil, fmt.Errorf("bot %q is not allowed in this context", bot.Username)
	}
	if !p.client.User.HasPermissionToChannel(request.UserID, request.ChannelID, model.PermissionReadChannel) {
		return nil, fmt.Errorf("user does not have access to the selected channel")
	}

	account, ok := p.getBotAccount(bot.ID)
	if !ok {
		if err := p.ensureBots(); err != nil {
			return nil, err
		}
		account, ok = p.getBotAccount(bot.ID)
		if !ok {
			return nil, fmt.Errorf("bot account %q is not available", bot.ID)
		}
	}

	prompt, err := p.buildExecutionPrompt(ctx, cfg, request, *bot)
	if err != nil {
		return nil, err
	}
	if prompt == "" {
		return nil, fmt.Errorf("prompt is empty")
	}

	callCtx, cancel := context.WithTimeout(ctx, cfg.DefaultTimeout)
	defer cancel()

	sessionInfo, err := p.resolveConversationSession(callCtx, cfg, *bot, channel, request, prompt)
	if err != nil {
		return nil, err
	}

	agentID := resolveAgentID(cfg, *bot)
	modelID := resolveModelID(cfg, *bot)
	messageRequest := buildOpenCodeMessageRequest(*bot, prompt, agentID, modelID, uuid.NewString())

	var streamUpdater *streamingPostUpdater
	if cfg.EnableStreaming {
		placeholder, placeholderErr := p.createStreamingPost(channel, request.RootID, account, correlationID, sessionInfo.SessionID, agentID, modelID)
		if placeholderErr != nil {
			return nil, placeholderErr
		}
		streamUpdater = &streamingPostUpdater{
			plugin:        p,
			post:          placeholder,
			account:       account,
			correlationID: correlationID,
			sessionID:     sessionInfo.SessionID,
			agentID:       agentID,
			modelID:       modelID,
			interval:      cfg.StreamingUpdateInterval,
			lastRendered:  placeholder.Message,
		}
	}

	runWithSession := func(sessionID string) (string, int, error) {
		if cfg.EnableStreaming {
			return p.invokeOpenCodeStream(callCtx, cfg, sessionID, messageRequest, streamUpdater.update)
		}
		return p.invokeOpenCode(callCtx, cfg, sessionID, messageRequest)
	}

	output, statusCode, runErr := runWithSession(sessionInfo.SessionID)
	if isMissingResourceError(runErr) {
		p.resetConversationSession(callCtx, cfg, sessionInfo.ConversationKey)
		sessionInfo, err = p.resolveConversationSession(callCtx, cfg, *bot, channel, request, prompt)
		if err == nil {
			messageRequest.MessageID = uuid.NewString()
			if streamUpdater != nil {
				streamUpdater.sessionID = sessionInfo.SessionID
			}
			output, statusCode, runErr = runWithSession(sessionInfo.SessionID)
		}
	}

	completedAt := time.Now()
	if runErr != nil {
		p.API.LogError("OpenCode execution failed", "error", runErr.Error(), "correlation_id", correlationID, "session_id", sessionInfo.SessionID)
		failure := describeExecutionFailure(runErr, statusCode >= 500 || statusCode == 0)
		record := newExecutionRecord(request, account.Definition, sessionInfo.SessionID, "", agentID, modelID, correlationID, "failed", prompt, failure.Message, failure.ErrorCode, failure.Retryable, startedAt, completedAt)
		p.appendExecutionHistory(request.UserID, record)
		p.logUsage(cfg, correlationID, request, account.Definition, sessionInfo.SessionID, agentID, modelID, "failed", failure.Message)

		var postErr error
		if streamUpdater != nil {
			postErr = streamUpdater.fail(failure)
		} else {
			postErr = p.postFailure(channel, request.RootID, account, correlationID, sessionInfo.SessionID, agentID, modelID, failure)
		}
		if postErr != nil {
			p.API.LogError("Failed to post OpenCode error response", "error", postErr, "correlation_id", correlationID)
		}
		return &BotRunResult{
			CorrelationID: correlationID,
			BotID:         account.Definition.ID,
			BotUsername:   account.Definition.Username,
			BotName:       account.Definition.DisplayName,
			BotMode:       account.Definition.Mode,
			SessionID:     sessionInfo.SessionID,
			AgentID:       agentID,
			ModelID:       modelID,
			Status:        "failed",
			ErrorMessage:  failure.Message,
			ErrorCode:     failure.ErrorCode,
			Retryable:     failure.Retryable,
		}, runErr
	}

	var codingTask CodingTask
	if account.Definition.isCodingBot() {
		var taskErr error
		metadataCtx, metadataCancel := context.WithTimeout(context.Background(), minDuration(cfg.DefaultTimeout, 10*time.Second))
		codingTask, output, taskErr = p.createCodingTask(metadataCtx, cfg, account.Definition, request, account, correlationID, sessionInfo.SessionID, output)
		metadataCancel()
		if taskErr != nil {
			return nil, taskErr
		}
	}

	var post *model.Post
	if streamUpdater != nil {
		post, err = streamUpdater.complete(output)
	} else {
		post, err = p.postSuccess(channel, request.RootID, account, correlationID, sessionInfo.SessionID, agentID, modelID, output)
	}
	if err != nil {
		failure := describeExecutionFailure(err, true)
		record := newExecutionRecord(request, account.Definition, sessionInfo.SessionID, codingTask.ID, agentID, modelID, correlationID, "failed", prompt, failure.Message, failure.ErrorCode, failure.Retryable, startedAt, time.Now())
		p.appendExecutionHistory(request.UserID, record)
		return nil, err
	}
	if account.Definition.isCodingBot() && codingTask.ID != "" {
		codingTask.PostID = post.Id
		if saveErr := p.saveCodingTask(codingTask); saveErr != nil {
			return nil, saveErr
		}
		post, err = p.attachCodingTaskToPost(post, codingTask)
		if err != nil {
			return nil, err
		}
	}

	record := newExecutionRecord(request, account.Definition, sessionInfo.SessionID, codingTask.ID, agentID, modelID, correlationID, "completed", prompt, "", "", false, startedAt, completedAt)
	p.appendExecutionHistory(request.UserID, record)
	p.logUsage(cfg, correlationID, request, account.Definition, sessionInfo.SessionID, agentID, modelID, "completed", "")

	return &BotRunResult{
		CorrelationID: correlationID,
		BotID:         account.Definition.ID,
		BotUsername:   account.Definition.Username,
		BotName:       account.Definition.DisplayName,
		BotMode:       account.Definition.Mode,
		SessionID:     sessionInfo.SessionID,
		AgentID:       agentID,
		ModelID:       modelID,
		TaskID:        codingTask.ID,
		PostID:        post.Id,
		Status:        "completed",
		Output:        output,
	}, nil
}

func (p *Plugin) resolveConversationSession(
	ctx context.Context,
	cfg *runtimeConfiguration,
	bot BotDefinition,
	channel *model.Channel,
	request BotRunRequest,
	prompt string,
) (conversationSession, error) {
	if strings.TrimSpace(request.ReuseSessionID) != "" {
		return p.adoptConversationSession(ctx, cfg, bot, channel, request, prompt)
	}
	return p.getOrCreateConversationSession(ctx, cfg, bot, channel, request, prompt)
}

func buildOpenCodeMessageRequest(bot BotDefinition, prompt, agentID, modelID, messageID string) openCodeMessageRequest {
	request := openCodeMessageRequest{
		MessageID: messageID,
		Agent:     strings.TrimSpace(agentID),
		Model:     strings.TrimSpace(modelID),
		System:    strings.TrimSpace(bot.SystemPrompt),
		Parts: []openCodePart{{
			Type: "text",
			Text: prompt,
		}},
	}
	if tools := parseToolPolicy(bot.ToolPolicy); tools != nil {
		request.Tools = tools
	}
	return request
}

func resolveAgentID(cfg *runtimeConfiguration, bot BotDefinition) string {
	if bot.DefaultAgent != "" {
		return bot.DefaultAgent
	}
	return cfg.DefaultAgentID
}

func resolveModelID(cfg *runtimeConfiguration, bot BotDefinition) string {
	modelID := strings.TrimSpace(bot.DefaultModel)
	if modelID == "" {
		modelID = strings.TrimSpace(cfg.DefaultModelID)
	}
	if modelID == "" {
		return ""
	}
	if strings.Contains(modelID, "/") || cfg.DefaultProviderID == "" {
		return modelID
	}
	return cfg.DefaultProviderID + "/" + modelID
}

func parseToolPolicy(raw string) any {
	value := strings.TrimSpace(raw)
	switch strings.ToLower(value) {
	case "", "inherit", "default":
		return nil
	case "none":
		return []string{}
	}

	if strings.HasPrefix(value, "[") {
		var tools []string
		if err := json.Unmarshal([]byte(value), &tools); err == nil {
			return tools
		}
	}

	parts := strings.Split(value, ",")
	tools := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tools = append(tools, part)
	}
	if len(tools) == 0 {
		return nil
	}
	return tools
}

func (p *Plugin) buildExecutionPrompt(ctx context.Context, cfg *runtimeConfiguration, request BotRunRequest, bot BotDefinition) (string, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return "", nil
	}
	if len(prompt) > cfg.MaxInputLength {
		return "", fmt.Errorf("prompt exceeds the maximum input length of %d characters", cfg.MaxInputLength)
	}
	if err := validateRequestedInputs(bot, request.Inputs); err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString(prompt)

	extraInputs := buildPromptInputs(bot, request.Inputs)
	if len(extraInputs) > 0 {
		builder.WriteString("\n\nAdditional inputs:\n")
		for _, line := range extraInputs {
			builder.WriteString("- ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	if request.IncludeContext {
		contextBlock, err := p.collectContextBlock(request.ChannelID, request.RootID, request.TriggerPostID, cfg.ContextPostLimit)
		if err != nil {
			return "", err
		}
		if contextBlock != "" {
			builder.WriteString("\n\nRecent Mattermost context:\n")
			builder.WriteString(contextBlock)
		}
	}

	if bot.isCodingBot() {
		codingContext := p.buildCodingPromptContext(ctx, cfg, bot, prompt)
		if codingContext != "" {
			builder.WriteString("\n\n")
			builder.WriteString(codingContext)
		}
	}

	return truncateString(builder.String(), cfg.MaxInputLength), nil
}

func buildPromptInputs(bot BotDefinition, inputs map[string]any) []string {
	lines := make([]string, 0, len(bot.InputSchema))
	for _, field := range bot.InputSchema {
		value, ok := inputs[field.Name]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", defaultIfEmpty(field.Label, field.Name), text))
	}
	return lines
}

func (p *Plugin) collectContextBlock(channelID, rootID, triggerPostID string, limit int) (string, error) {
	if limit <= 0 {
		return "", nil
	}

	var postList *model.PostList
	var appErr *model.AppError
	if rootID != "" {
		postList, appErr = p.API.GetPostThread(rootID)
	} else {
		postList, appErr = p.API.GetPostsForChannel(channelID, 0, limit)
	}
	if appErr != nil || postList == nil {
		return "", nil
	}

	posts := make([]*model.Post, 0, len(postList.Order))
	for _, postID := range postList.Order {
		post := postList.Posts[postID]
		if post == nil || post.Id == triggerPostID || p.isManagedBotUserID(post.UserId) {
			continue
		}
		if strings.TrimSpace(post.Message) == "" {
			continue
		}
		posts = append(posts, post)
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].CreateAt < posts[j].CreateAt
	})
	if len(posts) > limit {
		posts = posts[len(posts)-limit:]
	}

	lines := make([]string, 0, len(posts))
	for _, post := range posts {
		user, appErr := p.API.GetUser(post.UserId)
		username := post.UserId
		if appErr == nil && user != nil {
			username = user.Username
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", username, strings.TrimSpace(post.Message)))
	}
	return strings.Join(lines, "\n"), nil
}

func (p *Plugin) ensureBots() error {
	cfg, err := p.getRuntimeConfiguration()
	if err != nil {
		p.setBotAccounts(map[string]botAccount{})
		p.setBotSyncState(botSyncState{
			LastError: err.Error(),
			UpdatedAt: time.Now().UnixMilli(),
			Entries:   []botSyncEntry{},
		})
		return err
	}
	if len(cfg.BotDefinitions) == 0 {
		p.setBotAccounts(map[string]botAccount{})
		deactivateErr := p.deactivateManagedBots(nil)
		lastError := ""
		if deactivateErr != nil {
			lastError = deactivateErr.Error()
		}
		p.setBotSyncState(botSyncState{
			LastError: lastError,
			UpdatedAt: time.Now().UnixMilli(),
			Entries:   []botSyncEntry{},
		})
		return nil
	}

	accounts := make(map[string]botAccount, len(cfg.BotDefinitions))
	syncEntries := make([]botSyncEntry, 0, len(cfg.BotDefinitions))
	configuredUsernames := make(map[string]struct{}, len(cfg.BotDefinitions))
	syncIssues := make([]string, 0)
	for _, definition := range cfg.BotDefinitions {
		configuredUsernames[definition.Username] = struct{}{}
		userID, statusMessage, ensureErr := p.ensureSingleBot(definition)
		entry := botSyncEntry{
			BotID:         definition.ID,
			Username:      definition.Username,
			DisplayName:   definition.DisplayName,
			AgentID:       resolveAgentID(cfg, definition),
			ModelID:       resolveModelID(cfg, definition),
			UserID:        userID,
			Registered:    ensureErr == nil && userID != "",
			Active:        ensureErr == nil && userID != "",
			StatusMessage: statusMessage,
		}
		if ensureErr != nil {
			entry.StatusMessage = ensureErr.Error()
			entry.Active = false
			syncEntries = append(syncEntries, entry)
			syncIssues = append(syncIssues, ensureErr.Error())
			continue
		}
		accounts[definition.ID] = botAccount{
			Definition: definition,
			UserID:     userID,
		}
		syncEntries = append(syncEntries, entry)
	}

	if deactivateErr := p.deactivateManagedBots(configuredUsernames); deactivateErr != nil {
		syncIssues = append(syncIssues, deactivateErr.Error())
	}

	p.setBotAccounts(accounts)
	p.setBotSyncState(botSyncState{
		LastError: joinSyncIssues(syncIssues),
		UpdatedAt: time.Now().UnixMilli(),
		Entries:   syncEntries,
	})
	return nil
}

func (p *Plugin) ensureSingleBot(definition BotDefinition) (string, string, error) {
	description := botDescription(definition)
	displayName := definition.DisplayName

	existingUser, appErr := p.API.GetUserByUsername(definition.Username)
	if appErr == nil && existingUser != nil {
		if !existingUser.IsBot {
			return "", "", fmt.Errorf("username @%s is already used by a regular Mattermost account", definition.Username)
		}

		statusMessage := ""
		if _, err := p.client.Bot.Get(existingUser.Id, true); err == nil {
			if _, err := p.client.Bot.Patch(existingUser.Id, &model.BotPatch{
				DisplayName: &displayName,
				Description: &description,
			}); err != nil && !isBotNotFoundError(err) {
				return "", "", fmt.Errorf("failed to update OpenCode bot @%s: %w", definition.Username, err)
			}
			if _, err := p.client.Bot.UpdateActive(existingUser.Id, true); err != nil && !isBotNotFoundError(err) {
				return "", "", fmt.Errorf("failed to activate OpenCode bot @%s: %w", definition.Username, err)
			}
			p.API.LogInfo("Ensured OpenCode bot", "bot_username", definition.Username, "agent_id", definition.DefaultAgent, "model_id", definition.DefaultModel, "action", "linked_existing")
			return existingUser.Id, statusMessage, nil
		}

		statusMessage = fmt.Sprintf("Linked the existing Mattermost bot account for @%s.", definition.Username)
		p.API.LogWarn("Linked OpenCode bot user without bot metadata", "bot_username", definition.Username, "user_id", existingUser.Id)
		return existingUser.Id, statusMessage, nil
	}

	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		return "", "", fmt.Errorf("failed to look up Mattermost user @%s: %w", definition.Username, appErr)
	}

	newBot := &model.Bot{
		Username:    definition.Username,
		DisplayName: definition.DisplayName,
		Description: description,
	}
	if err := p.client.Bot.Create(newBot); err != nil {
		existingUser, existingErr := p.API.GetUserByUsername(definition.Username)
		if existingErr == nil && existingUser != nil && existingUser.IsBot {
			p.API.LogWarn("Recovered OpenCode bot by linking an already existing bot user", "bot_username", definition.Username, "user_id", existingUser.Id, "error", err.Error())
			return existingUser.Id, "Linked an existing Mattermost bot account.", nil
		}
		return "", "", fmt.Errorf("failed to create OpenCode bot @%s: %w", definition.Username, err)
	}

	p.API.LogInfo("Ensured OpenCode bot", "bot_username", definition.Username, "agent_id", definition.DefaultAgent, "model_id", definition.DefaultModel, "action", "created")
	return newBot.UserId, "", nil
}

func (p *Plugin) deactivateManagedBots(configuredUsernames map[string]struct{}) error {
	bots, err := p.client.Bot.List(0, 200, pluginapi.BotOwner(manifest.Id))
	if err != nil {
		return fmt.Errorf("failed to list plugin bots for deactivation: %w", err)
	}

	issues := make([]string, 0)
	for _, bot := range bots {
		if bot == nil {
			continue
		}
		if _, keep := configuredUsernames[strings.ToLower(bot.Username)]; keep {
			continue
		}
		if _, err := p.client.Bot.UpdateActive(bot.UserId, false); err != nil {
			if isBotNotFoundError(err) {
				p.API.LogWarn("Skipped deactivation for missing OpenCode bot metadata", "bot_username", bot.Username, "user_id", bot.UserId, "error", err.Error())
				continue
			}
			issues = append(issues, fmt.Sprintf("failed to deactivate removed OpenCode bot @%s: %s", bot.Username, err.Error()))
			continue
		}
		p.API.LogInfo("Deactivated removed OpenCode bot", "bot_username", bot.Username, "user_id", bot.UserId)
	}

	if len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}

func (p *Plugin) ensureBotInChannel(channelID, botUserID string) error {
	if channelID == "" || botUserID == "" {
		return nil
	}
	if _, appErr := p.API.GetChannelMember(channelID, botUserID); appErr == nil {
		return nil
	}
	if _, appErr := p.API.AddUserToChannel(channelID, botUserID, ""); appErr != nil {
		return fmt.Errorf("failed to add bot to channel: %w", appErr)
	}
	return nil
}

func (p *Plugin) postSuccess(channel *model.Channel, rootID string, account botAccount, correlationID, sessionID, agentID, modelID, output string) (*model.Post, error) {
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return nil, err
	}

	post, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      openCodeBotPostType,
		Message:   buildBotResponseMessage(output, correlationID, false),
		Props: map[string]any{
			"from_bot":                    "true",
			"opencode_bot_id":             account.Definition.ID,
			"opencode_bot_mode":           account.Definition.Mode,
			"opencode_correlation_id":     correlationID,
			"opencode_session_id":         sessionID,
			"opencode_agent_id":           agentID,
			"opencode_model_id":           modelID,
			"opencode_stream":             "false",
			"opencode_stream_placeholder": "false",
		},
	})
	if appErr != nil {
		return nil, fmt.Errorf("failed to create OpenCode response post: %w", appErr)
	}
	return post, nil
}

func (p *Plugin) postFailure(channel *model.Channel, rootID string, account botAccount, correlationID, sessionID, agentID, modelID string, failure executionFailureView) error {
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return err
	}

	_, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      openCodeBotPostType,
		Message:   buildBotFailureMessage(correlationID, failure),
		Props: map[string]any{
			"from_bot":                    "true",
			"opencode_bot_id":             account.Definition.ID,
			"opencode_bot_mode":           account.Definition.Mode,
			"opencode_correlation_id":     correlationID,
			"opencode_session_id":         sessionID,
			"opencode_agent_id":           agentID,
			"opencode_model_id":           modelID,
			"opencode_error":              "true",
			"opencode_stream":             "false",
			"opencode_stream_placeholder": "false",
			"opencode_error_code":         failure.ErrorCode,
		},
	})
	if appErr != nil {
		return fmt.Errorf("failed to create OpenCode error post: %w", appErr)
	}
	return nil
}

func (p *Plugin) postInstruction(channel *model.Channel, rootID string, account botAccount, message string) error {
	if channel == nil || strings.TrimSpace(message) == "" {
		return nil
	}
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return err
	}

	_, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      openCodeBotPostType,
		Message:   strings.TrimSpace(message),
		Props: map[string]any{
			"from_bot":                    "true",
			"opencode_bot_id":             account.Definition.ID,
			"opencode_bot_mode":           account.Definition.Mode,
			"opencode_stream_placeholder": "false",
		},
	})
	if appErr != nil {
		return fmt.Errorf("failed to create OpenCode instruction post: %w", appErr)
	}
	return nil
}

func responseRootID(post *model.Post) string {
	if post == nil {
		return ""
	}
	if post.RootId != "" {
		return post.RootId
	}
	return post.Id
}

func (p *Plugin) logUsage(cfg *runtimeConfiguration, correlationID string, request BotRunRequest, bot BotDefinition, sessionID, agentID, modelID, status, errorMessage string) {
	if !cfg.EnableUsageLogs {
		return
	}
	p.API.LogInfo("OpenCode execution", "correlation_id", correlationID, "bot_id", bot.ID, "bot_username", bot.Username, "session_id", sessionID, "agent_id", agentID, "model_id", modelID, "user_id", request.UserID, "channel_id", request.ChannelID, "source", request.Source, "status", status, "error", errorMessage)
}

func validateRequestedInputs(bot BotDefinition, inputs map[string]any) error {
	for _, field := range bot.InputSchema {
		value, ok := inputs[field.Name]
		if !ok {
			if field.Required {
				return fmt.Errorf("missing required input %q", field.Name)
			}
			continue
		}
		switch field.Type {
		case "number":
			text := strings.TrimSpace(fmt.Sprint(value))
			if text == "" && field.Required {
				return fmt.Errorf("missing required input %q", field.Name)
			}
		default:
			if strings.TrimSpace(fmt.Sprint(value)) == "" && field.Required {
				return fmt.Errorf("missing required input %q", field.Name)
			}
		}
	}
	return nil
}

func botDescription(bot BotDefinition) string {
	description := strings.TrimSpace(bot.Description)
	if description != "" {
		return description
	}

	targets := []string{}
	if bot.DefaultAgent != "" {
		targets = append(targets, "agent "+bot.DefaultAgent)
	}
	if bot.DefaultModel != "" {
		targets = append(targets, "model "+bot.DefaultModel)
	}
	if len(targets) == 0 {
		return "OpenCode bot"
	}
	return "OpenCode bot for " + strings.Join(targets, " and ")
}

func (p *Plugin) createStreamingPost(channel *model.Channel, rootID string, account botAccount, correlationID, sessionID, agentID, modelID string) (*model.Post, error) {
	if err := p.ensureBotInChannel(channel.Id, account.UserID); err != nil {
		return nil, err
	}

	post, appErr := p.API.CreatePost(&model.Post{
		UserId:    account.UserID,
		ChannelId: channel.Id,
		RootId:    rootID,
		Type:      openCodeBotPostType,
		Message:   buildBotStreamingMessage(""),
		Props: map[string]any{
			"from_bot":                    "true",
			"opencode_bot_id":             account.Definition.ID,
			"opencode_bot_mode":           account.Definition.Mode,
			"opencode_correlation_id":     correlationID,
			"opencode_session_id":         sessionID,
			"opencode_agent_id":           agentID,
			"opencode_model_id":           modelID,
			"opencode_stream":             "true",
			"opencode_streaming":          "true",
			"opencode_stream_status":      "streaming",
			"opencode_stream_placeholder": "true",
		},
	})
	if appErr != nil {
		return nil, fmt.Errorf("failed to create OpenCode streaming post: %w", appErr)
	}
	return post, nil
}

func (u *streamingPostUpdater) update(content string, final bool) {
	if u == nil || u.post == nil {
		return
	}
	u.start()
	if !final && u.interval > 0 && !u.lastUpdateAt.IsZero() && time.Since(u.lastUpdateAt) < u.interval {
		return
	}
	if _, err := u.render(content, final, executionFailureView{}); err != nil {
		u.plugin.API.LogError("Failed to update OpenCode streaming post", "error", err, "correlation_id", u.correlationID)
	}
}

func (u *streamingPostUpdater) complete(content string) (*model.Post, error) {
	return u.render(content, true, executionFailureView{})
}

func (u *streamingPostUpdater) fail(failure executionFailureView) error {
	_, err := u.render("", false, failure)
	return err
}

func (u *streamingPostUpdater) render(content string, completed bool, failure executionFailureView) (*model.Post, error) {
	if u == nil || u.post == nil {
		return nil, fmt.Errorf("streaming post is not initialized")
	}
	u.start()

	previewMessage := buildBotStreamingMessage(content)
	message := buildBotResponseMessage(content, u.correlationID, !failure.HasFailure && !completed)
	if failure.HasFailure {
		message = buildBotFailureMessage(u.correlationID, failure)
	}
	if !completed && !failure.HasFailure {
		message = previewMessage
	}
	if message == u.lastRendered {
		return u.post, nil
	}

	if !completed && !failure.HasFailure {
		u.post.Message = message
		u.sendUpdateEvent(message)
		u.lastRendered = message
		u.lastUpdateAt = time.Now()
		return u.post, nil
	}
	defer u.finish()

	updatedPost := *u.post
	updatedPost.Message = message
	updatedPost.Props = clonePostProps(u.post.Props)
	if updatedPost.Props == nil {
		updatedPost.Props = map[string]any{}
	}
	if failure.HasFailure {
		updatedPost.Props["opencode_error"] = "true"
		updatedPost.Props["opencode_error_code"] = failure.ErrorCode
		updatedPost.Props["opencode_stream_status"] = "failed"
		updatedPost.Props["opencode_streaming"] = "false"
		updatedPost.Props["opencode_stream_placeholder"] = "false"
	} else if completed {
		updatedPost.Props["opencode_stream_status"] = "completed"
		updatedPost.Props["opencode_streaming"] = "false"
		updatedPost.Props["opencode_stream_placeholder"] = "false"
	} else {
		updatedPost.Props["opencode_stream_status"] = "streaming"
		updatedPost.Props["opencode_streaming"] = "true"
		updatedPost.Props["opencode_stream_placeholder"] = "false"
	}

	post, appErr := u.plugin.API.UpdatePost(&updatedPost)
	if appErr != nil {
		return nil, fmt.Errorf("failed to update OpenCode streaming post: %w", appErr)
	}

	if failure.HasFailure {
		u.sendUpdateEvent(message)
	}
	u.post = post
	u.lastRendered = message
	u.lastUpdateAt = time.Now()
	return post, nil
}

func (u *streamingPostUpdater) start() {
	if u == nil || u.post == nil || u.started {
		return
	}
	u.started = true
	u.publishControlEvent(postStreamingControlStart)
}

func (u *streamingPostUpdater) finish() {
	if u == nil || u.post == nil || u.finished {
		return
	}
	u.finished = true
	u.publishControlEvent(postStreamingControlEnd)
}

func (u *streamingPostUpdater) sendUpdateEvent(message string) {
	if u == nil || u.post == nil {
		return
	}
	u.plugin.API.PublishWebSocketEvent("postupdate", map[string]any{
		"post_id": u.post.Id,
		"next":    message,
	}, &model.WebsocketBroadcast{ChannelId: u.post.ChannelId})
}

func (u *streamingPostUpdater) publishControlEvent(control string) {
	if u == nil || u.post == nil || strings.TrimSpace(control) == "" {
		return
	}
	u.plugin.API.PublishWebSocketEvent("postupdate", map[string]any{
		"post_id": u.post.Id,
		"control": control,
	}, &model.WebsocketBroadcast{ChannelId: u.post.ChannelId})
}

func buildBotStreamingMessage(output string) string {
	body := stripLeadingOpenCodeLabel(strings.TrimSpace(output))
	if body == "" {
		body = "_Generating a response..._"
	}
	return body
}

func buildBotResponseMessage(output, correlationID string, streaming bool) string {
	body := stripLeadingOpenCodeLabel(strings.TrimSpace(output))
	if body == "" && streaming {
		body = "_Generating a response..._"
	}
	if body == "" {
		body = "_No response content was returned._"
	}

	parts := []string{body, "", fmt.Sprintf("_Correlation ID:_ `%s`", correlationID)}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

type executionFailureView struct {
	HasFailure bool
	Message    string
	ErrorCode  string
	Retryable  bool
}

func describeExecutionFailure(err error, defaultRetryable bool) executionFailureView {
	if err == nil {
		return executionFailureView{}
	}

	var callErr *serviceCallError
	if errors.As(err, &callErr) {
		return executionFailureView{
			HasFailure: true,
			Message:    callErr.Summary,
			ErrorCode:  callErr.Code,
			Retryable:  callErr.Retryable,
		}
	}

	return executionFailureView{
		HasFailure: true,
		Message:    "The OpenCode request could not be completed.",
		Retryable:  defaultRetryable,
	}
}

func buildBotFailureMessage(correlationID string, failure executionFailureView) string {
	lines := []string{"OpenCode could not complete this request."}
	if failure.ErrorCode != "" {
		lines = append(lines, "", fmt.Sprintf("Code: `%s`", failure.ErrorCode))
	}
	if failure.Retryable {
		lines = append(lines, "", "Please try again in a moment.")
	}
	lines = append(lines, "", fmt.Sprintf("_Correlation ID:_ `%s`", correlationID))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func clonePostProps(source model.StringInterface) model.StringInterface {
	if source == nil {
		return nil
	}
	props := make(model.StringInterface, len(source))
	for key, value := range source {
		props[key] = value
	}
	return props
}

func isBotNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "resource bot not found") ||
		strings.Contains(lower, "bot does not exist") ||
		strings.Contains(lower, "unable to get bot")
}

func joinSyncIssues(issues []string) string {
	filtered := make([]string, 0, len(issues))
	for _, issue := range issues {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			continue
		}
		filtered = append(filtered, issue)
	}
	return strings.Join(filtered, " | ")
}

func stripLeadingOpenCodeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	lines := strings.Split(value, "\n")
	for len(lines) > 0 {
		normalized := strings.ToLower(strings.TrimSpace(lines[0]))
		for strings.HasPrefix(normalized, "#") {
			normalized = strings.TrimPrefix(normalized, "#")
		}
		normalized = strings.TrimSpace(strings.Trim(normalized, "*`:_-"))
		if normalized != "opencode" {
			break
		}
		lines = lines[1:]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isMissingResourceError(err error) bool {
	var callErr *serviceCallError
	if !errors.As(err, &callErr) {
		return false
	}
	return callErr.Code == "not_found"
}
