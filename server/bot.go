package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	botModeConversation         = "conversation"
	botModeCoding               = "coding"
	defaultCodingProfile        = "implementer"
	defaultCodingMaxFiles       = 4
	defaultCodingCommandTimeout = 180
)

type BotDefinition struct {
	ID                      string            `json:"id"`
	Username                string            `json:"username"`
	DisplayName             string            `json:"display_name"`
	Description             string            `json:"description"`
	Mode                    string            `json:"mode"`
	BaseURL                 string            `json:"base_url"`
	BasicAuthUsername       string            `json:"basic_auth_username"`
	BasicAuthPassword       string            `json:"basic_auth_password"`
	DefaultAgent            string            `json:"default_agent"`
	DefaultModel            string            `json:"default_model"`
	SystemPrompt            string            `json:"system_prompt"`
	ToolPolicy              string            `json:"tool_policy"`
	IncludeContextByDefault bool              `json:"include_context_by_default"`
	AllowedTeams            []string          `json:"allowed_teams"`
	AllowedChannels         []string          `json:"allowed_channels"`
	AllowedUsers            []string          `json:"allowed_users"`
	InputSchema             []BotInputField   `json:"input_schema"`
	Coding                  CodingBotSettings `json:"coding"`
}

type CodingBotSettings struct {
	Profile                  string   `json:"profile"`
	WorkspaceRoot            string   `json:"workspace_root"`
	WorkspaceLabel           string   `json:"workspace_label"`
	DefaultBranch            string   `json:"default_branch"`
	AllowedPaths             []string `json:"allowed_paths"`
	CommandAllowlist         []string `json:"command_allowlist"`
	RequireCommandApproval   bool     `json:"require_command_approval"`
	IncludeWorkspaceSnapshot bool     `json:"include_workspace_snapshot"`
	IncludeReferencedFiles   bool     `json:"include_referenced_files"`
	MaxReferencedFiles       int      `json:"max_referenced_files"`
	CommandTimeoutSeconds    int      `json:"command_timeout_seconds"`
}

type BotInputField struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	Required     bool   `json:"required"`
	Placeholder  string `json:"placeholder"`
	DefaultValue any    `json:"default_value"`
}

func (b BotDefinition) normalize() (BotDefinition, error) {
	b.ID = strings.TrimSpace(b.ID)
	b.Username = strings.ToLower(strings.TrimSpace(b.Username))
	b.DisplayName = strings.TrimSpace(b.DisplayName)
	b.Description = strings.TrimSpace(b.Description)
	b.Mode = normalizeBotMode(b.Mode)
	b.BaseURL = strings.TrimRight(strings.TrimSpace(b.BaseURL), "/")
	b.BasicAuthUsername = strings.TrimSpace(b.BasicAuthUsername)
	b.BasicAuthPassword = strings.TrimSpace(b.BasicAuthPassword)
	b.DefaultAgent = strings.TrimSpace(b.DefaultAgent)
	b.DefaultModel = strings.TrimSpace(b.DefaultModel)
	b.SystemPrompt = strings.TrimSpace(b.SystemPrompt)
	b.ToolPolicy = strings.TrimSpace(b.ToolPolicy)

	if b.Username == "" {
		return BotDefinition{}, fmt.Errorf("bot definition is missing username")
	}
	if b.ID == "" {
		b.ID = b.Username
	}
	if b.DisplayName == "" {
		b.DisplayName = b.Username
	}

	b.AllowedTeams = normalizeStringSlice(b.AllowedTeams)
	b.AllowedChannels = normalizeStringSlice(b.AllowedChannels)
	b.AllowedUsers = normalizeStringSlice(b.AllowedUsers)

	inputs := make([]BotInputField, 0, len(b.InputSchema))
	seen := map[string]struct{}{}
	for _, field := range b.InputSchema {
		field.Name = strings.TrimSpace(field.Name)
		field.Label = defaultIfEmpty(strings.TrimSpace(field.Label), field.Name)
		field.Description = strings.TrimSpace(field.Description)
		field.Placeholder = strings.TrimSpace(field.Placeholder)
		field.Type = defaultIfEmpty(strings.ToLower(strings.TrimSpace(field.Type)), "text")
		if field.Name == "" {
			return BotDefinition{}, fmt.Errorf("bot %q has an input field without a name", b.Username)
		}
		if _, ok := seen[field.Name]; ok {
			return BotDefinition{}, fmt.Errorf("bot %q defines duplicate input %q", b.Username, field.Name)
		}
		seen[field.Name] = struct{}{}
		inputs = append(inputs, field)
	}
	b.InputSchema = inputs
	b.Coding = b.Coding.normalize()

	return b, nil
}

