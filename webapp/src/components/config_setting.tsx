import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {
    AdminPluginConfig,
    BotDefinition,
    CodingBotSettings,
    BotInputField,
    ConnectionStatus,
    ConversationSession,
    ManagedBotStatus,
    PluginStatus,
} from '../client';
import {abortSession, getAdminConfig, getSessions, getStatus, resetSession, testConnection} from '../client';

type DraftInputField = BotInputField & {
    id: string;
};

type DraftBotDefinition = {
    local_id: string;
    id: string;
    username: string;
    display_name: string;
    description: string;
    mode: 'conversation' | 'coding';
    default_agent: string;
    default_model: string;
    system_prompt: string;
    tool_policy: string;
    include_context_by_default: boolean;
    allowed_teams: string[];
    allowed_channels: string[];
    allowed_users: string[];
    input_schema: DraftInputField[];
    coding: Required<CodingBotSettings>;
};

type DraftPluginConfig = {
    service: AdminPluginConfig['service'];
    runtime: AdminPluginConfig['runtime'];
    opencode_defaults: AdminPluginConfig['opencode_defaults'];
    session_policy: AdminPluginConfig['session_policy'];
    bots: DraftBotDefinition[];
};

type CustomSettingProps = {
    id?: string;
    value?: unknown;
    disabled?: boolean;
    setByEnv?: boolean;
    helpText?: React.ReactNode;
    onChange: (id: string, value: unknown) => void;
    setSaveNeeded?: () => void;
};

const containerStyle: React.CSSProperties = {
    display: 'flex',
    flexDirection: 'column',
    gap: '20px',
};

const sectionStyle: React.CSSProperties = {
    background: 'white',
    border: '1px solid rgba(63, 67, 80, 0.12)',
    borderRadius: '8px',
    boxShadow: '0 2px 3px rgba(0, 0, 0, 0.08)',
    display: 'flex',
    flexDirection: 'column',
    gap: '16px',
    padding: '24px',
};

const fieldStyle: React.CSSProperties = {
    border: '1px solid rgba(63, 67, 80, 0.16)',
    borderRadius: '8px',
    padding: '10px 12px',
    width: '100%',
};

const textAreaStyle: React.CSSProperties = {
    ...fieldStyle,
    minHeight: '96px',
    resize: 'vertical',
};

const gridTwoStyle: React.CSSProperties = {
    display: 'grid',
    gap: '12px',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
};

const botLayoutStyle: React.CSSProperties = {
    display: 'grid',
    gap: '16px',
    gridTemplateColumns: '320px minmax(0, 1fr)',
};

const botListItemStyle = (selected: boolean): React.CSSProperties => ({
    background: selected ? 'rgba(var(--button-bg-rgb), 0.10)' : 'rgba(63, 67, 80, 0.03)',
    border: `1px solid ${selected ? 'rgba(var(--button-bg-rgb), 0.30)' : 'rgba(63, 67, 80, 0.10)'}`,
    borderRadius: '10px',
    cursor: 'pointer',
    display: 'flex',
    flexDirection: 'column',
    gap: '4px',
    padding: '12px',
    textAlign: 'left',
    width: '100%',
});

