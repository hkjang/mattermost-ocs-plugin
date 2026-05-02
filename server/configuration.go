package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"
)

const (
	defaultTimeoutSeconds       = 30
	defaultMaxInputLength       = 4000
	defaultMaxOutputLength      = 8000
	defaultContextPostLimit     = 8
	defaultStreamIntervalMS     = 350
	defaultIdleExpireMinutes    = 120
	defaultSessionReuseScope    = "thread"
	maxHistoryEntriesPerUser    = 20
)

type configuration struct {
	Config string `json:"Config"`
}

type storedPluginConfig struct {
	SchemaVersion    int                          `json:"schema_version"`
	Service          storedServiceConfig          `json:"service"`
	Runtime          storedRuntimeConfig          `json:"runtime"`
	OpenCodeDefaults storedOpenCodeDefaults       `json:"opencode_defaults"`
	SessionPolicy    storedSessionPolicy          `json:"session_policy"`
	Bots             []BotDefinition              `json:"bots"`
}

const (
	currentConfigSchemaVersion = 2
	legacyConfigBackupKVKey    = "legacy_config_backup_v1"
	legacyConfigResetKVKey     = "legacy_config_reset_v2_done"
)

type storedServiceConfig struct {
	BaseURL    string `json:"base_url"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	AllowHosts string `json:"allow_hosts"`
}

type storedRuntimeConfig struct {
	DefaultTimeoutSeconds int  `json:"default_timeout_seconds"`
	EnableStreaming       bool `json:"enable_streaming"`
	StreamingUpdateMS     int  `json:"streaming_update_ms"`
	MaxInputLength        int  `json:"max_input_length"`
	MaxOutputLength       int  `json:"max_output_length"`
	ContextPostLimit      int  `json:"context_post_limit"`
	EnableDebugLogs       bool `json:"enable_debug_logs"`
	EnableUsageLogs       bool `json:"enable_usage_logs"`
}

type storedOpenCodeDefaults struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	AgentID    string `json:"agent_id"`
}

type storedSessionPolicy struct {
	ReuseScope        string `json:"reuse_scope"`
	IdleExpireMinutes int    `json:"idle_expire_minutes"`
}

type runtimeConfiguration struct {
	OpenCodeBaseURL         string
	ParsedBaseURL           *url.URL
	OpenCodeUsername        string
	OpenCodePassword        string
	AllowHosts              []string
	BotDefinitions          []BotDefinition
	DefaultTimeout          time.Duration
	StreamingUpdateInterval time.Duration
	MaxInputLength          int
	MaxOutputLength         int
	ContextPostLimit        int
	EnableStreaming         bool
	EnableDebugLogs         bool
	EnableUsageLogs         bool
	DefaultProviderID       string
	DefaultModelID          string
	DefaultAgentID          string
	SessionReuseScope       string
	SessionIdleExpire       time.Duration
}

// deriveForBot returns a configuration scoped to a particular bot. If the bot
// supplies its own base URL or basic auth credentials, those override the
// service-level defaults so each bot can target a different OpenCode server.
func (c *runtimeConfiguration) deriveForBot(bot BotDefinition) *runtimeConfiguration {
	if c == nil {
		return nil
	}

	derived := *c
	if raw := strings.TrimSpace(bot.BaseURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			derived.OpenCodeBaseURL = strings.TrimRight(parsed.String(), "/")
			derived.ParsedBaseURL = parsed

			host := strings.ToLower(parsed.Hostname())
			if host != "" {
				existing := append([]string{}, derived.AllowHosts...)
				seen := false
				for _, h := range existing {
					if h == host {
						seen = true
						break
					}
				}
				if !seen {
					derived.AllowHosts = append(existing, host)
				}
			}
		}
	}
	if v := strings.TrimSpace(bot.BasicAuthUsername); v != "" {
		derived.OpenCodeUsername = v
	}
	if v := strings.TrimSpace(bot.BasicAuthPassword); v != "" {
		derived.OpenCodePassword = v
	}
	return &derived
}

func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

func (c *configuration) normalize() (*runtimeConfiguration, error) {
	stored, _, err := c.getStoredPluginConfig()
	if err != nil {
		return nil, err
	}
	return stored.normalize()
}

func (c *configuration) getStoredPluginConfig() (storedPluginConfig, string, error) {
	stored, err := parseStoredPluginConfig(c.Config)
	if err != nil {
		return storedPluginConfig{}, "config", err
	}
	return stored, "config", nil
}

func parseStoredPluginConfig(raw string) (storedPluginConfig, error) {
	cfg := defaultStoredPluginConfig()
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return storedPluginConfig{}, fmt.Errorf("invalid Config JSON: %w", err)
	}
	return cfg, nil
}

func defaultStoredPluginConfig() storedPluginConfig {
	return storedPluginConfig{
		SchemaVersion: currentConfigSchemaVersion,
		Runtime: storedRuntimeConfig{
			DefaultTimeoutSeconds: defaultTimeoutSeconds,
			EnableStreaming:       true,
			StreamingUpdateMS:     defaultStreamIntervalMS,
			MaxInputLength:        defaultMaxInputLength,
			MaxOutputLength:       defaultMaxOutputLength,
			ContextPostLimit:      defaultContextPostLimit,
			EnableUsageLogs:       true,
		},
		SessionPolicy: storedSessionPolicy{
			ReuseScope:        defaultSessionReuseScope,
			IdleExpireMinutes: defaultIdleExpireMinutes,
		},
		Bots: []BotDefinition{},
	}
}

func (c storedPluginConfig) normalize() (*runtimeConfiguration, error) {
	cfg := &runtimeConfiguration{
		OpenCodeBaseURL:   strings.TrimSpace(c.Service.BaseURL),
		OpenCodeUsername:  strings.TrimSpace(c.Service.Username),
		OpenCodePassword:  strings.TrimSpace(c.Service.Password),
		EnableStreaming:   c.Runtime.EnableStreaming,
		MaxInputLength:    positiveOrDefault(c.Runtime.MaxInputLength, defaultMaxInputLength),
		MaxOutputLength:   positiveOrDefault(c.Runtime.MaxOutputLength, defaultMaxOutputLength),
		ContextPostLimit:  positiveOrDefault(c.Runtime.ContextPostLimit, defaultContextPostLimit),
		EnableDebugLogs:   c.Runtime.EnableDebugLogs,
		EnableUsageLogs:   c.Runtime.EnableUsageLogs,
		DefaultProviderID: "", // intentionally ignored due to stale config issue
		DefaultModelID:    "",
		DefaultAgentID:    "",
		SessionReuseScope: normalizeSessionReuseScope(c.SessionPolicy.ReuseScope),
	}
	cfg.DefaultTimeout = time.Duration(positiveOrDefault(c.Runtime.DefaultTimeoutSeconds, defaultTimeoutSeconds)) * time.Second
	cfg.StreamingUpdateInterval = time.Duration(positiveOrDefault(c.Runtime.StreamingUpdateMS, defaultStreamIntervalMS)) * time.Millisecond
	cfg.SessionIdleExpire = time.Duration(positiveOrDefault(c.SessionPolicy.IdleExpireMinutes, defaultIdleExpireMinutes)) * time.Minute

	if cfg.OpenCodeBaseURL != "" {
		parsedURL, err := url.Parse(cfg.OpenCodeBaseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenCode base URL: %w", err)
		}
		if parsedURL.Scheme == "" || parsedURL.Host == "" {
			return nil, fmt.Errorf("OpenCode base URL must include scheme and host")
		}
		cfg.OpenCodeBaseURL = strings.TrimRight(parsedURL.String(), "/")
		cfg.ParsedBaseURL = parsedURL
	}

	serializedBots, err := json.Marshal(c.Bots)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize bot definitions: %w", err)
	}

	bots, err := parseBotDefinitions(string(serializedBots))
	if err != nil {
		return nil, err
	}
	cfg.BotDefinitions = bots
	cfg.AllowHosts = normalizeAllowHosts(c.Service.AllowHosts, cfg.ParsedBaseURL)

	return cfg, nil
}

func normalizeAllowHosts(raw string, parsedBaseURL *url.URL) []string {
	parts := strings.Split(raw, ",")
	hosts := make([]string, 0, len(parts)+1)
	seen := map[string]struct{}{}

	appendHost := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}

	for _, part := range parts {
		appendHost(part)
	}

	if len(hosts) == 0 && parsedBaseURL != nil {
		appendHost(parsedBaseURL.Hostname())
	}

	return hosts
}

func normalizeSessionReuseScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "thread", "dm", "channel":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return defaultSessionReuseScope
	}
}

func parsePositiveInt(raw string, fallback int) int {
	var value int
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &value); err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseBotDefinitions(raw string) ([]BotDefinition, error) {
	if strings.TrimSpace(raw) == "" {
		return []BotDefinition{}, nil
	}

	var bots []BotDefinition
	if err := json.Unmarshal([]byte(raw), &bots); err != nil {
		return nil, fmt.Errorf("invalid bot definitions JSON: %w", err)
	}

	normalized := make([]BotDefinition, 0, len(bots))
	seenIDs := map[string]struct{}{}
	seenUsernames := map[string]struct{}{}
	for _, bot := range bots {
		item, err := bot.normalize()
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[item.ID]; ok {
			return nil, fmt.Errorf("duplicate bot id %q", item.ID)
		}
		if _, ok := seenUsernames[item.Username]; ok {
			return nil, fmt.Errorf("duplicate bot username %q", item.Username)
		}
		seenIDs[item.ID] = struct{}{}
		seenUsernames[item.Username] = struct{}{}
		normalized = append(normalized, item)
	}

	return normalized, nil
}

func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

func (p *Plugin) getRuntimeConfiguration() (*runtimeConfiguration, error) {
	return p.getConfiguration().normalize()
}

func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}
		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

func (p *Plugin) OnConfigurationChange() error {
	configuration := new(configuration)
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return fmt.Errorf("failed to load plugin configuration: %w", err)
	}

	if p.client != nil {
		if cleared, err := p.resetLegacyConfigOnce(configuration); err != nil {
			p.API.LogWarn("Failed to clear legacy OpenCode plugin config", "error", err.Error())
		} else if cleared {
			// Saving the cleared config will re-trigger this hook with the
			// fresh values; let that pass handle the rest.
			return nil
		}
	}

	p.setConfiguration(configuration)

	if p.client != nil {
		if err := p.ensureBots(); err != nil {
			p.API.LogError("Failed to ensure OpenCode bots after configuration change", "error", err)
		}
	}

	return nil
}

// resetLegacyConfigOnce wipes the stored Config blob the first time the v2
// schema runs, so admins start from a clean slate after the field/UI overhaul.
// The previous Config is backed up to KV so it can be recovered if needed.
func (p *Plugin) resetLegacyConfigOnce(cfg *configuration) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	marker, _ := p.API.KVGet(legacyConfigResetKVKey)
	if len(marker) > 0 {
		return false, nil
	}

	raw := strings.TrimSpace(cfg.Config)
	if raw == "" {
		// Nothing to migrate — just record the marker so we never run again.
		_ = p.API.KVSet(legacyConfigResetKVKey, []byte("1"))
		return false, nil
	}

	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err == nil {
		if version, ok := probe["schema_version"].(float64); ok && int(version) >= currentConfigSchemaVersion {
			_ = p.API.KVSet(legacyConfigResetKVKey, []byte("1"))
			return false, nil
		}
	}

	if appErr := p.API.KVSet(legacyConfigBackupKVKey, []byte(cfg.Config)); appErr != nil {
		return false, fmt.Errorf("failed to back up legacy config: %w", appErr)
	}

	if appErr := p.API.SavePluginConfig(map[string]any{"Config": ""}); appErr != nil {
		return false, fmt.Errorf("failed to clear legacy config: %w", appErr)
	}

	if appErr := p.API.KVSet(legacyConfigResetKVKey, []byte("1")); appErr != nil {
		return false, fmt.Errorf("failed to record legacy config reset: %w", appErr)
	}

	if cleared, err := p.purgeStoredSessionKeys(); err != nil {
		p.API.LogWarn("Failed to purge stale OpenCode session metadata", "error", err.Error())
	} else if cleared > 0 {
		p.API.LogInfo("Purged stale OpenCode session metadata after legacy reset", "removed_keys", cleared)
	}

	cfg.Config = ""
	p.API.LogInfo("Cleared legacy OpenCode plugin configuration; previous values backed up to KV.", "kv_key", legacyConfigBackupKVKey)
	return true, nil
}

// purgeStoredSessionKeys deletes every cached session map/meta KV entry so the
// plugin no longer reuses session IDs from a previous install or OpenCode
// instance. It pages through KVList to handle large key sets.
func (p *Plugin) purgeStoredSessionKeys() (int, error) {
	removed := 0
	for page := 0; ; page++ {
		keys, appErr := p.API.KVList(page, 200)
		if appErr != nil {
			return removed, fmt.Errorf("failed to list KV keys: %w", appErr)
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, sessionMapKeyPrefix) && !strings.HasPrefix(key, sessionMetaKeyPrefix) {
				continue
			}
			if appErr := p.API.KVDelete(key); appErr != nil {
				return removed, fmt.Errorf("failed to delete session key %q: %w", key, appErr)
			}
			removed++
		}
		if len(keys) < 200 {
			break
		}
	}
	return removed, nil
}