func normalizeStringSlice(items []string) []string {
	normalized := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func normalizeBotMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case botModeCoding:
		return botModeCoding
	default:
		return botModeConversation
	}
}

func (c CodingBotSettings) normalize() CodingBotSettings {
	c.Profile = defaultIfEmpty(strings.ToLower(strings.TrimSpace(c.Profile)), defaultCodingProfile)
	c.WorkspaceRoot = strings.TrimSpace(c.WorkspaceRoot)
	if c.WorkspaceRoot != "" {
		c.WorkspaceRoot = filepath.Clean(c.WorkspaceRoot)
	}
	c.WorkspaceLabel = strings.TrimSpace(c.WorkspaceLabel)
	c.DefaultBranch = strings.TrimSpace(c.DefaultBranch)
	c.AllowedPaths = normalizePathSlice(c.AllowedPaths)
	c.CommandAllowlist = normalizeCommandPrefixes(c.CommandAllowlist)
	c.MaxReferencedFiles = positiveOrDefault(c.MaxReferencedFiles, defaultCodingMaxFiles)
	c.CommandTimeoutSeconds = positiveOrDefault(c.CommandTimeoutSeconds, defaultCodingCommandTimeout)
	if len(c.CommandAllowlist) == 0 {
		c.CommandAllowlist = []string{"git status", "git diff", "git diff --stat", "git log", "go test", "go build", "npm test", "npm run build", "pnpm test", "pnpm build", "yarn test", "yarn build", "pytest", "cargo test"}
	}
	if c.Profile == "" {
		c.Profile = defaultCodingProfile
	}
	return c
}

func normalizePathSlice(items []string) []string {
	normalized := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		item = filepath.Clean(item)
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func normalizeCommandPrefixes(items []string) []string {
	normalized := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func (b BotDefinition) isCodingBot() bool {
	return b.Mode == botModeCoding
}

func (cfg *runtimeConfiguration) getBotByID(botID string) *BotDefinition {
	botID = strings.ToLower(strings.TrimSpace(botID))
	for _, bot := range cfg.BotDefinitions {
		if strings.ToLower(bot.ID) == botID || strings.ToLower(bot.Username) == botID {
			item := bot
			return &item
		}
	}
	return nil
}

func (cfg *runtimeConfiguration) getBotByUsername(username string) *BotDefinition {
	username = strings.ToLower(strings.TrimSpace(username))
	for _, bot := range cfg.BotDefinitions {
		if bot.Username == username {
			item := bot
			return &item
		}
	}
	return nil
}

func (cfg *runtimeConfiguration) getAllowedBots(user *model.User, channel *model.Channel, team *model.Team) []BotDefinition {
	allowed := make([]BotDefinition, 0, len(cfg.BotDefinitions))
	for _, bot := range cfg.BotDefinitions {
		if bot.isAllowedFor(user, channel, team) {
			allowed = append(allowed, bot)
		}
	}
	return allowed
}

func (b BotDefinition) isAllowedFor(user *model.User, channel *model.Channel, team *model.Team) bool {
	if user == nil || channel == nil {
		return false
	}

	if len(b.AllowedUsers) > 0 && !matchesAccessEntry(b.AllowedUsers, user.Id, user.Username) {
		return false
	}

	if len(b.AllowedChannels) > 0 && !matchesAccessEntry(b.AllowedChannels, channel.Id, channel.Name) {
		return false
	}

	teamName := ""
	if team != nil {
		teamName = team.Name
	}
	if len(b.AllowedTeams) > 0 && !matchesAccessEntry(b.AllowedTeams, channel.TeamId, teamName) {
		return false
	}

	return true
}

func matchesAccessEntry(entries []string, values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		for _, entry := range entries {
			if entry == value {
				return true
			}
		}
	}
	return false
}

func botUsageExamples(bot BotDefinition) []string {
	if bot.isCodingBot() {
		return []string{
			fmt.Sprintf("- `@%s Investigate why the tests are failing in server/api.go`", bot.Username),
			fmt.Sprintf("- `@%s Review the current diff and suggest the next patch`", bot.Username),
		}
	}

	return []string{
		fmt.Sprintf("- `@%s Summarize this thread`", bot.Username),
		fmt.Sprintf("- DM `%s` with `What should I do next?`", bot.Username),
	}
}