export default function ConfigSetting(props: CustomSettingProps) {
    const settingKey = props.id || 'Config';
    const disabled = Boolean(props.disabled || props.setByEnv);

    const [config, setConfig] = useState<DraftPluginConfig>(createDefaultConfig());
    const [selectedBotID, setSelectedBotID] = useState('');
    const [status, setStatus] = useState<PluginStatus | null>(null);
    const [connection, setConnection] = useState<ConnectionStatus | null>(null);
    const [sessions, setSessions] = useState<ConversationSession[]>([]);
    const [source, setSource] = useState('config');
    const [loadError, setLoadError] = useState('');
    const [loadingConfig, setLoadingConfig] = useState(true);
    const [loadingStatus, setLoadingStatus] = useState(true);
    const [loadingSessions, setLoadingSessions] = useState(true);
    const [testingConnection, setTestingConnection] = useState(false);
    const [sessionActionMessage, setSessionActionMessage] = useState('');
    const lastSubmittedValueRef = useRef('');

    useEffect(() => {
        let cancelled = false;

        async function loadConfig() {
            setLoadingConfig(true);
            setLoadError('');

            const serializedValue = serializeSettingValue(props.value);
            if (serializedValue && serializedValue === lastSubmittedValueRef.current) {
                if (!cancelled) {
                    setLoadingConfig(false);
                }
                return;
            }

            const parsedValue = parseStoredConfigValue(props.value);
            if (parsedValue.ok) {
                if (!cancelled) {
                    setConfig(parsedValue.config);
                    setSource('config');
                    setSelectedBotID((current) => pickSelectedBotID(parsedValue.config.bots, current));
                    lastSubmittedValueRef.current = serializedValue;
                    setLoadingConfig(false);
                }
                return;
            }

            try {
                const response = await getAdminConfig();
                if (cancelled) {
                    return;
                }
                const nextConfig = normalizeAdminConfig(response.config);
                setConfig(nextConfig);
                setSource(response.source || 'config');
                setSelectedBotID((current) => pickSelectedBotID(nextConfig.bots, current));
                lastSubmittedValueRef.current = serializeSettingValue(buildStoredConfig(nextConfig));
            } catch (error) {
                if (!cancelled) {
                    setLoadError((error as Error).message);
                }
            } finally {
                if (!cancelled) {
                    setLoadingConfig(false);
                }
            }
        }

        loadConfig();

        return () => {
            cancelled = true;
        };
    }, [props.value]);

    useEffect(() => {
        let cancelled = false;

        async function loadStatus() {
            setLoadingStatus(true);
            setLoadingSessions(true);
            try {
                const [pluginStatus, activeSessions] = await Promise.all([
                    getStatus(),
                    getSessions(20),
                ]);
                if (!cancelled) {
                    setStatus(pluginStatus);
                    setSessions(activeSessions);
                }
            } catch (error) {
                if (!cancelled) {
                    setLoadError((error as Error).message);
                }
            } finally {
                if (!cancelled) {
                    setLoadingStatus(false);
                    setLoadingSessions(false);
                }
            }
        }

        loadStatus();

        return () => {
            cancelled = true;
        };
    }, []);

    const selectedBot = useMemo(
        () => config.bots.find((bot) => bot.local_id === selectedBotID) || config.bots[0] || null,
        [config.bots, selectedBotID],
    );

    const validationMessages = useMemo(() => validateConfig(config), [config]);
    const providerOptions = useMemo(() => buildProviderOptions(connection), [connection]);
    const defaultModelOptions = useMemo(
        () => buildModelOptions(connection, config.opencode_defaults.provider_id),
        [connection, config.opencode_defaults.provider_id],
    );
    const agentOptions = useMemo(() => buildAgentOptions(connection), [connection]);
    const botModelOptions = useMemo(() => buildModelOptions(connection, ''), [connection]);

    const applyConfig = (nextConfig: DraftPluginConfig, nextSelectedBotID?: string) => {
        setConfig(nextConfig);
        const nextValue = JSON.stringify(buildStoredConfig(nextConfig), null, 2);
        lastSubmittedValueRef.current = nextValue;
        props.onChange(settingKey, nextValue);
        props.setSaveNeeded?.();

        if (nextConfig.bots.length === 0) {
            setSelectedBotID('');
            return;
        }

        if (nextSelectedBotID) {
            setSelectedBotID(nextSelectedBotID);
            return;
        }

        setSelectedBotID((current) => pickSelectedBotID(nextConfig.bots, current));
    };

    const updateConfig = <K extends keyof DraftPluginConfig>(key: K, value: DraftPluginConfig[K]) => {
        applyConfig({...config, [key]: value});
    };

    const updateBot = (localID: string, updater: (bot: DraftBotDefinition) => DraftBotDefinition) => {
        applyConfig({
            ...config,
            bots: config.bots.map((bot) => (bot.local_id === localID ? updater(bot) : bot)),
        }, localID);
    };

    const addBot = () => {
        const nextBot = createEmptyBot();
        applyConfig({...config, bots: [...config.bots, nextBot]}, nextBot.local_id);
    };

    const duplicateBot = () => {
        if (!selectedBot) {
            return;
        }
        const duplicate = cloneBot(selectedBot);
        applyConfig({...config, bots: [...config.bots, duplicate]}, duplicate.local_id);
    };

    const removeSelectedBot = () => {
        if (!selectedBot) {
            return;
        }
        applyConfig({...config, bots: config.bots.filter((bot) => bot.local_id !== selectedBot.local_id)});
    };

    const runConnectionTest = async () => {
        setTestingConnection(true);
        setConnection(null);
        try {
            setConnection(await testConnection());
        } catch (error) {
            setLoadError((error as Error).message);
        } finally {
            setTestingConnection(false);
        }
    };

    const refreshSessions = async () => {
        setLoadingSessions(true);
        setSessionActionMessage('');
        try {
            setSessions(await getSessions(20));
        } catch (error) {
            setLoadError((error as Error).message);
        } finally {
            setLoadingSessions(false);
        }
    };

    const handleResetSession = async (session: ConversationSession) => {
        try {
            await resetSession({session_id: session.session_id, conversation_key: session.conversation_key});
            setSessionActionMessage(`Reset session ${session.session_id}.`);
            await refreshSessions();
        } catch (error) {
            setLoadError((error as Error).message);
        }
    };

    const handleAbortSession = async (session: ConversationSession) => {
        try {
            await abortSession({session_id: session.session_id});
            setSessionActionMessage(`Aborted active run for session ${session.session_id}.`);
            await refreshSessions();
        } catch (error) {
            setLoadError((error as Error).message);
        }
    };

    return (
        <div style={containerStyle}>
            <section style={sectionStyle}>
                <div>
                    <div style={{fontSize: '16px', fontWeight: 600}}>{'OpenCode Settings'}</div>
                    <div style={{color: 'rgba(63, 67, 80, 0.72)', fontSize: '14px'}}>
                        {'Configure the OpenCode service, session policy, and bot catalog from one screen.'}
                    </div>
                    <div style={{marginTop: '8px', fontSize: '12px', opacity: 0.8}}>
                        {`Config source: ${source}`}
                    </div>
                </div>
                {loadError && <div style={{color: 'var(--error-text)'}}>{loadError}</div>}
                {validationMessages.length > 0 && (
                    <div style={{display: 'flex', flexDirection: 'column', gap: '4px'}}>
                        {validationMessages.map((message) => <span key={message}>{message}</span>)}
                    </div>
                )}
                {loadingConfig && <div>{'Loading configuration...'}</div>}
            </section>

            <section style={sectionStyle}>
                <div style={{fontSize: '16px', fontWeight: 600}}>{'Service'}</div>
                <div style={gridTwoStyle}>
                    <LabeledField label={'Base URL'}>
                        <input
                            disabled={disabled}
                            onChange={(event) => updateConfig('service', {...config.service, base_url: event.target.value})}
                            placeholder={'https://opencode.example.com'}
                            style={fieldStyle}
                            value={config.service.base_url}
                        />
                    </LabeledField>
                    <LabeledField label={'Allow Hosts'}>
                        <input
                            disabled={disabled}
                            onChange={(event) => updateConfig('service', {...config.service, allow_hosts: event.target.value})}
                            placeholder={'opencode.example.com, *.internal.example.com'}
                            style={fieldStyle}
                            value={config.service.allow_hosts}
                        />
                    </LabeledField>
                    <LabeledField label={'Username'}>
                        <input disabled={disabled} onChange={(event) => updateConfig('service', {...config.service, username: event.target.value})} style={fieldStyle} value={config.service.username}/>
                    </LabeledField>
                    <LabeledField label={'Password'}>
                        <input disabled={disabled} onChange={(event) => updateConfig('service', {...config.service, password: event.target.value})} style={fieldStyle} type='password' value={config.service.password}/>
                    </LabeledField>
                </div>
            </section>

            <section style={sectionStyle}>
                <div style={{fontSize: '16px', fontWeight: 600}}>{'Runtime Defaults'}</div>
                <div style={gridTwoStyle}>
                    <NumberField label={'Timeout (seconds)'} value={config.runtime.default_timeout_seconds} onChange={(value) => updateConfig('runtime', {...config.runtime, default_timeout_seconds: value})}/>
                    <NumberField label={'Streaming update (ms)'} value={config.runtime.streaming_update_ms} onChange={(value) => updateConfig('runtime', {...config.runtime, streaming_update_ms: value})}/>
                    <NumberField label={'Max input length'} value={config.runtime.max_input_length} onChange={(value) => updateConfig('runtime', {...config.runtime, max_input_length: value})}/>
                    <NumberField label={'Max output length'} value={config.runtime.max_output_length} onChange={(value) => updateConfig('runtime', {...config.runtime, max_output_length: value})}/>
                    <NumberField label={'Context post limit'} value={config.runtime.context_post_limit} onChange={(value) => updateConfig('runtime', {...config.runtime, context_post_limit: value})}/>
                    <LabeledField label={'Streaming'}>
                        <input checked={config.runtime.enable_streaming} disabled={disabled} onChange={(event) => updateConfig('runtime', {...config.runtime, enable_streaming: event.target.checked})} type='checkbox'/>
                    </LabeledField>
                    <LabeledField label={'Debug logs'}>
                        <input checked={config.runtime.enable_debug_logs} disabled={disabled} onChange={(event) => updateConfig('runtime', {...config.runtime, enable_debug_logs: event.target.checked})} type='checkbox'/>
                    </LabeledField>
                    <LabeledField label={'Usage logs'}>
                        <input checked={config.runtime.enable_usage_logs} disabled={disabled} onChange={(event) => updateConfig('runtime', {...config.runtime, enable_usage_logs: event.target.checked})} type='checkbox'/>
                    </LabeledField>
                </div>

                <div style={gridTwoStyle}>
                    <CatalogField
                        disabled={disabled}
                        emptyLabel={'Server default'}
                        label={'Default Provider'}
                        onChange={(value) => updateConfig('opencode_defaults', {...config.opencode_defaults, provider_id: value})}
                        options={providerOptions}
                        placeholder={'provider id'}
                        value={config.opencode_defaults.provider_id}
                    />
                    <CatalogField
                        disabled={disabled}
                        emptyLabel={'Server default'}
                        label={'Default Model'}
                        onChange={(value) => updateConfig('opencode_defaults', {...config.opencode_defaults, model_id: value})}
                        options={defaultModelOptions}
                        placeholder={'provider/model or model'}
                        value={config.opencode_defaults.model_id}
                    />
                    <CatalogField
                        disabled={disabled}
                        emptyLabel={'Server default'}
                        label={'Default Agent'}
                        onChange={(value) => updateConfig('opencode_defaults', {...config.opencode_defaults, agent_id: value})}
                        options={agentOptions}
                        placeholder={'agent id'}
                        value={config.opencode_defaults.agent_id}
                    />
                    <LabeledField label={'Reuse Scope'}>
                        <select disabled={disabled} onChange={(event) => updateConfig('session_policy', {...config.session_policy, reuse_scope: event.target.value})} style={fieldStyle} value={config.session_policy.reuse_scope}>
                            <option value='thread'>{'thread'}</option>
                            <option value='dm'>{'dm'}</option>
                            <option value='channel'>{'channel'}</option>
                        </select>
                    </LabeledField>
                    <NumberField label={'Idle expire (minutes)'} value={config.session_policy.idle_expire_minutes} onChange={(value) => updateConfig('session_policy', {...config.session_policy, idle_expire_minutes: value})}/>
                </div>
            </section>

            <section style={sectionStyle}>
                <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'center'}}>
                    <div>
                        <div style={{fontSize: '16px', fontWeight: 600}}>{'Bots'}</div>
                        <div style={{color: 'rgba(63, 67, 80, 0.72)', fontSize: '14px'}}>
                            {'Each Mattermost bot can define its own default agent, model, prompt, and access policy.'}
                        </div>
                    </div>
                    <div style={{display: 'flex', gap: '8px'}}>
                        <button className='btn btn-tertiary' disabled={disabled} onClick={addBot} type='button'>{'Add Bot'}</button>
                        <button className='btn btn-tertiary' disabled={disabled || !selectedBot} onClick={duplicateBot} type='button'>{'Duplicate'}</button>
                        <button className='btn btn-tertiary' disabled={disabled || !selectedBot} onClick={removeSelectedBot} type='button'>{'Remove'}</button>
                    </div>
                </div>

                <div style={botLayoutStyle}>
                    <div style={{display: 'flex', flexDirection: 'column', gap: '8px'}}>
                        {config.bots.length === 0 && <div>{'No bots configured.'}</div>}
                        {config.bots.map((bot) => (
                            <button
                                key={bot.local_id}
                                onClick={() => setSelectedBotID(bot.local_id)}
                                style={botListItemStyle(bot.local_id === selectedBot?.local_id)}
                                type='button'
                            >
                                <strong>{bot.display_name || bot.username || 'New bot'}</strong>
                                <span style={{fontSize: '12px', opacity: 0.8}}>{`@${bot.username || 'username'}`}</span>
                                <span style={{fontSize: '12px', opacity: 0.8}}>{bot.mode === 'coding' ? 'coding' : 'conversation'}</span>
                                <span style={{fontSize: '12px', opacity: 0.8}}>{`${bot.default_agent || 'server default'} / ${bot.default_model || 'server default'}`}</span>
                            </button>
                        ))}
                    </div>

                    {selectedBot && (
                        <div style={{display: 'flex', flexDirection: 'column', gap: '12px'}}>
                            <div style={gridTwoStyle}>
                                <LabeledField label={'Username'}>
                                    <input disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, username: event.target.value}))} style={fieldStyle} value={selectedBot.username}/>
                                </LabeledField>
                                <LabeledField label={'Display Name'}>
                                    <input disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, display_name: event.target.value}))} style={fieldStyle} value={selectedBot.display_name}/>
                                </LabeledField>
                                <LabeledField label={'Mode'}>
                                    <select
                                        disabled={disabled}
                                        onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, mode: event.target.value as DraftBotDefinition['mode']}))}
                                        style={fieldStyle}
                                        value={selectedBot.mode}
                                    >
                                        <option value='conversation'>{'conversation'}</option>
                                        <option value='coding'>{'coding'}</option>
                                    </select>
                                </LabeledField>
                                <CatalogField
                                    disabled={disabled}
                                    emptyLabel={'Server default'}
                                    label={'Default Agent'}
                                    onChange={(value) => updateBot(selectedBot.local_id, (bot) => ({...bot, default_agent: value}))}
                                    options={agentOptions}
                                    placeholder={'agent id'}
                                    value={selectedBot.default_agent}
                                />
                                <CatalogField
                                    disabled={disabled}
                                    emptyLabel={'Server default'}
                                    label={'Default Model'}
                                    onChange={(value) => updateBot(selectedBot.local_id, (bot) => ({...bot, default_model: value}))}
                                    options={botModelOptions}
                                    placeholder={'provider/model or model'}
                                    value={selectedBot.default_model}
                                />
                                <LabeledField label={'Tool Policy'}>
                                    <input disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, tool_policy: event.target.value}))} placeholder={'inherit, none, or ["tool-a","tool-b"]'} style={fieldStyle} value={selectedBot.tool_policy}/>
                                </LabeledField>
                                <LabeledField label={'Include Context'}>
                                    <input checked={selectedBot.include_context_by_default} disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, include_context_by_default: event.target.checked}))} type='checkbox'/>
                                </LabeledField>
                            </div>

                            <LabeledField label={'Description'}>
                                <textarea disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, description: event.target.value}))} style={textAreaStyle} value={selectedBot.description}/>
                            </LabeledField>

                            <LabeledField label={'System Prompt'}>
                                <textarea disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, system_prompt: event.target.value}))} style={textAreaStyle} value={selectedBot.system_prompt}/>
                            </LabeledField>

                            <div style={gridTwoStyle}>
                                <TokenListField label={'Allowed Teams'} value={selectedBot.allowed_teams} onChange={(items) => updateBot(selectedBot.local_id, (bot) => ({...bot, allowed_teams: items}))}/>
                                <TokenListField label={'Allowed Channels'} value={selectedBot.allowed_channels} onChange={(items) => updateBot(selectedBot.local_id, (bot) => ({...bot, allowed_channels: items}))}/>
                                <TokenListField label={'Allowed Users'} value={selectedBot.allowed_users} onChange={(items) => updateBot(selectedBot.local_id, (bot) => ({...bot, allowed_users: items}))}/>
                            </div>

                            {selectedBot.mode === 'coding' && (
                                <>
                                    <div style={{fontSize: '15px', fontWeight: 600}}>{'Coding Workspace'}</div>
                                    <div style={{fontSize: '12px', opacity: 0.8}}>
                                        {'Coding bots use the current OpenCode project and file APIs. The working directory hint is optional and scopes file lookup or shell commands inside that project when needed.'}
                                    </div>
                                    <div style={gridTwoStyle}>
                                        <LabeledField label={'Coding Profile'}>
                                            <select
                                                disabled={disabled}
                                                onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, profile: event.target.value}}))}
                                                style={fieldStyle}
                                                value={selectedBot.coding.profile}
                                            >
                                                <option value='implementer'>{'implementer'}</option>
                                                <option value='reviewer'>{'reviewer'}</option>
                                                <option value='triage'>{'triage'}</option>
                                                <option value='release'>{'release'}</option>
                                            </select>
                                        </LabeledField>
                                        <LabeledField label={'Working Directory Hint'}>
                                            <input disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, workspace_root: event.target.value}}))} style={fieldStyle} value={selectedBot.coding.workspace_root}/>
                                        </LabeledField>
                                        <LabeledField label={'Workspace Label'}>
                                            <input disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, workspace_label: event.target.value}}))} style={fieldStyle} value={selectedBot.coding.workspace_label}/>
                                        </LabeledField>
                                        <LabeledField label={'Default Branch'}>
                                            <input disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, default_branch: event.target.value}}))} style={fieldStyle} value={selectedBot.coding.default_branch}/>
                                        </LabeledField>
                                        <TokenListField label={'Allowed Paths'} value={selectedBot.coding.allowed_paths} onChange={(items) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, allowed_paths: items}}))}/>
                                        <TokenListField label={'Command Allowlist'} value={selectedBot.coding.command_allowlist} onChange={(items) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, command_allowlist: items}}))}/>
                                        <NumberField label={'Command Timeout (seconds)'} value={selectedBot.coding.command_timeout_seconds} onChange={(value) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, command_timeout_seconds: value}}))}/>
                                        <NumberField label={'Max Referenced Files'} value={selectedBot.coding.max_referenced_files} onChange={(value) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, max_referenced_files: value}}))}/>
                                        <LabeledField label={'Require Approval'}>
                                            <input checked={selectedBot.coding.require_command_approval} disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, require_command_approval: event.target.checked}}))} type='checkbox'/>
                                        </LabeledField>
                                        <LabeledField label={'Include Workspace Snapshot'}>
                                            <input checked={selectedBot.coding.include_workspace_snapshot} disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, include_workspace_snapshot: event.target.checked}}))} type='checkbox'/>
                                        </LabeledField>
                                        <LabeledField label={'Include Referenced Files'}>
                                            <input checked={selectedBot.coding.include_referenced_files} disabled={disabled} onChange={(event) => updateBot(selectedBot.local_id, (bot) => ({...bot, coding: {...bot.coding, include_referenced_files: event.target.checked}}))} type='checkbox'/>
                                        </LabeledField>
                                    </div>
                                </>
                            )}
                        </div>
                    )}
                </div>
            </section>

            <section style={sectionStyle}>
                <div style={{fontSize: '16px', fontWeight: 600}}>{'Diagnostics'}</div>
                <div>{loadingStatus ? 'Loading status...' : `Configured bots: ${status?.bot_count || 0}`}</div>
                {status?.config_error && <div>{`Config error: ${status.config_error}`}</div>}
                {status?.bot_sync?.last_error && <div>{`Bot sync error: ${status.bot_sync.last_error}`}</div>}
                <div style={{display: 'flex', gap: '8px', flexWrap: 'wrap'}}>
                    <button className='btn btn-primary' disabled={testingConnection} onClick={runConnectionTest} type='button'>
                        {testingConnection ? 'Testing...' : 'Run Connection Test'}
                    </button>
                    <button className='btn btn-tertiary' disabled={loadingSessions} onClick={refreshSessions} type='button'>
                        {loadingSessions ? 'Refreshing Sessions...' : 'Refresh Sessions'}
                    </button>
                </div>
                {connection && (
                    <div style={{display: 'flex', flexDirection: 'column', gap: '6px'}}>
                        <div>{connection.ok ? 'OpenCode is reachable.' : 'OpenCode connection test failed.'}</div>
                        <div>{connection.url}</div>
                        <div>{connection.message}</div>
                        {connection.version && <div>{`Version: ${connection.version}`}</div>}
                        {connection.detail && <div>{connection.detail}</div>}
                        {connection.hint && <div>{connection.hint}</div>}
                        {(connection.agents || []).length > 0 && <div>{`Agents: ${(connection.agents || []).map((item) => item.id).join(', ')}`}</div>}
                        {(connection.providers || []).length > 0 && <div>{`Providers: ${(connection.providers || []).map((item) => item.id).join(', ')}`}</div>}
                    </div>
                )}
                {(status?.managed_bots || []).length > 0 && (
                    <div style={{display: 'flex', flexDirection: 'column', gap: '8px'}}>
                        {(status?.managed_bots || []).map((bot: ManagedBotStatus) => (
                            <div key={bot.bot_id}>
                                <strong>{bot.display_name || bot.username}</strong>
                                <div>{`@${bot.username}`}</div>
                                <div>{`${bot.agent_id || 'server default'} / ${bot.model_id || 'server default'}`}</div>
                                <div>{`Registered: ${bot.registered ? 'yes' : 'no'}, Active: ${bot.active ? 'yes' : 'no'}`}</div>
                                {bot.status_message && <div>{bot.status_message}</div>}
                            </div>
                        ))}
                    </div>
                )}
                <div style={{display: 'flex', flexDirection: 'column', gap: '8px'}}>
                    <div style={{fontWeight: 600}}>{'Recent Active Sessions'}</div>
                    {sessionActionMessage && <div>{sessionActionMessage}</div>}
                    {loadingSessions && <div>{'Loading sessions...'}</div>}
                    {!loadingSessions && sessions.length === 0 && <div>{'No sessions recorded yet.'}</div>}
                    {!loadingSessions && sessions.map((session) => (
                        <div key={session.session_id} style={{border: '1px solid rgba(63, 67, 80, 0.1)', borderRadius: '8px', padding: '12px'}}>
                            <div style={{fontWeight: 600}}>{session.title || session.session_id}</div>
                            <div>{`${session.bot_name || session.bot_username || session.bot_id} / ${session.channel_name || session.channel_id}`}</div>
                            <div>{`${session.user_name || session.user_id}`}</div>
                            <div>{`Session: ${session.session_id}`}</div>
                            <div>{`Last used: ${formatTimestamp(session.last_used_at)}`}</div>
                            {session.expired && <div>{'Marked expired by current idle policy.'}</div>}
                            <div style={{display: 'flex', gap: '8px', marginTop: '8px'}}>
                                <button className='btn btn-tertiary' onClick={() => handleAbortSession(session)} type='button'>{'Abort Run'}</button>
                                <button className='btn btn-tertiary' onClick={() => handleResetSession(session)} type='button'>{'Reset Session'}</button>
                            </div>
                        </div>
                    ))}
                </div>
            </section>
        </div>
    );
}

