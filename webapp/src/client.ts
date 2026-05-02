import manifest from 'manifest';

let siteURL = '';

type RequestOptions = Omit<RequestInit, 'headers'> & {
    headers?: Record<string, string>;
};

export type BotInputField = {
    name: string;
    label: string;
    description?: string;
    type?: string;
    required?: boolean;
    placeholder?: string;
    default_value?: unknown;
};

export type CodingBotSettings = {
    profile?: string;
    workspace_root?: string;
    workspace_label?: string;
    default_branch?: string;
    allowed_paths?: string[];
    command_allowlist?: string[];
    require_command_approval?: boolean;
    include_workspace_snapshot?: boolean;
    include_referenced_files?: boolean;
    max_referenced_files?: number;
    command_timeout_seconds?: number;
};

export type BotDefinition = {
    id: string;
    username: string;
    display_name: string;
    description?: string;
    mode?: 'conversation' | 'coding';
    base_url?: string;
    basic_auth_username?: string;
    basic_auth_password?: string;
    default_agent?: string;
    default_model?: string;
    system_prompt?: string;
    tool_policy?: string;
    include_context_by_default?: boolean;
    allowed_teams?: string[];
    allowed_channels?: string[];
    allowed_users?: string[];
    input_schema?: BotInputField[];
    coding?: CodingBotSettings;
};

export type ExecutionRecord = {
    bot_id: string;
    bot_username: string;
    bot_name: string;
    bot_mode?: string;
    session_id: string;
    task_id?: string;
    agent_id?: string;
    model_id?: string;
    status: string;
    error_message?: string;
    error_code?: string;
    source: string;
    prompt_preview: string;
    channel_id?: string;
    root_id?: string;
    duration_ms?: number;
    started_at?: number;
    completed_at?: number;
};

export type BotRunResult = {
    bot_id: string;
    bot_username: string;
    bot_name: string;
    session_id?: string;
    agent_id?: string;
    model_id?: string;
    bot_mode?: string;
    task_id?: string;
    post_id?: string;
    status: string;
    output?: string;
    error_message?: string;
    error_code?: string;
    retryable?: boolean;
};

export type PluginStatus = {
    plugin_id: string;
    base_url: string;
    bot_count: number;
    allow_hosts: string[];
    bots: BotDefinition[];
    managed_bots: ManagedBotStatus[];
    bot_sync: BotSyncState;
    streaming_enabled: boolean;
    streaming_update_interval_ms: number;
    default_provider_id?: string;
    default_model_id?: string;
    default_agent_id?: string;
    session_reuse_scope?: string;
    session_idle_expire_minutes?: number;
    config_error?: string;
};

export type AdminPluginConfig = {
    schema_version?: number;
    service: {
        base_url: string;
        username: string;
        password: string;
        allow_hosts: string;
    };
    runtime: {
        default_timeout_seconds: number;
        enable_streaming: boolean;
        streaming_update_ms: number;
        max_input_length: number;
        max_output_length: number;
        context_post_limit: number;
        enable_debug_logs: boolean;
        enable_usage_logs: boolean;
    };
    opencode_defaults: {
        provider_id: string;
        model_id: string;
        agent_id: string;
    };
    session_policy: {
        reuse_scope: string;
        idle_expire_minutes: number;
    };
    bots: BotDefinition[];
};

export type AdminConfigResponse = {
    config: AdminPluginConfig;
    source: string;
};

export type ManagedBotStatus = {
    bot_id: string;
    username: string;
    display_name: string;
    agent_id?: string;
    model_id?: string;
    user_id?: string;
    registered: boolean;
    active: boolean;
    status_message?: string;
};

export type BotSyncState = {
    last_error?: string;
    updated_at: number;
    entries: ManagedBotStatus[];
};

export type ConnectionStatus = {
    ok: boolean;
    url: string;
    status_code: number;
    message: string;
    error_code?: string;
    detail?: string;
    hint?: string;
    retryable?: boolean;
    healthy?: boolean;
    version?: string;
    agents?: Array<{id: string; name?: string; description?: string}>;
    providers?: Array<{id: string; name?: string; connected?: boolean; default_model?: string; models?: string[]}>;
};

export type ConversationSession = {
    conversation_key: string;
    session_id: string;
    bot_id: string;
    bot_username?: string;
    bot_name?: string;
    user_id: string;
    user_name?: string;
    channel_id: string;
    channel_name?: string;
    root_id: string;
    title?: string;
    created_at: number;
    last_used_at: number;
    expired?: boolean;
};

