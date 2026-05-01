import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

jest.mock('mattermost-redux/actions/posts', () => ({
    receivedPost: jest.fn((post) => ({
        type: 'RECEIVED_POST',
        data: post,
        features: {
            crtEnabled: undefined,
        },
    })),
}), {virtual: true});

import {buildPluginWebSocketEventName, buildStreamingPostUpdate, isOpenCodeAwaitingFirstChunk} from './streaming';

function makeState(post: Post) {
    return {
        entities: {
            posts: {
                posts: {
                    [post.id]: post,
                },
            },
        },
    } as GlobalState;
}

function makePost(overrides: Partial<Post> = {}) {
    return {
        id: 'post-id',
        channel_id: 'channel-id',
        create_at: 1,
        delete_at: 0,
        edit_at: 0,
        file_ids: [],
        hashtags: '',
        is_pinned: false,
        message: 'initial',
        metadata: {},
        original_id: '',
        pending_post_id: '',
        props: {
            opencode_stream: 'true',
            opencode_streaming: 'true',
        },
        root_id: '',
        type: '',
        update_at: 1,
        user_id: 'bot-user-id',
        ...overrides,
    } as Post;
}

test('buildPluginWebSocketEventName prefixes plugin events correctly', () => {
    expect(buildPluginWebSocketEventName('com.mattermost.ocs-plugin', 'postupdate')).toBe('custom_com.mattermost.ocs-plugin_postupdate');
});

test('buildStreamingPostUpdate updates only streaming OpenCode posts', () => {
    const post = makePost();

    const updatedPost = buildStreamingPostUpdate(makeState(post), {
        post_id: post.id,
        next: 'streamed reply',
        reasoning: 'thinking through it',
        tool_status: 'Running read',
    });

    expect(updatedPost).toBeTruthy();
    expect(updatedPost?.message).toBe('streamed reply');
    expect(updatedPost?.props?.opencode_stream_status).toBe('streaming');
    expect(updatedPost?.props?.opencode_reasoning).toBe('thinking through it');
    expect(updatedPost?.props?.opencode_tool_status).toBe('Running read');
});

test('buildStreamingPostUpdate can update reasoning without changing text', () => {
    const post = makePost({
        props: {
            opencode_stream: 'true',
            opencode_streaming: 'true',
            opencode_reasoning: '',
            opencode_tool_status: '',
        },
    });

    const updatedPost = buildStreamingPostUpdate(makeState(post), {
        post_id: post.id,
        reasoning: 'reasoning chunk',
    });

    expect(updatedPost).toBeTruthy();
    expect(updatedPost?.message).toBe('initial');
    expect(updatedPost?.props?.opencode_reasoning).toBe('reasoning chunk');
});

test('buildStreamingPostUpdate ignores non-streaming posts', () => {
    const post = makePost({
        props: {},
    });

    const updatedPost = buildStreamingPostUpdate(makeState(post), {
        post_id: post.id,
        next: 'streamed reply',
    });

    expect(updatedPost).toBeNull();
});

test('isOpenCodeAwaitingFirstChunk detects placeholder streaming posts', () => {
    const post = makePost({
        props: {
            opencode_stream: 'true',
            opencode_streaming: 'true',
            opencode_stream_placeholder: 'true',
        },
    });

    expect(isOpenCodeAwaitingFirstChunk(post)).toBe(true);
});
