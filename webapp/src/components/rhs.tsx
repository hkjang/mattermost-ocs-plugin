import React, {useEffect, useMemo, useState} from 'react';
import {useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import type {BotDefinition, BotInputField, BotRunResult, CodingSearchResult, CodingTask, CodingWorkspaceSnapshot, ExecutionRecord} from '../client';
import {getBots, getCodingWorkspace, getHistory, runBot, searchCodingWorkspace} from '../client';

const containerStyle: React.CSSProperties = {
    display: 'flex',
    flexDirection: 'column',
    gap: '16px',
    padding: '16px',
};

const cardStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '12px',
    padding: '12px',
};

const fieldStyle: React.CSSProperties = {
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)',
    borderRadius: '8px',
    padding: '10px 12px',
    width: '100%',
};

export default function RHSPane() {
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const selectedPostId = useSelector((state: GlobalState) => (state as any).views?.rhs?.selectedPostId as string | undefined);

    const [bots, setBots] = useState<BotDefinition[]>([]);
    const [history, setHistory] = useState<ExecutionRecord[]>([]);
    const [selectedBotId, setSelectedBotId] = useState('');
    const [prompt, setPrompt] = useState('');
    const [includeContext, setIncludeContext] = useState(true);
    const [inputs, setInputs] = useState<Record<string, unknown>>({});
    const [reuseSessionID, setReuseSessionID] = useState('');
    const [codingWorkspace, setCodingWorkspace] = useState<CodingWorkspaceSnapshot | null>(null);
    const [searchQuery, setSearchQuery] = useState('');
    const [searchResults, setSearchResults] = useState<CodingSearchResult[]>([]);
    const [searching, setSearching] = useState(false);
    const [loading, setLoading] = useState(true);
    const [submitting, setSubmitting] = useState(false);
    const [message, setMessage] = useState('');
    const [lastResult, setLastResult] = useState<BotRunResult | null>(null);

    const selectedBot = useMemo(
        () => bots.find((bot) => bot.id === selectedBotId) || bots[0],
        [bots, selectedBotId],
    );

    useEffect(() => {
        let cancelled = false;
        async function load() {
            setLoading(true);
            setMessage('');
            try {
                const [loadedBots, loadedHistory] = await Promise.all([
                    getBots(channelId),
                    getHistory(8),
                ]);
                if (cancelled) {
                    return;
                }
                setBots(loadedBots);
                setHistory(loadedHistory);
                if (loadedBots.length > 0) {
                    const initialBot = loadedBots[0];
                    setSelectedBotId(initialBot.id);
                    setIncludeContext(Boolean(initialBot.include_context_by_default));
                    setInputs(buildInitialInputs(initialBot.input_schema || []));
                    setReuseSessionID('');
                } else {
                    setSelectedBotId('');
                    setInputs({});
                    setReuseSessionID('');
                }
            } catch (error) {
                if (!cancelled) {
                    setMessage((error as Error).message);
                }
            } finally {
                if (!cancelled) {
                    setLoading(false);
                }
            }
        }
        load();
        return () => {
            cancelled = true;
        };
    }, [channelId]);

    useEffect(() => {
        if (!selectedBot) {
            return;
        }
        setIncludeContext(Boolean(selectedBot.include_context_by_default));
        setInputs(buildInitialInputs(selectedBot.input_schema || []));
        setSearchResults([]);
        setSearchQuery('');
        if (selectedBot.mode === 'coding') {
            getCodingWorkspace(selectedBot.id, channelId).then(setCodingWorkspace).catch(() => setCodingWorkspace(null));
        } else {
            setCodingWorkspace(null);
        }
    }, [selectedBotId, selectedBot, channelId]);

    async function submit() {
        if (!selectedBot || !channelId) {
            return;
        }
        setSubmitting(true);
        setMessage('');
        try {
            const result = await runBot({
                bot_id: selectedBot.id,
                channel_id: channelId,
                root_id: selectedPostId,
                prompt,
                include_context: includeContext,
                inputs,
                reuse_session_id: reuseSessionID || undefined,
            });
            setLastResult(result);
            setPrompt('');
            setReuseSessionID(result.session_id || reuseSessionID);
            setHistory(await getHistory(8));
            setMessage(`@${selectedBot.username} posted a ${selectedBot.mode === 'coding' ? 'coding task' : 'reply'} in Mattermost and will stream updates there when enabled.${result.session_id ? ` Active session: ${result.session_id}` : ''}${result.task_id ? ` Task: ${result.task_id}` : ''}`);
        } catch (error) {
            setMessage((error as Error).message);
        } finally {
            setSubmitting(false);
        }
    }

    function useHistoryItem(item: ExecutionRecord, withSession: boolean) {
        const historyBot = bots.find((bot) => bot.id === item.bot_id) || bots.find((bot) => bot.username === item.bot_username);
        if (historyBot) {
            setSelectedBotId(historyBot.id);
            setIncludeContext(Boolean(historyBot.include_context_by_default));
            setInputs(buildInitialInputs(historyBot.input_schema || []));
        }
        setPrompt(item.prompt_preview || '');
        setReuseSessionID(withSession ? (item.session_id || '') : '');
    }

    function handleBotSelection(botID: string) {
        setSelectedBotId(botID);
        setReuseSessionID('');
    }

    async function runSearch() {
        if (!selectedBot || selectedBot.mode !== 'coding' || !searchQuery.trim()) {
            return;
        }
        setSearching(true);
        try {
            setSearchResults(await searchCodingWorkspace(selectedBot.id, searchQuery.trim(), 12, channelId));
        } catch (error) {
            setMessage((error as Error).message);
        } finally {
            setSearching(false);
        }
    }

    return (
        <div style={containerStyle}>
            <section style={cardStyle}>
                <div style={{display: 'flex', flexDirection: 'column', gap: '8px'}}>
                    <strong>{selectedBot?.mode === 'coding' ? 'Coding Agent' : 'Ask OpenCode'}</strong>
                    <span style={{fontSize: '12px', opacity: 0.8}}>
                        {selectedBot?.mode === 'coding' ?
                            'Choose a coding bot, work against the current OpenCode project, and continue the same session as you iterate.' :
                            'Choose a bot, send a prompt, and the selected bot will keep the same session for the current thread or DM according to the plugin policy.'}
                    </span>
                    {loading && <span>{'Loading bots...'}</span>}
                    {!loading && bots.length === 0 && <span>{'No OpenCode bots are available in this channel.'}</span>}
                    {!loading && bots.length > 0 && (
                        <>
                            <select
                                value={selectedBot?.id || ''}
                                onChange={(event) => handleBotSelection(event.target.value)}
                                style={fieldStyle}
                            >
                                {bots.map((bot) => (
                                    <option
                                        key={bot.id}
                                        value={bot.id}
                                    >
                                        {`${bot.display_name || bot.username} (@${bot.username})`}
                                    </option>
                                ))}
                            </select>
                            <div style={{fontSize: '12px', opacity: 0.8}}>
                                {`Default target: ${selectedBot?.default_agent || 'server default'} / ${selectedBot?.default_model || 'server default'}`}
                            </div>
                            {selectedBot?.mode === 'coding' && codingWorkspace && (
                                <div style={{fontSize: '12px', opacity: 0.85}}>
                                    <div>{`Workspace: ${codingWorkspace.label || codingWorkspace.root || 'n/a'}`}</div>
                                    <div>{`Branch: ${codingWorkspace.branch || codingWorkspace.default_branch || 'n/a'}`}</div>
                                    <div>{`Status: ${codingWorkspace.status_summary || (codingWorkspace.dirty ? 'dirty' : 'clean')}`}</div>
                                </div>
                            )}
                            {selectedBot?.description && (
                                <span style={{opacity: 0.8}}>{selectedBot.description}</span>
                            )}
                            <textarea
                                value={prompt}
                                onChange={(event) => setPrompt(event.target.value)}
                                placeholder={selectedBot ? `Message @${selectedBot.username}...` : 'Ask OpenCode something...'}
                                rows={6}
                                style={{...fieldStyle, resize: 'vertical'}}
                            />
                            {reuseSessionID && (
                                <div style={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '12px', fontSize: '12px', opacity: 0.9}}>
                                    <span>{`Continuing session: ${reuseSessionID}`}</span>
                                    <button
                                        className='btn btn-tertiary'
                                        onClick={() => setReuseSessionID('')}
                                        type='button'
                                    >
                                        {'Start Fresh'}
                                    </button>
                                </div>
                            )}
                            {(selectedBot?.input_schema || []).map((field) => renderField(field, inputs, setInputs))}
                            <label style={{display: 'flex', gap: '8px', alignItems: 'center'}}>
                                <input
                                    type='checkbox'
                                    checked={includeContext}
                                    onChange={(event) => setIncludeContext(event.target.checked)}
                                />
                                {'Include recent thread or channel context'}
                            </label>
                            <button
                                className='btn btn-primary'
                                disabled={submitting || !prompt.trim()}
                                onClick={submit}
                                type='button'
                            >
                                {submitting ? 'Sending...' : selectedBot?.mode === 'coding' ? `Start coding task as @${selectedBot?.username || 'bot'}` : `Send as @${selectedBot?.username || 'bot'}`}
                            </button>
                        </>
                    )}
                    {message && <span>{message}</span>}
                </div>
            </section>

            {selectedBot?.mode === 'coding' && (
                <section style={cardStyle}>
                    <strong>{'Workspace Search'}</strong>
                    <div style={{display: 'flex', flexDirection: 'column', gap: '8px', marginTop: '8px'}}>
                        <input
                            onChange={(event) => setSearchQuery(event.target.value)}
                            placeholder={'Search code, symbols, or filenames'}
                            style={fieldStyle}
                            value={searchQuery}
                        />
                        <button className='btn btn-tertiary' disabled={searching || !searchQuery.trim()} onClick={runSearch} type='button'>
                            {searching ? 'Searching...' : 'Search Workspace'}
                        </button>
                        {searchResults.map((item) => (
                            <div key={`${item.path}:${item.line}`} style={{fontSize: '12px'}}>
                                <strong>{item.path}</strong>
                                <div>{`${item.kind || 'match'}${item.line ? ` - line ${item.line}` : ''}`}</div>
                                <div style={{whiteSpace: 'pre-wrap'}}>{item.preview}</div>
                            </div>
                        ))}
                    </div>
                </section>
            )}

            {lastResult && (
                <section style={cardStyle}>
                    <strong>{'Latest Result'}</strong>
                    <div>{`${lastResult.bot_name || lastResult.bot_username} - ${lastResult.status}`}</div>
                    {lastResult.bot_mode && <div>{`Mode: ${lastResult.bot_mode}`}</div>}
                    {lastResult.session_id && <div>{`Session: ${lastResult.session_id}`}</div>}
                    {lastResult.task_id && <div>{`Task: ${lastResult.task_id}`}</div>}
                    {(lastResult.agent_id || lastResult.model_id) && <div>{`Target: ${lastResult.agent_id || 'server default'} / ${lastResult.model_id || 'server default'}`}</div>}
                    {lastResult.error_message && <div style={{whiteSpace: 'pre-wrap'}}>{lastResult.error_message}</div>}
                    {lastResult.error_code && <div>{`Code: ${lastResult.error_code}`}</div>}
                    {lastResult.retryable !== undefined && <div>{`Retryable: ${lastResult.retryable ? 'Yes' : 'No'}`}</div>}

                </section>
            )}

            <section style={cardStyle}>
                <strong>{'Recent Sessions'}</strong>
                <div style={{display: 'flex', flexDirection: 'column', gap: '8px', marginTop: '8px'}}>
                    {history.length === 0 && <span>{'No executions yet.'}</span>}
                    {history.map((item) => (
                        <div
                            key={`${item.session_id || item.bot_id}-${item.started_at}`}
                            style={{fontSize: '12px'}}
                        >
                            <strong>{item.bot_name || item.bot_username}</strong>
                            {item.bot_mode && <div>{`Mode: ${item.bot_mode}`}</div>}
                            <div>{`Session: ${item.session_id || 'n/a'}`}</div>
                            {item.task_id && <div>{`Task: ${item.task_id}`}</div>}
                            <div>{`${item.status} via ${item.source}`}</div>
                            {(item.agent_id || item.model_id) && <div>{`${item.agent_id || 'server default'} / ${item.model_id || 'server default'}`}</div>}
                            {item.prompt_preview && <div style={{whiteSpace: 'pre-wrap'}}>{item.prompt_preview}</div>}
                            {item.error_message && <div style={{whiteSpace: 'pre-wrap'}}>{item.error_message}</div>}
                            <div style={{display: 'flex', gap: '8px', marginTop: '6px'}}>
                                <button
                                    className='btn btn-tertiary'
                                    disabled={!item.session_id}
                                    onClick={() => useHistoryItem(item, true)}
                                    type='button'
                                >
                                    {'Continue This Session'}
                                </button>
                                <button
                                    className='btn btn-tertiary'
                                    onClick={() => useHistoryItem(item, false)}
                                    type='button'
                                >
                                    {'Use Prompt Only'}
                                </button>
                            </div>
                        </div>
                    ))}
                </div>
            </section>
        </div>
    );
}