export type CodingWorkspaceSnapshot = {
    label?: string;
    root?: string;
    repo_root?: string;
    profile?: string;
    branch?: string;
    default_branch?: string;
    dirty?: boolean;
    changed_files?: string[];
    diff_stat?: string;
    status_summary?: string;
    allowed_paths?: string[];
    allowed_commands?: string[];
};

export type CodingDiff = {
    path: string;
    summary?: string;
};

export type CodingCommand = {
    id: string;
    title?: string;
    command: string;
    cwd?: string;
    reason?: string;
    requires_approval?: boolean;
    status?: string;
    exit_code?: number;
    output_preview?: string;
    test_summary?: string;
    duration_ms?: number;
    started_at?: number;
    completed_at?: number;
    error_message?: string;
};

export type CodingTask = {
    id: string;
    session_id: string;
    bot_id: string;
    bot_username: string;
    bot_name: string;
    user_id: string;
    channel_id: string;
    root_id: string;
    post_id?: string;
    status: string;
    summary?: string;
    response_message?: string;
    workspace: CodingWorkspaceSnapshot;
    diffs?: CodingDiff[];
    commands?: CodingCommand[];
    last_command_id?: string;
    created_at: number;
    updated_at: number;
};

export type CodingSearchResult = {
    kind?: string;
    path: string;
    line: number;
    preview: string;
};

export function setSiteURL(value: string) {
    siteURL = value.replace(/\/+$/, '');
}

function pluginURL(path: string) {
    const base = siteURL || window.location.origin;
    return `${base}/plugins/${manifest.id}/api/v1${path}`;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const response = await fetch(pluginURL(path), {
        ...options,
        headers: {
            'Content-Type': 'application/json',
            ...(options.headers || {}),
        },
    });

    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
        const failure = data as {error?: string; error_message?: string};
        throw new Error(failure.error || failure.error_message || 'Request failed');
    }
    return data as T;
}

export async function getStatus() {
    return request<PluginStatus>('/status');
}

export async function getAdminConfig() {
    return request<AdminConfigResponse>('/config');
}

export async function testConnection() {
    return request<ConnectionStatus>('/test', {method: 'POST'});
}

export async function getBots(channelId?: string) {
    const query = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : '';
    const response = await request<{bots: BotDefinition[]}>(`/bots${query}`);
    return response.bots;
}

export async function getHistory(limit = 5) {
    const response = await request<{items: ExecutionRecord[]}>(`/history?limit=${limit}`);
    return response.items;
}

export async function getSessions(limit = 20) {
    const response = await request<{items: ConversationSession[]}>(`/sessions?limit=${limit}`);
    return response.items;
}

export async function resetSession(payload: {session_id?: string; conversation_key?: string}) {
    return request<{ok: boolean}>('/sessions/reset', {
        method: 'POST',
        body: JSON.stringify(payload),
    });
}

export async function abortSession(payload: {session_id: string}) {
    return request<{ok: boolean}>('/sessions/abort', {
        method: 'POST',
        body: JSON.stringify(payload),
    });
}

export async function getCodingWorkspace(botID: string, channelID?: string) {
    const params = new URLSearchParams({bot_id: botID});
    if (channelID) {
        params.set('channel_id', channelID);
    }
    return request<CodingWorkspaceSnapshot>(`/coding/workspace?${params.toString()}`);
}

export async function searchCodingWorkspace(botID: string, query: string, limit = 10, channelID?: string) {
    const params = new URLSearchParams({bot_id: botID, q: query, limit: String(limit)});
    if (channelID) {
        params.set('channel_id', channelID);
    }
    const response = await request<{items: CodingSearchResult[]}>(`/coding/search?${params.toString()}`);
    return response.items;
}

export async function getCodingTask(taskID: string) {
    return request<CodingTask>(`/coding/task/${encodeURIComponent(taskID)}`);
}

export async function runCodingTaskCommand(payload: {task_id: string; command_id: string}) {
    return request<CodingTask>('/coding/task/command/run', {
        method: 'POST',
        body: JSON.stringify(payload),
    });
}

export async function runBot(payload: {
    bot_id: string;
    channel_id: string;
    root_id?: string;
    prompt: string;
    include_context: boolean;
    inputs: Record<string, unknown>;
    reuse_session_id?: string;
}) {
    return request<BotRunResult>('/run', {
        method: 'POST',
        body: JSON.stringify(payload),
    });
}