function LabeledField(props: {label: string; children: React.ReactNode}) {
    return (
        <div style={{display: 'flex', flexDirection: 'column', gap: '6px'}}>
            <label style={{fontWeight: 600}}>{props.label}</label>
            {props.children}
        </div>
    );
}

function NumberField(props: {label: string; value: number; onChange: (value: number) => void}) {
    return (
        <LabeledField label={props.label}>
            <input
                onChange={(event) => props.onChange(Number(event.target.value) || 0)}
                style={fieldStyle}
                type='number'
                value={props.value}
            />
        </LabeledField>
    );
}

function TokenListField(props: {label: string; value: string[]; onChange: (items: string[]) => void}) {
    return (
        <LabeledField label={props.label}>
            <input
                onChange={(event) => props.onChange(splitCommaSeparated(event.target.value))}
                style={fieldStyle}
                value={props.value.join(', ')}
            />
        </LabeledField>
    );
}

function CatalogField(props: {
    label: string;
    value: string;
    options: string[];
    onChange: (value: string) => void;
    placeholder?: string;
    disabled?: boolean;
    emptyLabel?: string;
}) {
    const options = buildCatalogOptions(props.value, props.options);
    if (options.length === 0) {
        return (
            <LabeledField label={props.label}>
                <input
                    disabled={props.disabled}
                    onChange={(event) => props.onChange(event.target.value)}
                    placeholder={props.placeholder}
                    style={fieldStyle}
                    value={props.value}
                />
            </LabeledField>
        );
    }

    return (
        <LabeledField label={props.label}>
            <select
                disabled={props.disabled}
                onChange={(event) => props.onChange(event.target.value)}
                style={fieldStyle}
                value={props.value}
            >
                <option value=''>{props.emptyLabel || 'None'}</option>
                {options.map((option) => (
                    <option key={option} value={option}>
                        {option}
                    </option>
                ))}
            </select>
        </LabeledField>
    );
}

