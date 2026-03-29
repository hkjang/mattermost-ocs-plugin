import React, {useEffect, useRef, useState} from 'react';

import {renderMermaidDefinition} from '../mermaid_rendering';

type Props = {
    definition: string;
    postID: string;
    index: number;
};

export default function MermaidDiagram({definition, postID, index}: Props) {
    const containerRef = useRef<HTMLDivElement | null>(null);
    const popupContainerRef = useRef<HTMLDivElement | null>(null);
    const copyResetTimerRef = useRef<number | null>(null);
    const [error, setError] = useState('');
    const [popupError, setPopupError] = useState('');
    const [copied, setCopied] = useState(false);
    const [showSource, setShowSource] = useState(false);
    const [showRenderedPopup, setShowRenderedPopup] = useState(false);

    useEffect(() => {
        return renderIntoContainer({
            containerRef,
            definition,
            postID,
            index,
            setError,
            variant: 'inline',
        });
    }, [definition, index, postID]);

    useEffect(() => {
        if (!showRenderedPopup) {
            setPopupError('');
            if (popupContainerRef.current) {
                popupContainerRef.current.innerHTML = '';
            }
            return () => undefined;
        }

        return renderIntoContainer({
            containerRef: popupContainerRef,
            definition,
            postID,
            index,
            setError: setPopupError,
            variant: 'popup',
        });
    }, [definition, index, postID, showRenderedPopup]);

    useEffect(() => {
        return () => {
            if (copyResetTimerRef.current) {
                window.clearTimeout(copyResetTimerRef.current);
            }
        };
    }, []);

    const handleCopy = async () => {
        const copySucceeded = await copyText(definition);
        if (!copySucceeded) {
            setError((currentError) => currentError || 'Failed to copy the Mermaid source.');
            return;
        }

        setCopied(true);
        if (copyResetTimerRef.current) {
            window.clearTimeout(copyResetTimerRef.current);
        }
        copyResetTimerRef.current = window.setTimeout(() => {
            setCopied(false);
        }, 1600);
    };

    return (
        <>
            <div className='opencode-mermaid-card'>
                <div className='opencode-mermaid-toolbar'>
                    <button
                        className='opencode-mermaid-toolbar-button'
                        onClick={handleCopy}
                        type='button'
                    >
                        {copied ? 'Copied' : 'Copy'}
                    </button>
                    <button
                        className='opencode-mermaid-toolbar-button'
                        onClick={() => setShowSource(true)}
                        type='button'
                    >
                        {'View Source'}
                    </button>
                    <button
                        className='opencode-mermaid-toolbar-button'
                        onClick={() => setShowRenderedPopup(true)}
                        type='button'
                    >
                        {'Open Preview'}
                    </button>
                </div>
                {error && (
                    <div className='opencode-mermaid-error'>
                        {`Mermaid ?åÎçîÎß??§Ìå®: ${error}`}
                    </div>
                )}
                {error ? (
                    <div className='opencode-mermaid-fallback'>
                        <pre className='post-code'>
                            <code className='language-mermaid'>{definition}</code>
                        </pre>
                    </div>
                ) : (
                    <div className='opencode-mermaid-rendered'>
                        <div
                            data-testid='opencode-mermaid-diagram'
                            ref={containerRef}
                        />
                    </div>
                )}
            </div>
            {showRenderedPopup && (
                <div
                    className='opencode-mermaid-modal-backdrop'
                    onClick={() => setShowRenderedPopup(false)}
                    role='presentation'
                >
                    <div
                        className='opencode-mermaid-modal opencode-mermaid-render-modal'
                        onClick={(event) => event.stopPropagation()}
                        role='dialog'
                    >
                        <div className='opencode-mermaid-modal-header'>
                            <strong>{'Mermaid Preview'}</strong>
                            <div className='opencode-mermaid-modal-actions'>
                                <button
                                    className='opencode-mermaid-toolbar-button'
                                    onClick={handleCopy}
                                    type='button'
                                >
                                    {copied ? 'Copied' : 'Copy'}
                                </button>
                                <button
                                    className='opencode-mermaid-toolbar-button'
                                    onClick={() => setShowRenderedPopup(false)}
                                    type='button'
                                >
                                    {'Close'}
                                </button>
                            </div>
                        </div>
                        {popupError && (
                            <div className='opencode-mermaid-error opencode-mermaid-modal-error'>
                                {`Mermaid ?åÎçîÎß??§Ìå®: ${popupError}`}
                            </div>
                        )}
                        <div className='opencode-mermaid-modal-content'>
                            {popupError ? (
                                <pre className='post-code opencode-mermaid-source'>
                                    <code className='language-mermaid'>{definition}</code>
                                </pre>
                            ) : (
                                <div
                                    className='opencode-mermaid-rendered opencode-mermaid-rendered-popup'
                                    data-testid='opencode-mermaid-diagram-popup'
                                    ref={popupContainerRef}
                                />
                            )}
                        </div>
                    </div>
                </div>
            )}
            {showSource && (
                <div
                    className='opencode-mermaid-modal-backdrop'
                    onClick={() => setShowSource(false)}
                    role='presentation'
                >
                    <div
                        className='opencode-mermaid-modal'
                        onClick={(event) => event.stopPropagation()}
                        role='dialog'
                    >
                        <div className='opencode-mermaid-modal-header'>
                            <strong>{'Mermaid Source'}</strong>
                            <div className='opencode-mermaid-modal-actions'>
                                <button
                                    className='opencode-mermaid-toolbar-button'
                                    onClick={handleCopy}
                                    type='button'
                                >
                                    {copied ? 'Copied' : 'Copy'}
                                </button>
                                <button
                                    className='opencode-mermaid-toolbar-button'
                                    onClick={() => setShowSource(false)}
                                    type='button'
                                >
                                    {'Close'}
                                </button>
                            </div>
                        </div>
                        <pre className='post-code opencode-mermaid-source'>
                            <code className='language-mermaid'>{definition}</code>
                        </pre>
                    </div>
                </div>
            )}
        </>
    );
}