function buildInitialInputs(fields: BotInputField[]) {
    return fields.reduce<Record<string, unknown>>((acc, field) => {
        if (field.default_value !== undefined) {
            acc[field.name] = field.default_value;
        } else if (field.type === 'bool') {
            acc[field.name] = false;
        } else {
            acc[field.name] = '';
        }
        return acc;
    }, {});
}

function renderField(
    field: BotInputField,
    inputs: Record<string, unknown>,
    setInputs: React.Dispatch<React.SetStateAction<Record<string, unknown>>>,
) {
    const currentValue = inputs[field.name];
    const onChange = (value: unknown) => setInputs((current) => ({...current, [field.name]: value}));
    let control: React.ReactNode;

    if (field.type === 'bool') {
        control = (
            <label style={{display: 'flex', gap: '8px', alignItems: 'center'}}>
                <input
                    checked={Boolean(currentValue)}
                    onChange={(event) => onChange(event.target.checked)}
                    type='checkbox'
                />
                {field.placeholder || 'Enabled'}
            </label>
        );
    } else if (field.type === 'textarea') {
        control = (
            <textarea
                rows={4}
                style={{...fieldStyle, resize: 'vertical'}}
                value={String(currentValue ?? '')}
                onChange={(event) => onChange(event.target.value)}
                placeholder={field.placeholder}
            />
        );
    } else {
        control = (
            <input
                style={fieldStyle}
                type={field.type === 'number' ? 'number' : 'text'}
                value={String(currentValue ?? '')}
                onChange={(event) => onChange(field.type === 'number' ? Number(event.target.value) : event.target.value)}
                placeholder={field.placeholder}
            />
        );
    }

    return (
        <div
            key={field.name}
            style={{display: 'flex', flexDirection: 'column', gap: '6px'}}
        >
            <label style={{fontWeight: 600}}>{field.label || field.name}</label>
            {field.description && <span style={{fontSize: '12px', opacity: 0.8}}>{field.description}</span>}
            {control}
        </div>
    );
}
