# Coding Bot Guide

Coding bots are designed for development workflows that need more than a plain chat response.

## What Makes A Coding Bot Different

Coding bots can:

- inspect the current OpenCode project context
- search files and symbols through OpenCode APIs
- include referenced file content into the prompt
- show session diff data in task cards
- return planned commands as `ocs-task` actions

They still use the same OpenCode session lifecycle as other bots, which means follow-up requests can stay in the same thread or DM session.

## Supported Coding Workflow

1. User asks a coding bot for a change, review, or investigation.
2. Plugin builds a prompt with optional Mattermost context plus OpenCode project context.
3. OpenCode returns a response.
4. If the response includes an `ocs-task`, the plugin stores a coding task record.
5. Mattermost renders a task card with status, approved commands, and session diff metadata.
6. Approved commands are sent back through OpenCode session APIs.

## Important Safety Boundaries

The plugin does not execute arbitrary local filesystem operations directly for coding mode.

Instead it relies on documented OpenCode capabilities such as:

- project path
- VCS metadata
- file content
- file status
- project search
- session shell
- session command
- session diff

This keeps the plugin aligned with the OpenCode server contract and avoids hidden local-only behavior.

## Good Prompts For Coding Bots

- `Review the latest session diff and suggest the next safe step.`
- `Inspect server/api.go and explain why this request fails for coding tasks.`
- `Search for session reuse logic and tell me where DM and thread behavior diverge.`
- `Prepare a validation plan for this change and return commands as an ocs-task block.`

## Good Command Allowlist Examples

- `git status`
- `git diff`
- `go test`
- `go build`
- `npm test`
- `npm run build`
- `/undo`
- `/compact`

Keep destructive or highly privileged commands out of the allowlist unless you have a very controlled use case.

## Rollout Advice

- start with one coding bot
- keep access limited to one engineering channel
- require approval for all commands
- watch task history and session diagnostics for a few days before expanding access
