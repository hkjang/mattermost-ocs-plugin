import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {WebSocketMessage} from '@mattermost/client';

import PostText from './post_text';

import type {CodingTask} from '../client';
import {getCodingTask, runCodingTaskCommand} from '../client';
import {isOpenCodeAwaitingFirstChunk} from '../streaming';
import {parseThinkTaggedMessage} from '../think_parsing';

type PostUpdateData = {
    post_id?: string;
    next?: string;
    control?: string;
    reasoning?: string;
    tool_status?: string;
};

type Props = {
    post: any;
    websocketRegister: (postID: string, listenerID: string, listener: (msg: WebSocketMessage<PostUpdateData>) => void) => void;
    websocketUnregister: (postID: string, listenerID: string) => void;
};

const containerStyle: React.CSSProperties = {
    display: 'flex',
    flexDirection: 'column',
    gap: '8px',
};

const statusStyle: React.CSSProperties = {
    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
    fontSize: '12px',
    fontWeight: 600,
    letterSpacing: '0.01em',
};

const toolStatusStyle: React.CSSProperties = {
    alignSelf: 'flex-start',
    background: 'rgba(var(--center-channel-color-rgb), 0.06)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '999px',
    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
    fontSize: '12px',
    fontWeight: 600,
    padding: '4px 9px',
};

const precontentStyle: React.CSSProperties = {
    alignItems: 'center',
    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
    display: 'inline-flex',
    fontSize: '13px',
    gap: '8px',
};

const spinnerStyle: React.CSSProperties = {
    animation: 'opencode-stream-cursor-blink 700ms linear infinite',
    background: 'rgba(var(--center-channel-color-rgb), 0.16)',
    borderRadius: '999px',
    display: 'inline-block',
    height: '10px',
    width: '10px',
};

const reasoningCardStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '12px',
    overflow: 'hidden',
    transition: 'opacity 520ms ease, max-height 520ms ease, margin 520ms ease, padding 520ms ease',
};

const reasoningHeaderStyle: React.CSSProperties = {
    alignItems: 'center',
    color: 'rgba(var(--center-channel-color-rgb), 0.72)',
    display: 'flex',
    fontSize: '12px',
    fontWeight: 600,
    gap: '8px',
    justifyContent: 'space-between',
    letterSpacing: '0.01em',
};

const reasoningBodyStyle: React.CSSProperties = {
    fontSize: '13px',
    lineHeight: 1.5,
    marginTop: '8px',
    overflow: 'auto',
    whiteSpace: 'pre-wrap',
};

const reasoningButtonStyle: React.CSSProperties = {
    background: 'transparent',
    border: 'none',
    color: 'inherit',
    cursor: 'pointer',
    fontSize: '12px',
    fontWeight: 600,
    padding: 0,
};

const reasoningFadeDelayMS = 2400;
const reasoningFadeDurationMS = 520;
const codingCardStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '12px',
    display: 'flex',
    flexDirection: 'column',
    gap: '8px',
    padding: '12px 14px',
};