function createDefaultConfig(): DraftPluginConfig {
    return {
        service: {
            base_url: '',
            username: '',
            password: '',
            allow_hosts: '',
        },
        runtime: {
            default_timeout_seconds: 30,
            enable_streaming: true,
            streaming_update_ms: 350,
            max_input_length: 4000,
            max_output_length: 8000,
            context_post_limit: 8,
            enable_debug_logs: false,
            enable_usage_logs: true,
        },
        opencode_defaults: {
            provider_id: '',
            model_id: '',
            agent_id: '',
        },
        session_policy: {
            reuse_scope: 'thread',
            idle_expire_minutes: 120,
        },
        bots: [],
    };
}

function createEmptyBot(): DraftBotDefinition {
    return {
        local_id: createLocalID('bot'),
        id: '',
        username: '',
        display_name: '',
        description: '',
        mode: 'conversation',
        default_agent: '',
        default_model: '',
        system_prompt: '',
        tool_policy: '',
        include_context_by_default: true,
        allowed_teams: [],
        allowed_channels: [],
        allowed_users: [],
        input_schema: [],
        coding: createDefaultCodingSettings(),
    };
}

function cloneBot(bot: DraftBotDefinition): DraftBotDefinition {
    return {
        ...bot,
        local_id: createLocalID('bot'),
        id: '',
        username: bot.username ? `${bot.username}-copy` : '',
        display_name: bot.display_name ? `${bot.display_name} Copy` : '',
        input_schema: bot.input_schema.map((field) => ({...field, id: createLocalID('input')})),
        coding: {...bot.coding},
    };
}

