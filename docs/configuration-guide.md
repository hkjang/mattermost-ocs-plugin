# Configuration Guide

This guide explains the plugin configuration structure and the most important operational choices.

## Service

`service.base_url`

- OpenCode server base URL
- should point at the documented server API root

`service.username`

- Basic Auth username

`service.password`

- Basic Auth password

`service.allow_hosts`

- comma-separated host allow list
- useful for keeping plugin traffic scoped to approved OpenCode hosts

## Runtime

`runtime.default_timeout_seconds`

- request timeout used for regular service calls

`runtime.enable_streaming`

- enables SSE-backed progressive updates in Mattermost posts

`runtime.streaming_update_ms`

- controls how often a streaming Mattermost post is refreshed

`runtime.context_post_limit`

- number of recent Mattermost posts injected as prompt context

`runtime.max_input_length`

- maximum text length accepted for outgoing prompts

`runtime.max_output_length`

- maximum stored or rendered output length

`runtime.enable_usage_logs`

- stores structured execution metadata for diagnostics

## OpenCode Defaults

`opencode_defaults.provider_id`

- default provider if a bot does not override it

`opencode_defaults.model_id`

- default model if a bot does not override it

`opencode_defaults.agent_id`

- default agent if a bot does not override it

## Session Policy

`session_policy.reuse_scope`

- `thread`
- `dm`
- `channel`

`session_policy.idle_expire_minutes`

- inactive sessions can be replaced after the configured idle window

## Bot Definitions

Each bot can have its own:

- display name and description
- mode
- default agent
- default model
- system prompt
- tool policy
- access control rules

## Bot Modes

### Conversation

Use this mode for:

- summaries
- drafting
- Q&A
- support bots
- assistant workflows that do not need coding-specific UX

### Coding

Use this mode for:

- repository-aware development help
- project search
- file lookup
- approved command execution
- coding task cards and session diff review

## Coding Settings

`coding.profile`

- semantic bot role such as `implementer`, `reviewer`, `triage`, or `release`

`coding.workspace_root`

- optional OpenCode project-relative working directory hint

`coding.workspace_label`

- user-facing workspace label

`coding.default_branch`

- informational branch target shown in the UI

`coding.allowed_paths`

- restricts which project-relative paths may be surfaced or referenced in coding flows

`coding.command_allowlist`

- approved command prefixes for coding task execution

`coding.require_command_approval`

- when enabled, users must explicitly trigger task commands

`coding.include_workspace_snapshot`

- includes OpenCode project and VCS metadata in prompts

`coding.include_referenced_files`

- includes content from referenced files when the user mentions files in the prompt

`coding.max_referenced_files`

- prompt guardrail to avoid oversharing large file sets

`coding.command_timeout_seconds`

- timeout used for approved coding task commands

## Recommended Defaults

- enable streaming
- keep context limits moderate
- start coding bots with strict command allowlists
- restrict coding bots to specific teams or channels during rollout
