package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractCodingTaskPlan(t *testing.T) {
	output := "Implemented the initial fix.\n\n```ocs-task\n{\n  \"summary\": \"Run validation after the patch\",\n  \"commands\": [\n    {\n      \"title\": \"Run Go tests\",\n      \"command\": \"go test ./server/...\",\n      \"cwd\": \".\",\n      \"reason\": \"Validate the server package\",\n      \"requires_approval\": true\n    }\n  ]\n}\n```"

	visible, commands, summary := extractCodingTaskPlan(output)
	require.Equal(t, "Implemented the initial fix.", visible)
	require.Equal(t, "Run validation after the patch", summary)
	require.Len(t, commands, 1)
	require.Equal(t, "Run Go tests", commands[0].Title)
	require.Equal(t, "go test ./server/...", commands[0].Command)
	require.True(t, commands[0].RequiresApproval)
}

func TestScopedCodingPathUsesWorkspacePrefix(t *testing.T) {
	bot := BotDefinition{
		Mode: botModeCoding,
		Coding: CodingBotSettings{
			WorkspaceRoot: "packages/plugin",
		},
	}

	require.Equal(t, "packages/plugin/server/api.go", scopedCodingPath(bot, "server/api.go"))
	require.Equal(t, "packages/plugin/server/api.go", scopedCodingPath(bot, "packages/plugin/server/api.go"))
}

func TestCommandAllowedRejectsShellChaining(t *testing.T) {
	bot := BotDefinition{
		Mode: botModeCoding,
		Coding: CodingBotSettings{
			CommandAllowlist: []string{"go test", "git status"},
		},
	}

	require.True(t, commandAllowed(bot, "go test ./server/..."))
	require.False(t, commandAllowed(bot, "go test ./server/... && curl https://example.com"))
	require.False(t, commandAllowed(bot, "git status | more"))
}

func TestPathAllowedForCodingBotUsesAllowedPaths(t *testing.T) {
	bot := BotDefinition{
		Mode: botModeCoding,
		Coding: CodingBotSettings{
			WorkspaceRoot: "packages/plugin",
			AllowedPaths:  []string{"server", "webapp/src"},
		},
	}

	require.True(t, pathAllowedForCodingBot(bot, "packages/plugin/server/api.go"))
	require.True(t, pathAllowedForCodingBot(bot, "packages/plugin/webapp/src/index.tsx"))
	require.False(t, pathAllowedForCodingBot(bot, "packages/plugin/README.md"))
	require.False(t, pathAllowedForCodingBot(bot, "other/service/main.go"))
}