function createDefaultCodingSettings(): Required<CodingBotSettings> {
    return {
        profile: 'implementer',
        workspace_root: '',
        workspace_label: '',
        default_branch: '',
        allowed_paths: [],
        command_allowlist: ['git status', 'git diff', 'go test', 'go build', 'npm test', 'npm run build'],
        require_command_approval: true,
        include_workspace_snapshot: true,
        include_referenced_files: true,
        max_referenced_files: 4,
        command_timeout_seconds: 180,
    };
}

function buildStoredConfig(config: DraftPluginConfig): AdminPluginConfig {
    return {
        service: {...config.service},
        runtime: {...config.runtime},
        opencode_defaults: {...config.opencode_defaults},
        session_policy: {...config.session_policy},
        bots: config.bots.map((bot) => ({
            id: bot.id.trim(),
            username: bot.username.trim(),
            display_name: bot.display_name.trim(),
            description: bot.description.trim(),
            mode: bot.mode,
            default_agent: bot.default_agent.trim(),
            default_model: bot.default_model.trim(),
            system_prompt: bot.system_prompt,
            tool_policy: bot.tool_policy.trim(),
            include_context_by_default: bot.include_context_by_default,
            allowed_teams: bot.allowed_teams,
            allowed_channels: bot.allowed_channels,
            allowed_users: bot.allowed_users,
            input_schema: bot.input_schema.map((field) => ({
                name: field.name?.trim() || '',
                label: field.label?.trim() || '',
                description: field.description?.trim(),
                type: field.type || 'text',
                required: Boolean(field.required),
                placeholder: field.placeholder?.trim(),
                default_value: field.default_value,
            })),
            coding: {
                profile: bot.coding.profile,
                workspace_root: bot.coding.workspace_root.trim(),
                workspace_label: bot.coding.workspace_label.trim(),
                default_branch: bot.coding.default_branch.trim(),
                allowed_paths: bot.coding.allowed_paths,
                command_allowlist: bot.coding.command_allowlist,
                require_command_approval: bot.coding.require_command_approval,
                include_workspace_snapshot: bot.coding.include_workspace_snapshot,
                include_referenced_files: bot.coding.include_referenced_files,
                max_referenced_files: bot.coding.max_referenced_files,
                command_timeout_seconds: bot.coding.command_timeout_seconds,
            },
        })),
    };
}

