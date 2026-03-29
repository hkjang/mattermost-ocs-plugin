# Getting Started

This guide walks through the shortest path to a working Mattermost OCS Plugin installation.

## 1. Prepare OpenCode Server

You need a reachable OpenCode Server with:

- a valid base URL
- Basic Auth credentials
- at least one usable model or agent

Before configuring Mattermost, verify the OpenCode server answers:

- `GET /global/health`
- `GET /agent`
- `GET /provider`

## 2. Install The Plugin

1. Build the package with `make dist` or download a release archive.
2. In Mattermost System Console, upload the plugin tarball.
3. Enable the plugin.

## 3. Configure Service Access

Open the plugin custom settings screen and set:

- `service.base_url`
- `service.username`
- `service.password`
- `service.allow_hosts` if you want host allow-list enforcement

Then run the built-in connection test.

## 4. Add Bots

Create at least one bot definition.

Recommended first setup:

- a conversation bot for summaries or general assistant work
- a coding bot for project-aware development workflows

## 5. Test A Conversation Bot

1. Mention the bot in a channel thread.
2. Ask a summary or drafting question.
3. Send a follow-up question in the same thread.
4. Confirm the bot continues the same OpenCode session.

## 6. Test A Coding Bot

1. Open the RHS runner or mention the coding bot.
2. Ask it to inspect a file or review a change.
3. Confirm the response includes project-aware context.
4. If it returns an `ocs-task`, approve a safe command and verify the task card updates.

## 7. Verify Admin Diagnostics

Use the diagnostics section to review:

- health and version
- available agents
- available providers and models
- active sessions
- bot sync state

## Recommended First Rollout

Start with one team and one coding bot in a limited channel before broad rollout. That makes it easier to tune prompts, permissions, and allowed commands.