async function copyText(value: string) {
    try {
        if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(value);
            return true;
        }
    } catch {
        return legacyCopy(value);
    }

    return legacyCopy(value);
}

function legacyCopy(value: string) {
    if (typeof document === 'undefined') {
        return false;
    }

    const textarea = document.createElement('textarea');
    textarea.value = value;
    textarea.setAttribute('readonly', 'true');
    textarea.style.opacity = '0';
    textarea.style.position = 'fixed';
    textarea.style.pointerEvents = 'none';
    document.body.appendChild(textarea);
    textarea.select();

    try {
        return document.execCommand('copy');
    } catch {
        return false;
    } finally {
        document.body.removeChild(textarea);
    }
}

type RenderIntoContainerOptions = {
    containerRef: React.RefObject<HTMLDivElement>;
    definition: string;
    postID: string;
    index: number;
    setError: React.Dispatch<React.SetStateAction<string>>;
    variant: string;
};

function renderIntoContainer({
    containerRef,
    definition,
    postID,
    index,
    setError,
    variant,
}: RenderIntoContainerOptions) {
    const container = containerRef.current;
    if (!container) {
        return () => undefined;
    }

    let cancelled = false;
    container.innerHTML = '';
    setError('');

    renderMermaidDefinition(definition, postID, index, variant).then(({svg, bindFunctions}) => {
        if (cancelled || !containerRef.current) {
            return;
        }
        containerRef.current.innerHTML = svg;
        bindFunctions?.(containerRef.current);
    }).catch((renderError: unknown) => {
        if (cancelled) {
            return;
        }
        const message = renderError instanceof Error ? renderError.message : String(renderError);
        setError(message);
    });

    return () => {
        cancelled = true;
        if (containerRef.current) {
            containerRef.current.innerHTML = '';
        }
    };
}
