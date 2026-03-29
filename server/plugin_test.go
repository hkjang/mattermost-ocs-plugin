package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestParseBotDefinitions(t *testing.T) {
	bots, err := parseBotDefinitions(`[
		{"id":"support","username":"support-bot","display_name":"Support","default_agent":"support-agent","default_model":"anthropic/sonnet","system_prompt":"help","tool_policy":"inherit","input_schema":[{"name":"tone","type":"text"}]},
		{"username":"summary-bot","display_name":"Thread Summary","default_agent":"summary-agent"}
	]`)
	require.NoError(t, err)
	require.Len(t, bots, 2)
	require.Equal(t, "support", bots[0].ID)
	require.Equal(t, "support-agent", bots[0].DefaultAgent)
	require.Equal(t, "anthropic/sonnet", bots[0].DefaultModel)
	require.Equal(t, "summary-bot", bots[1].ID)
}

func TestConfigurationNormalizeFromConfig(t *testing.T) {
	cfg := &configuration{
		Config: `{
			"service": {
				"base_url": "https://opencode.example.com",
				"username": "opencode",
				"password": "secret",
				"allow_hosts": "opencode.example.com"
			},
			"runtime": {
				"default_timeout_seconds": 55,
				"enable_streaming": true,
				"streaming_update_ms": 900,
				"max_input_length": 5000,
				"max_output_length": 9000,
				"context_post_limit": 12,
				"enable_debug_logs": true,
				"enable_usage_logs": false
			},
			"opencode_defaults": {
				"provider_id": "anthropic",
				"model_id": "claude-sonnet",
				"agent_id": "default"
			},
			"session_policy": {
				"reuse_scope": "thread",
				"idle_expire_minutes": 30
			},
			"bots": [
				{"username":"summary-bot","display_name":"Thread Summary","default_agent":"summary-agent"}
			]
		}`,
	}

	runtimeCfg, err := cfg.normalize()
	require.NoError(t, err)
	require.Equal(t, "https://opencode.example.com", runtimeCfg.OpenCodeBaseURL)
	require.Equal(t, "opencode", runtimeCfg.OpenCodeUsername)
	require.Equal(t, "secret", runtimeCfg.OpenCodePassword)
	require.Equal(t, "anthropic", runtimeCfg.DefaultProviderID)
	require.Equal(t, "claude-sonnet", runtimeCfg.DefaultModelID)
	require.Equal(t, "default", runtimeCfg.DefaultAgentID)
	require.Equal(t, "thread", runtimeCfg.SessionReuseScope)
	require.Equal(t, 30, int(runtimeCfg.SessionIdleExpire.Minutes()))
	require.True(t, runtimeCfg.EnableStreaming)
	require.Len(t, runtimeCfg.BotDefinitions, 1)
}

func TestBuildConversationKey(t *testing.T) {
	cfg := &runtimeConfiguration{SessionReuseScope: "thread"}
	channel := &model.Channel{Type: model.ChannelTypeOpen}
	key := buildConversationKey(cfg, channel, BotRunRequest{
		ChannelID:     "channel-id",
		RootID:        "root-id",
		TriggerPostID: "post-id",
		UserID:        "user-id",
	}, BotDefinition{ID: "assistant"})
	require.Equal(t, "thread:channel-id:root-id:assistant", key)

	dmKey := buildConversationKey(cfg, &model.Channel{Type: model.ChannelTypeDirect}, BotRunRequest{
		ChannelID: "dm-channel-id",
	}, BotDefinition{ID: "assistant"})
	require.Equal(t, "dm:dm-channel-id:assistant", dmKey)
}

func TestBuildOpenCodeRequestUsesBasicAuth(t *testing.T) {
	parsedURL, err := url.Parse("https://opencode.example.com/base")
	require.NoError(t, err)

	plugin := &Plugin{}
	cfg := &runtimeConfiguration{
		ParsedBaseURL:    parsedURL,
		OpenCodeUsername: "opencode",
		OpenCodePassword: "secret",
		AllowHosts:       []string{"opencode.example.com"},
	}

	request, err := plugin.newOpenCodeRequest(context.Background(), cfg, "POST", []string{"session", "abc", "message"}, []byte(`{}`), "application/json")
	require.NoError(t, err)
	require.Equal(t, "https://opencode.example.com/base/session/abc/message", request.URL.String())
	require.Equal(t, "application/json", request.Header.Get("Accept"))
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:secret"))
	require.Equal(t, expected, request.Header.Get("Authorization"))
}

func TestParseOpenCodeStreamSSELine(t *testing.T) {
	parser := openCodeStreamParser{}

	event, _, err := parser.readEvent(bufio.NewReader(strings.NewReader("event: message.part.updated\ndata: {\"data\":{\"parts\":[{\"type\":\"text-delta\",\"text\":\"World\"}]}}\n\n")))
	require.NoError(t, err)
	require.NotNil(t, event)
	update, completed := extractOpenCodeStreamText(*event)
	require.Equal(t, "World", update)
	require.False(t, completed)
}

func TestExtractOpenCodeMessageText(t *testing.T) {
	parts := []map[string]any{
		{"type": "tool-input", "text": "ignore"},
		{"type": "text", "text": "Hello from OpenCode"},
	}
	require.Equal(t, "Hello from OpenCode", extractOpenCodeMessageText(parts))
}

func TestEventBelongsToSessionFromNestedProperties(t *testing.T) {
	event := &openCodeStreamEvent{
		Event: "message.updated",
		Properties: map[string]any{
			"session": map[string]any{
				"id": "session-123",
			},
		},
	}

	require.True(t, eventBelongsToSession("", event, "session-123"))
	require.False(t, eventBelongsToSession("", event, "session-999"))
}

func TestExtractOpenCodeStreamTextFromDocumentedLikePayload(t *testing.T) {
	event := openCodeStreamEvent{
		Event: "message.updated",
		Data: map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{"type": "text", "text": "Hello from nested stream payload"},
				},
			},
		},
	}

	update, completed := extractOpenCodeStreamText(event)
	require.Equal(t, "Hello from nested stream payload", update)
	require.False(t, completed)
}

func TestExtractProviderModelIDs(t *testing.T) {
	models := extractProviderModelIDs("anthropic", []any{
		"claude-sonnet",
		map[string]any{"id": "anthropic/claude-opus"},
		map[string]any{"modelID": "claude-haiku"},
		map[string]any{"name": "claude-sonnet"},
	})

	require.Equal(t, []string{
		"anthropic/claude-sonnet",
		"anthropic/claude-opus",
		"anthropic/claude-haiku",
	}, models)
}

func TestMergeOpenCodeStreamOutputPrefersNewestSnapshot(t *testing.T) {
	require.Equal(t, "Hello", mergeOpenCodeStreamOutput("", "Hello"))
	require.Equal(t, "Hello world", mergeOpenCodeStreamOutput("Hello", "Hello world"))
	require.Equal(t, "Hello world", mergeOpenCodeStreamOutput("Hello", " world"))
}

func TestStripLeadingOpenCodeLabel(t *testing.T) {
	require.Equal(t, "actual response", stripLeadingOpenCodeLabel("### OpenCode\n\nactual response"))
	require.Equal(t, "actual response", stripLeadingOpenCodeLabel("opencode\nactual response"))
	require.Equal(t, "actual response", stripLeadingOpenCodeLabel("**OpenCode**\nactual response"))
}
