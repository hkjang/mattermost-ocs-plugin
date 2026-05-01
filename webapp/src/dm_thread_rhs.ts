import type {Store} from 'redux';

import type {WebSocketMessage} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

import {receivedPost} from 'mattermost-redux/actions/posts';

const dmChannelType = 'D';
const selectPostAction = 'SELECT_POST';

type PostedEventData = {
    post?: string | Post;
    channel_id?: string;
    channel_type?: string;
};

export function handlePostedOpenCodeDMThread(
    store: Store<GlobalState>,
    msg: WebSocketMessage<PostedEventData>,
) {
    const post = parsePostedEventPost(msg?.data);
    if (!post || !shouldOpenDMThreadForOpenCodeBotPost(store.getState(), post, msg?.data?.channel_type)) {
        return;
    }

    store.dispatch(receivedPost(post) as any);
    window.setTimeout(() => {
        store.dispatch(buildSelectPostAction(post) as any);
        focusThreadReplyBox();
    }, 0);
}

export function parsePostedEventPost(data?: PostedEventData): Post | null {
    const rawPost = data?.post;
    if (!rawPost) {
        return null;
    }

    if (typeof rawPost !== 'string') {
        return rawPost;
    }

    try {
        return JSON.parse(rawPost) as Post;
    } catch {
        return null;
    }
}

export function shouldOpenDMThreadForOpenCodeBotPost(
    state: GlobalState,
    post: Post,
    channelTypeFromEvent?: string,
) {
    if (!isOpenCodeBotPost(post) || !post.id || !post.channel_id || Boolean(post.delete_at)) {
        return false;
    }

    const entities = (state as any).entities || {};
    const currentUserID = entities.users?.currentUserId || '';
    if (currentUserID && post.user_id === currentUserID) {
        return false;
    }

    const currentChannelID = entities.channels?.currentChannelId || '';
    if (currentChannelID && currentChannelID !== post.channel_id) {
        return false;
    }

    const channel = entities.channels?.channels?.[post.channel_id];
    const channelType = channelTypeFromEvent || channel?.type || '';
    return channelType === dmChannelType;
}

export function isOpenCodeBotPost(post: Post) {
    return String(post.type) === 'custom_opencode_bot' || Boolean(post.props?.opencode_bot_id);
}

export function getThreadRootID(post: Pick<Post, 'id' | 'root_id'>) {
    return post.root_id || post.id;
}

export function buildSelectPostAction(post: Post) {
    return {
        type: selectPostAction,
        postId: getThreadRootID(post),
        channelId: post.channel_id,
        timestamp: Date.now(),
    };
}

export function shouldMoveThreadFocus(activeElement?: Element | null) {
    const hasDocument = typeof document !== 'undefined';
    const active = activeElement || (hasDocument ? document.activeElement : null);
    if (!active) {
        return true;
    }
    if (hasDocument && (active === document.body || active === document.documentElement)) {
        return true;
    }

    const element = active as HTMLElement;
    const tagName = element.tagName.toLowerCase();
    return !(
        tagName === 'input' ||
        tagName === 'select' ||
        tagName === 'textarea' ||
        tagName === 'button' ||
        element.isContentEditable
    );
}

function focusThreadReplyBox() {
    if (typeof document === 'undefined' || !shouldMoveThreadFocus()) {
        return;
    }

    window.setTimeout(focusFirstReplyCandidate, 80);
    window.setTimeout(focusFirstReplyCandidate, 240);
}

function focusFirstReplyCandidate() {
    if (typeof document === 'undefined' || !shouldMoveThreadFocus()) {
        return;
    }

    const selectors = [
        '#reply_textbox',
        'textarea#reply_textbox',
        '[data-testid="reply_textbox"]',
        '[data-testid="reply_textbox"] textarea',
        '[data-testid="rhs-thread-textbox"] textarea',
        '[data-testid="rhs-channel-textbox"] textarea',
        '[contenteditable="true"][data-testid*="reply"]',
        'textarea[aria-label*="Reply"]',
        'textarea[aria-label*="reply"]',
    ];

    for (const selector of selectors) {
        const element = document.querySelector<HTMLElement>(selector);
        if (!element || typeof element.focus !== 'function') {
            continue;
        }
        element.focus();
        break;
    }
}
