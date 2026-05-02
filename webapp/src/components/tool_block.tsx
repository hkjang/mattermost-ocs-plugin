import React, {useEffect, useRef, useState} from 'react';

import PostText from './post_text';
import {messageHasToolBlocks, splitMessageSegments} from '../tool_block_parsing';

export {messageHasToolBlocks, splitMessageSegments};
export type {MessageSegment} from '../tool_block_parsing';

const AUTO_COLLAPSE_DELAY_MS = 5000;

const containerStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '10px',
    display: 'flex',
    flexDirection: 'column',
    margin: '4px 0',
    overflow: 'hidden',
};

const headerButtonStyle: React.CSSProperties = {
    alignItems: 'center',
    background: 'transparent',
    border: 'none',
    color: 'inherit',
    cursor: 'pointer',
    display: 'flex',
    fontSize: '13px',
    fontWeight: 600,
    gap: '8px',
    padding: '8px 12px',
    textAlign: 'left',
    width: '100%',
};

const chevronStyle: React.CSSProperties = {
    display: 'inline-block',
    fontSize: '10px',
    opacity: 0.7,
    transition: 'transform 180ms ease',
    width: '10px',
};

const labelStyle: React.CSSProperties = {
    color: 'rgba(var(--center-channel-color-rgb), 0.85)',
    fontSize: '13px',
    fontWeight: 600,
};

const toolNameStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.08)',
    borderRadius: '4px',
    fontFamily: 'var(--font-family-monospace, monospace)',
    fontSize: '12px',
    padding: '1px 6px',
};

const bodyStyle: React.CSSProperties = {
    borderTop: '1px solid rgba(var(--center-channel-color-rgb), 0.10)',
    padding: '8px 12px 4px',
};

type CollapsibleToolBlockProps = {
    toolName: string;
    content: string;
    streaming: boolean;
    channelID: string;
    postID: string;
};

export function CollapsibleToolBlock(props: CollapsibleToolBlockProps) {
    const {toolName, content, streaming, channelID, postID} = props;
    const [expanded, setExpanded] = useState(true);
    const userToggledRef = useRef(false);
    const collapseTimerRef = useRef<number | null>(null);

    useEffect(() => {
        return () => {
            if (collapseTimerRef.current !== null) {
                window.clearTimeout(collapseTimerRef.current);
                collapseTimerRef.current = null;
            }
        };
    }, []);

    useEffect(() => {
        if (collapseTimerRef.current !== null) {
            window.clearTimeout(collapseTimerRef.current);
            collapseTimerRef.current = null;
        }
        if (userToggledRef.current) {
            return;
        }
        if (streaming) {
            setExpanded(true);
            return;
        }
        collapseTimerRef.current = window.setTimeout(() => {
            if (!userToggledRef.current) {
                setExpanded(false);
            }
        }, AUTO_COLLAPSE_DELAY_MS);
    }, [streaming]);

    const onToggle = () => {
        if (collapseTimerRef.current !== null) {
            window.clearTimeout(collapseTimerRef.current);
            collapseTimerRef.current = null;
        }
        userToggledRef.current = true;
        setExpanded((prev) => !prev);
    };

    return (
        <div
            data-testid='ocs-tool-block'
            style={containerStyle}
        >
            <button
                aria-expanded={expanded}
                onClick={onToggle}
                style={headerButtonStyle}
                type='button'
            >
                <span
                    aria-hidden='true'
                    style={{...chevronStyle, transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)'}}
                >
                    {'▶'}
                </span>
                <span style={labelStyle}>{'도구 호출'}</span>
                <span style={toolNameStyle}>{toolName}</span>
            </button>
            {expanded && (
                <div style={bodyStyle}>
                    <PostText
                        channelID={channelID}
                        message={content}
                        postID={postID}
                    />
                </div>
            )}
        </div>
    );
}
