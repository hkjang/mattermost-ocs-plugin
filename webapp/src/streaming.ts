import type {Store} from 'redux';

import type {WebSocketMessage} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

import {receivedPost} from 'mattermost-redux/actions/posts';

import type {PluginRegistry} from './types/mattermost-webapp';

type StreamingPostUpdateEventData = {
    post_id?: string;
    next?: string;
    control?: string;
    reasoning?: string;
    tool_status?: string;
};

export type {StreamingPostUpdateEventData};

export function buildPluginWebSocketEventName(pluginID: string, eventName: string) {
    return `custom_${pluginID}_${eventName}`;
}

export function isOpenCodeStreamingPost(post?: Post | null): post is Post {
    if (!post) {
        return false;
    }

    const props = post.props || {};
    return props.opencode_stream === 'true' || props.opencode_streaming === 'true';
}

export function isOpenCodeAwaitingFirstChunk(post?: Post | null) {
    if (!post) {
        return false;
    }

    const props = post.props || {};
    return isOpenCodeStreamingPost(post) && props.opencode_stream_placeholder === 'true';
}

export function buildStreamingPostUpdate(state: GlobalState, data?: StreamingPostUpdateEventData): Post | null {
    const postID = normalizeIdentifier(data?.post_id);
    if (!postID) {
        return null;
    }

    const existingPost = state.entities.posts.posts[postID];
    if (!isOpenCodeStreamingPost(existingPost)) {
        return null;
    }

    const hasNextMessage = typeof data?.next === 'string' && data.next.trim() !== '';
    const nextMessage = hasNextMessage ? data?.next || '' : existingPost.message;
    const nextReasoning = typeof data?.reasoning === 'string' ? data.reasoning : existingPost.props?.opencode_reasoning;
    const nextToolStatus = typeof data?.tool_status === 'string' ? data.tool_status : existingPost.props?.opencode_tool_status;
    const messageChanged = existingPost.message !== nextMessage;
    const reasoningChanged = existingPost.props?.opencode_reasoning !== nextReasoning;
    const toolStatusChanged = existingPost.props?.opencode_tool_status !== nextToolStatus;
    if (!messageChanged && !reasoningChanged && !toolStatusChanged) {
        return null;
    }

    return {
        ...existingPost,
        message: nextMessage,
        update_at: Date.now(),
        props: {
            ...existingPost.props,
            opencode_stream: 'true',
            opencode_streaming: 'true',
            opencode_stream_status: 'streaming',
            opencode_stream_placeholder: 'false',
            opencode_reasoning: nextReasoning || '',
            opencode_tool_status: nextToolStatus || '',
        },
    };
}

export function handleStreamingPostUpdateEvent(
    store: Store<GlobalState>,
    msg: WebSocketMessage<StreamingPostUpdateEventData>,
) {
    if (!msg?.data) {
        return;
    }

    const updatedPost = buildStreamingPostUpdate(store.getState(), msg.data);
    if (!updatedPost) {
        return;
    }

    store.dispatch(receivedPost(updatedPost) as any);
}

export function registerStreamingPostHandler(
    registry: Pick<PluginRegistry, 'registerWebSocketEventHandler'>,
    store: Store<GlobalState>,
    pluginID: string,
) {
    registry.registerWebSocketEventHandler(
        buildPluginWebSocketEventName(pluginID, 'postupdate'),
        (msg: WebSocketMessage<StreamingPostUpdateEventData>) => handleStreamingPostUpdateEvent(store, msg),
    );
}

function normalizeIdentifier(value?: string) {
    if (typeof value !== 'string') {
        return '';
    }

    return value.trim();
}