function normalizeAdminConfig(config: AdminPluginConfig): DraftPluginConfig {
    const defaults = createDefaultConfig();
    return {
        service: {...defaults.service, ...config.service},
        runtime: {...defaults.runtime, ...config.runtime},
        opencode_defaults: {...defaults.opencode_defaults, ...config.opencode_defaults},
        session_policy: {...defaults.session_policy, ...config.session_policy},
        bots: (config.bots || []).map(normalizeStoredBot),
    };
}

function normalizeStoredBot(bot: BotDefinition): DraftBotDefinition {
    return {
        local_id: createLocalID('bot'),
        id: stringValue(bot.id),
        username: stringValue(bot.username),
        display_name: stringValue(bot.display_name),
        description: stringValue(bot.description),
        mode: bot.mode === 'coding' ? 'coding' : 'conversation',
        default_agent: stringValue(bot.default_agent),
        default_model: stringValue(bot.default_model),
        system_prompt: stringValue(bot.system_prompt),
        tool_policy: stringValue(bot.tool_policy),
        include_context_by_default: Boolean(bot.include_context_by_default),
        allowed_teams: arrayValue(bot.allowed_teams),
        allowed_channels: arrayValue(bot.allowed_channels),
        allowed_users: arrayValue(bot.allowed_users),
        input_schema: (bot.input_schema || []).map((field) => ({...field, id: createLocalID('input')})),
        coding: normalizeCodingSettings(bot.coding),
    };
}

