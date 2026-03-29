# Operations Guide

This guide focuses on operating the plugin safely in production.

## Health Checks

Use the admin diagnostics panel to verify:

- OpenCode health
- version
- agent catalog
- provider catalog
- active sessions
- recent bot sync status

## Session Management

The plugin stores session mappings in the Mattermost plugin KV store.

Admins can:

- inspect recent active sessions
- abort an active run
- reset a stored session mapping

## Error Handling

The plugin separates:

- user-facing failure messages
- operational detail in logs and diagnostics

When OpenCode fails, the user sees a generalized failure summary while the plugin retains more detailed execution context for admins.

## Logging

Recommended during rollout:

- enable usage logs
- leave debug logs off unless troubleshooting

When debugging:

- turn on debug logs temporarily
- reproduce the issue
- turn them back off after collecting enough information

## Coding Bot Operations

Pay special attention to:

- command allowlists
- access controls
- session reuse scope
- idle expiration windows

For coding bots, review whether the current OpenCode project is the intended one before broad rollout.

## Upgrade Checklist

1. Review release notes and configuration changes.
2. Build or download the new tarball.
3. Upload and enable the new plugin version.
4. Run the connection test.
5. Verify one conversation bot and one coding bot end-to-end.

## GitHub Pages

The `docs/` folder contains a static landing page and repository documentation. It is suitable for serving from the repository docs folder through GitHub Pages.
