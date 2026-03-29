# Mattermost OpenCode Plugin

Mattermost channels, threads, and DMs can trigger OpenCode sessions through dedicated Mattermost bots. Each configured bot can define its own default agent, model, system prompt, tool policy, and access controls.

## Highlights

- OpenCode session creation and reuse for threads and DMs
- Streaming replies by updating a single Mattermost post
- Basic Auth support for the OpenCode server
- Bot-specific default agent, model, and prompt policy
- Conversation and coding bot modes with separate workspace policy
- Right-hand sidebar runner and per-user execution history
- Admin connection test for health, agents, and providers

## Configuration Shape

```json
{
  "service": {
    "base_url": "https://opencode.example.com",
    "username": "opencode",
    "password": "secret",
    "allow_hosts": "opencode.example.com"
  },
  "runtime": {
    "default_timeout_seconds": 30,
    "enable_streaming": true,
    "streaming_update_ms": 350,
    "max_input_length": 4000,
    "max_output_length": 8000,
    "context_post_limit": 8,
    "enable_debug_logs": false,
    "enable_usage_logs": true
  },
  "opencode_defaults": {
    "provider_id": "anthropic",
    "model_id": "claude-sonnet",
    "agent_id": "default"
  },
  "session_policy": {
    "reuse_scope": "thread",
    "idle_expire_minutes": 120
  },
  "bots": [
    {
      "username": "thread-summary-bot",
      "display_name": "Thread Summary",
      "mode": "conversation",
      "default_agent": "summary-agent",
      "default_model": "anthropic/claude-sonnet",
      "system_prompt": "Summarize the conversation and next steps.",
      "tool_policy": "inherit",
      "include_context_by_default": true,
      "allowed_teams": ["engineering"],
      "allowed_channels": ["town-square"],
      "allowed_users": []
    },
    {
      "username": "repo-coder-bot",
      "display_name": "Repo Coder",
      "mode": "coding",
      "default_agent": "implementer",
      "default_model": "anthropic/claude-sonnet",
      "system_prompt": "Work like a careful coding agent. Prefer small safe changes and explain validation steps.",
      "tool_policy": "inherit",
      "include_context_by_default": true,
      "allowed_teams": ["engineering"],
      "allowed_channels": ["dev-ai"],
      "allowed_users": [],
      "coding": {
        "profile": "implementer",
        "workspace_root": "packages/plugin",
        "workspace_label": "mattermost-ocs-plugin",
        "default_branch": "main",
        "allowed_paths": ["server", "webapp/src", "README.md"],
        "command_allowlist": ["git status", "git diff", "go test", "npm test", "npm run build"],
        "require_command_approval": true,
        "include_workspace_snapshot": true,
        "include_referenced_files": true,
        "max_referenced_files": 4,
        "command_timeout_seconds": 180
      }
    }
  ]
}
```

## Runtime Flow

1. The plugin resolves the target bot from a mention, DM, or RHS request.
2. The plugin creates or reuses an OpenCode session for the current conversation.
3. The prompt is sent through `POST /session/:id/message` or `POST /session/:id/prompt_async`.
4. Streaming updates are reflected into one Mattermost post via OpenCode SSE events.
5. Execution history is stored per user in the plugin KV store.

## Coding Mode

- Coding bots use the current OpenCode project and its file APIs.
- `coding.workspace_root` is an optional project-relative working directory hint, not a local plugin filesystem dependency.
- The plugin adds OpenCode workspace status and referenced file snippets into the prompt when enabled.
- Coding responses can include an `ocs-task` block that becomes a tracked task card in Mattermost.
- Approved commands run through OpenCode session APIs with allowlist and path restrictions.
- RHS includes workspace search and task history for coding bots.