function normalizeCodingSettings(value?: CodingBotSettings) {
    const defaults = createDefaultCodingSettings();
    return {
        ...defaults,
        ...(value || {}),
        allowed_paths: arrayValue(value?.allowed_paths),
        command_allowlist: arrayValue(value?.command_allowlist),
        profile: stringValue(value?.profile) || defaults.profile,
        workspace_root: stringValue(value?.workspace_root),
        workspace_label: stringValue(value?.workspace_label),
        default_branch: stringValue(value?.default_branch),
        require_command_approval: value?.require_command_approval ?? defaults.require_command_approval,
        include_workspace_snapshot: value?.include_workspace_snapshot ?? defaults.include_workspace_snapshot,
        include_referenced_files: value?.include_referenced_files ?? defaults.include_referenced_files,
        max_referenced_files: numericValue(value?.max_referenced_files, defaults.max_referenced_files),
        command_timeout_seconds: numericValue(value?.command_timeout_seconds, defaults.command_timeout_seconds),
    };
}

function parseStoredConfigValue(value: unknown): {ok: boolean; config: DraftPluginConfig} {
    const defaults = createDefaultConfig();
    const raw = serializeSettingValue(value);
    if (!raw) {
        return {ok: true, config: defaults};
    }
    try {
        const parsed = JSON.parse(raw) as AdminPluginConfig;
        return {ok: true, config: normalizeAdminConfig(parsed)};
    } catch {
        return {ok: false, config: defaults};
    }
}

