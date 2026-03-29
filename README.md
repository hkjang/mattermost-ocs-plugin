# Mattermost OCS Plugin

Mattermost OCS Plugin connects Mattermost bots to an OpenCode Server so teams can run both general-purpose assistant conversations and coding-focused agent workflows inside channels, threads, and direct messages.

The plugin is built around OpenCode sessions instead of one-shot flow execution. That gives you reusable conversation context, bot-specific agent and model defaults, streaming replies, coding task cards, and an admin experience that fits day-to-day operations.

## Why This Plugin

- OpenCode session reuse for threads and DMs
- Bot-specific conversation and coding modes
- Streaming replies into a single Mattermost post
- Coding task cards with approved command execution
- OpenCode API-based project search, file lookup, and session diff support
- Admin diagnostics for health, agents, providers, and active sessions

## What It Looks Like In Practice

- `@summary-bot` can summarize a long thread and keep the same session for follow-up questions
- `@repo-coder-bot` can inspect the current OpenCode project, suggest a coding plan, and attach runnable commands as a task card
- RHS users can pick a bot, reuse a prior session, and work without leaving Mattermost
- Admins can verify connectivity, review managed bots, and reset or abort stuck sessions

## Core Concepts

### Conversation Bots

Conversation bots are optimized for Q&A, summaries, drafting, and general assistant work.

- Default agent and model per bot
- Optional recent thread or channel context
- Session reuse by thread, DM, or channel policy
- Streaming text updates with progressive Mattermost post refresh

### Coding Bots

Coding bots are optimized for repository-aware development workflows through OpenCode.

- Work against the current OpenCode project
- Use OpenCode file, search, VCS, session shell, and session diff APIs
- Add workspace context and referenced file content into prompts
- Return `ocs-task` plans that become tracked Mattermost task cards
- Require user approval before command execution when configured

Important:
`coding.workspace_root` is an optional OpenCode project-relative working directory hint. It is not a requirement for local filesystem access inside the Mattermost plugin.

## Quick Start

1. Install and run an OpenCode Server.
2. Build or download the plugin package.
3. Upload the plugin to Mattermost.
4. Open the plugin settings and configure:
   - `service.base_url`
   - `service.username`
   - `service.password`
   - optional `allow_hosts`
5. Add one or more bots in the custom config UI.
6. Run the admin connection test.
7. Mention a bot in a channel, use a DM, or launch it from the RHS panel.

## Configuration Example

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
      "username": "summary-bot",
      "display_name": "Thread Summary",
      "mode": "conversation",
      "default_agent": "summary-agent",
      "default_model": "anthropic/claude-sonnet",
      "system_prompt": "Summarize the conversation and recommend the next steps.",
      "tool_policy": "inherit",
      "include_context_by_default": true,
      "allowed_teams": ["engineering"]
    },
    {
      "username": "repo-coder-bot",
      "display_name": "Repo Coder",
      "mode": "coding",
      "default_agent": "implementer",
      "default_model": "anthropic/claude-sonnet",
      "system_prompt": "Work like a careful coding agent. Keep plans concrete and validate with commands when needed.",
      "tool_policy": "inherit",
      "include_context_by_default": true,
      "allowed_channels": ["dev-ai"],
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

1. The plugin resolves the selected bot from a mention, DM, or RHS request.
2. The plugin creates or reuses an OpenCode session for the active conversation scope.
3. The prompt is sent through `POST /session/:id/message` or `POST /session/:id/prompt_async`.
4. Streaming updates are reflected into a single Mattermost post through OpenCode SSE events.
5. Coding bots can enrich prompts with OpenCode project metadata and referenced files.
6. Coding replies can include an `ocs-task` block, which becomes a Mattermost task card with approved command actions.

## Documentation

- [Getting Started](docs/getting-started.md)
- [Configuration Guide](docs/configuration-guide.md)
- [Coding Bot Guide](docs/coding-bots.md)
- [Operations Guide](docs/operations-guide.md)
- [GitHub Pages Promo Site](docs/index.html)

## Build And Package

```bash
make dist
```

That produces a Mattermost plugin archive under `dist/`.

## Validation

Server and webapp verification used in this repository:

- `go test ./server/...`
- `npm run check-types`
- `npm test -- --runInBand`
- `npm run build`

## Notes

- The plugin is designed around documented OpenCode server APIs.
- Coding mode uses OpenCode project, file, search, VCS, shell, and session diff endpoints instead of local plugin filesystem execution.
- The GitHub Pages site in `docs/` is static and can be served directly from the repository docs folder.