export default function OpenCodeBotPost(props: Props) {
    const [message, setMessage] = useState(getRenderableMessage(props.post));
    const [generating, setGenerating] = useState(isStreamingPost(props.post));
    const [precontent, setPrecontent] = useState(isOpenCodeAwaitingFirstChunk(props.post));
    const [reasoningOverride, setReasoningOverride] = useState(getPostReasoning(props.post));
    const [toolStatus, setToolStatus] = useState(getPostToolStatus(props.post));
    const [reasoningVisible, setReasoningVisible] = useState(false);
    const [reasoningFading, setReasoningFading] = useState(false);
    const [reasoningExpanded, setReasoningExpanded] = useState(false);
    const [codingTask, setCodingTask] = useState<CodingTask | null>(extractCodingTask(props.post));
    const [runningCommandID, setRunningCommandID] = useState('');
    const listenerID = useRef(`opencode-${Math.random().toString(36).slice(2)}`);
    const fadeTimerRef = useRef<number | null>(null);
    const hideTimerRef = useRef<number | null>(null);

    useEffect(() => {
        setMessage(getRenderableMessage(props.post));
        setGenerating(isStreamingPost(props.post));
        setPrecontent(isOpenCodeAwaitingFirstChunk(props.post));
        setReasoningOverride((current) => {
            const nextReasoning = getPostReasoning(props.post);
            if (nextReasoning || isStreamingPost(props.post)) {
                return nextReasoning;
            }
            return current;
        });
        setToolStatus(getPostToolStatus(props.post));
    }, [
        props.post.message,
        props.post.props?.opencode_streaming,
        props.post.props?.opencode_stream_status,
        props.post.props?.opencode_stream_placeholder,
        props.post.props?.opencode_reasoning,
        props.post.props?.opencode_tool_status,
        props.post.props?.opencode_coding_task,
        props.post.props?.opencode_task_id,
    ]);

    useEffect(() => {
        setCodingTask(extractCodingTask(props.post));
    }, [props.post.props?.opencode_coding_task, props.post.props?.opencode_task_id]);

    useEffect(() => {
        const taskID = getTaskID(props.post);
        if (!taskID) {
            return;
        }
        getCodingTask(taskID).then(setCodingTask).catch(() => undefined);
    }, [props.post.id, props.post.props?.opencode_task_id]);

    const renderableMessage = useMemo(() => parseThinkTaggedMessage(message), [message]);
    const reasoningMessage = reasoningOverride || renderableMessage.thinkMessage;
    const reasoningOpen = Boolean(renderableMessage.thinkOpen && !reasoningOverride);
    const hasVisibleResponse = renderableMessage.responseMessage.trim() !== '';
    const canAutoHideReasoning = hasVisibleResponse;

    useEffect(() => {
        return () => {
            clearReasoningTimers(fadeTimerRef, hideTimerRef);
        };
    }, []);

    useEffect(() => {
        clearReasoningTimers(fadeTimerRef, hideTimerRef);

        if (!reasoningMessage) {
            setReasoningVisible(false);
            setReasoningFading(false);
            setReasoningExpanded(false);
            return;
        }

        if (generating || reasoningOpen || reasoningExpanded || !canAutoHideReasoning) {
            setReasoningVisible(true);
            setReasoningFading(false);
            return;
        }

        setReasoningVisible(true);
        setReasoningFading(false);
        fadeTimerRef.current = window.setTimeout(() => {
            setReasoningFading(true);
            hideTimerRef.current = window.setTimeout(() => {
                setReasoningVisible(false);
                setReasoningFading(false);
            }, reasoningFadeDurationMS);
        }, reasoningFadeDelayMS);
    }, [
        canAutoHideReasoning,
        generating,
        reasoningMessage,
        reasoningOpen,
        reasoningExpanded,
    ]);

    const listener = useMemo(() => {
        return (msg: WebSocketMessage<PostUpdateData>) => {
            const data = msg?.data || {};
            if (data.post_id !== props.post.id) {
                return;
            }

            if (data.control === 'start') {
                setGenerating(true);
                setPrecontent(true);
                setMessage('');
                setReasoningOverride('');
                setToolStatus('');
                setReasoningVisible(false);
                setReasoningFading(false);
                setReasoningExpanded(false);
                return;
            }

            if (typeof data.reasoning === 'string') {
                setReasoningOverride(data.reasoning);
            }

            if (typeof data.tool_status === 'string') {
                setToolStatus(data.tool_status);
            }

            if (typeof data.next === 'string' && data.next !== '') {
                setGenerating(true);
                setPrecontent(false);
                setMessage(data.next);
            }

            if (data.control === 'end' || data.control === 'cancel') {
                setGenerating(false);
                setPrecontent(false);
                setToolStatus('');
            }
        };
    }, [props.post.id]);

    useEffect(() => {
        props.websocketRegister(props.post.id, listenerID.current, listener);
        return () => {
            props.websocketUnregister(props.post.id, listenerID.current);
        };
    }, [listener, props.post.id, props.websocketRegister, props.websocketUnregister]);

    return (
        <div
            data-testid='opencode-bot-post'
            style={containerStyle}
        >
            {precontent && (
                <span style={precontentStyle}>
                    <span style={spinnerStyle}/>
                    {'Preparing a reply...'}
                </span>
            )}
            {codingTask && (
                <div style={codingCardStyle}>
                    <strong>{codingTask.summary || 'Coding task'}</strong>
                    <div style={{fontSize: '12px', opacity: 0.85}}>
                        <div>{`Workspace: ${codingTask.workspace?.label || codingTask.workspace?.root || 'n/a'}`}</div>
                        <div>{`Branch: ${codingTask.workspace?.branch || codingTask.workspace?.default_branch || 'n/a'}`}</div>
                        <div>{`Status: ${codingTask.status}`}</div>
                    </div>
                    {(codingTask.workspace?.changed_files || []).length > 0 && (
                        <div style={{fontSize: '12px'}}>
                            <strong>{'Changed files'}</strong>
                            <div>{codingTask.workspace.changed_files?.join(', ')}</div>
                        </div>
                    )}
                    {codingTask.workspace?.diff_stat && (
                        <div style={{fontSize: '12px', whiteSpace: 'pre-wrap'}}>
                            <strong>{'Diff stat'}</strong>
                            <div>{codingTask.workspace.diff_stat}</div>
                        </div>
                    )}
                    {(codingTask.diffs || []).length > 0 && (
                        <div style={{fontSize: '12px'}}>
                            <strong>{'Session Diff'}</strong>
                            {(codingTask.diffs || []).slice(0, 8).map((item) => (
                                <div
                                    key={`${item.path}:${item.summary || ''}`}
                                    style={{marginTop: '4px'}}
                                >
                                    <div>{item.path}</div>
                                    {item.summary && <div style={{opacity: 0.85, whiteSpace: 'pre-wrap'}}>{item.summary}</div>}
                                </div>
                            ))}
                        </div>
                    )}
                    {(codingTask.commands || []).length > 0 && (
                        <div style={{display: 'flex', flexDirection: 'column', gap: '8px'}}>
                            <strong>{'Commands'}</strong>
                            {codingTask.commands?.map((command) => (
                                <div
                                    key={command.id}
                                    style={{border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)', borderRadius: '10px', padding: '10px'}}
                                >
                                    <div style={{fontSize: '12px', fontWeight: 600}}>{command.title || command.command}</div>
                                    <div style={{fontSize: '12px', whiteSpace: 'pre-wrap'}}>{command.command}</div>
                                    {command.reason && <div style={{fontSize: '12px', opacity: 0.85}}>{command.reason}</div>}
                                    <div style={{fontSize: '12px', opacity: 0.85}}>{`Status: ${command.status || 'pending'}`}</div>
                                    {command.test_summary && <div style={{fontSize: '12px'}}>{command.test_summary}</div>}
                                    {command.output_preview && <div style={{fontSize: '12px', whiteSpace: 'pre-wrap'}}>{command.output_preview}</div>}
                                    {(command.status === 'pending' || command.status === 'failed') && codingTask.id && (
                                        <button
                                            className='btn btn-tertiary'
                                            disabled={runningCommandID === command.id}
                                            onClick={async () => {
                                                setRunningCommandID(command.id);
                                                try {
                                                    setCodingTask(await runCodingTaskCommand({task_id: codingTask.id, command_id: command.id}));
                                                } finally {
                                                    setRunningCommandID('');
                                                }
                                            }}
                                            style={{marginTop: '8px'}}
                                            type='button'
                                        >
                                            {getCommandButtonLabel(runningCommandID === command.id, command.requires_approval)}
                                        </button>
                                    )}
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            )}
            {reasoningMessage && (
                <>
                    {reasoningVisible ? (
                        <div
                            style={{
                                ...reasoningCardStyle,
                                margin: reasoningFading ? '0' : '0',
                                maxHeight: reasoningFading ? 0 : 280,
                                opacity: reasoningFading ? 0 : 1,
                                padding: reasoningFading ? '0 14px' : '12px 14px',
                            }}
                        >
                            <div style={reasoningHeaderStyle}>
                                <span style={{alignItems: 'center', display: 'inline-flex', gap: '8px'}}>
                                    {generating && <span style={spinnerStyle}/>}
                                    {generating || reasoningOpen ? 'Reasoning...' : 'Reasoning'}
                                </span>
                            </div>
                            <div style={reasoningBodyStyle}>
                                {reasoningMessage}
                            </div>
                        </div>
                    ) : (
                        <button
                            onClick={() => {
                                clearReasoningTimers(fadeTimerRef, hideTimerRef);
                                setReasoningExpanded(true);
                                setReasoningFading(false);
                                setReasoningVisible(true);
                            }}
                            style={{...reasoningButtonStyle, alignSelf: 'flex-start'}}
                            type='button'
                        >
                            {'View reasoning'}
                        </button>
                    )}
                </>
            )}
            {(hasVisibleResponse || (generating && !precontent && !reasoningMessage)) && (
                <PostText
                    channelID={props.post.channel_id}
                    message={renderableMessage.responseMessage}
                    postID={props.post.id}
                    showCursor={generating && !precontent && hasVisibleResponse}
                />
            )}
            {toolStatus && generating && !precontent && (
                <span style={toolStatusStyle}>
                    {toolStatus}
                </span>
            )}
            {generating && !precontent && hasVisibleResponse && (
                <span style={statusStyle}>
                    {'Streaming reply...'}
                </span>
            )}
        </div>
    );
}

function extractCodingTask(post: any): CodingTask | null {
    const raw = post?.props?.opencode_coding_task;
    if (typeof raw !== 'string' || !raw.trim()) {
        return null;
    }
    try {
        return JSON.parse(raw) as CodingTask;
    } catch {
        return null;
    }
}

function getTaskID(post: any) {
    const taskID = post?.props?.opencode_task_id;
    return typeof taskID === 'string' ? taskID : '';
}

function getCommandButtonLabel(isRunning: boolean, requiresApproval?: boolean) {
    if (isRunning) {
        return 'Running...';
    }
    return requiresApproval ? 'Approve & Run' : 'Run Command';
}

function isStreamingPost(post: any) {
    return post?.props?.opencode_streaming === 'true' || post?.props?.opencode_stream_status === 'streaming';
}

function getPostReasoning(post: any) {
    const reasoning = post?.props?.opencode_reasoning;
    return typeof reasoning === 'string' ? reasoning : '';
}

function getPostToolStatus(post: any) {
    const status = post?.props?.opencode_tool_status;
    return typeof status === 'string' ? status : '';
}

function getRenderableMessage(post: any) {
    if (isOpenCodeAwaitingFirstChunk(post)) {
        return '';
    }

    return post?.message || '';
}

function clearReasoningTimers(
    fadeTimerRef: React.MutableRefObject<number | null>,
    hideTimerRef: React.MutableRefObject<number | null>,
) {
    if (fadeTimerRef.current) {
        window.clearTimeout(fadeTimerRef.current);
        fadeTimerRef.current = null;
    }
    if (hideTimerRef.current) {
        window.clearTimeout(hideTimerRef.current);
        hideTimerRef.current = null;
    }
}