function serializeSettingValue(value: unknown) {
    if (typeof value === 'string') {
        return value.trim();
    }
    if (value === null || value === undefined) {
        return '';
    }
    try {
        return JSON.stringify(value);
    } catch {
        return '';
    }
}

function validateConfig(config: DraftPluginConfig) {
    const messages: string[] = [];
    if (!config.service.base_url.trim()) {
        messages.push('Service connection: base URL is required.');
    }
    const usernames = new Set<string>();
    config.bots.forEach((bot) => {
        const username = bot.username.trim().toLowerCase();
        if (!username) {
            messages.push('Each bot needs a username.');
            return;
        }
        if (usernames.has(username)) {
            messages.push(`Duplicate bot username: ${username}`);
        }
        usernames.add(username);
    });
    return messages;
}

function pickSelectedBotID(bots: DraftBotDefinition[], current: string) {
    if (current && bots.some((bot) => bot.local_id === current)) {
        return current;
    }
    return bots[0]?.local_id || '';
}

function splitCommaSeparated(value: string) {
    return value.split(',').map((item) => item.trim()).filter(Boolean);
}

function buildProviderOptions(connection: ConnectionStatus | null) {
    return uniqueStrings((connection?.providers || []).map((provider) => provider.id));
}

function buildModelOptions(connection: ConnectionStatus | null, providerID: string) {
    const normalizedProviderID = providerID.trim().toLowerCase();
    const models = (connection?.providers || []).flatMap((provider) => {
        const providerModels = [...(provider.models || []), provider.default_model || ''];
        if (!normalizedProviderID) {
            return providerModels;
        }
        if (provider.id.toLowerCase() === normalizedProviderID) {
            return providerModels;
        }
        return providerModels.filter((model) => model.toLowerCase().startsWith(`${normalizedProviderID}/`));
    });

    return uniqueStrings(models);
}

function buildAgentOptions(connection: ConnectionStatus | null) {
    return uniqueStrings((connection?.agents || []).map((agent) => agent.id));
}

function buildCatalogOptions(currentValue: string, options: string[]) {
    const normalized = uniqueStrings(options);
    if (currentValue.trim() && !normalized.includes(currentValue.trim())) {
        return [currentValue.trim(), ...normalized];
    }
    return normalized;
}

function uniqueStrings(items: string[]) {
    return Array.from(new Set(items.map((item) => item.trim()).filter(Boolean)));
}

function formatTimestamp(value?: number) {
    if (!value) {
        return 'n/a';
    }
    try {
        return new Date(value).toLocaleString();
    } catch {
        return 'n/a';
    }
}

function stringValue(value: unknown) {
    return typeof value === 'string' ? value : '';
}

function arrayValue(value: unknown) {
    return Array.isArray(value) ? value.map((item) => String(item).trim()).filter(Boolean) : [];
}

function numericValue(value: unknown, fallback: number) {
    return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}

function createLocalID(prefix: string) {
    return `${prefix}-${Math.random().toString(36).slice(2)}-${Date.now()}`;
}
