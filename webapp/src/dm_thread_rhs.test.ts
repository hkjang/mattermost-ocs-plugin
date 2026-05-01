import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

jest.mock('mattermost-redux/actions/posts', () => ({
    receivedPost: jest.fn((post) => ({
        type: 'RECEIVED_POST',
        data: post,
    })),
}), {virtual: true});

import {
    buildSelectPostAction,
    getThreadRootID,
    isOpenCodeBotPost,
    parsePostedEventPost,
    shouldMoveThreadFocus,
    shouldOpenDMThreadForOpenCodeBotPost,
} from './dm_thread_rhs';

function makePost(overrides: Partial<Post> = {}) {
    return {
        id: 'post-id',
        channel_id: 'dm-channel-id',
        create_at: 1,
        delete_at: 0,
        edit_at: 0,
        file_ids: [],
        hashtags: '',
        is_pinned: false,
        message: 'reply',
        metadata: {},
        original_id: '',
        pending_post_id: '',
        props: {
            opencode_bot_id: 'assistant',
        },
        root_id: 'root-post-id',
        type: 'custom_opencode_bot',
        update_at: 1,
        user_id: 'bot-user-id',
        ...overrides,
    } as Post;
}

function makeState(overrides: Record<string, unknown> = {}) {
    return {
        entities: {
            channels: {
                currentChannelId: 'dm-channel-id',
                channels: {
                    'dm-channel-id': {
                        id: 'dm-channel-id',
                        type: 'D',
                    },
                },
            },
            users: {
                currentUserId: 'current-user-id',
            },
        },
        ...overrides,
    } as unknown as GlobalState;
}

test('parsePostedEventPost accepts Mattermost posted JSON payloads', () => {
    const post = makePost();

    expect(parsePostedEventPost({post: JSON.stringify(post)})).toMatchObject({
        id: post.id,
        root_id: post.root_id,
    });
});

test('shouldOpenDMThreadForOpenCodeBotPost gates on OpenCode bot DM posts in current channel', () => {
    const post = makePost();

    expect(shouldOpenDMThreadForOpenCodeBotPost(makeState(), post, 'D')).toBe(true);
    expect(shouldOpenDMThreadForOpenCodeBotPost(makeState(), {...post, type: ''} as Post, 'D')).toBe(true);
    expect(shouldOpenDMThreadForOpenCodeBotPost(makeState(), {...post, props: {}} as Post, 'D')).toBe(true);
    expect(shouldOpenDMThreadForOpenCodeBotPost(makeState(), {...post, type: '', props: {}} as Post, 'D')).toBe(false);
    expect(shouldOpenDMThreadForOpenCodeBotPost(makeState(), post, 'O')).toBe(false);
    expect(shouldOpenDMThreadForOpenCodeBotPost(makeState(), {...post, user_id: 'current-user-id'} as Post, 'D')).toBe(false);
});

test('shouldOpenDMThreadForOpenCodeBotPost avoids opening from another active channel', () => {
    const state = makeState({
        entities: {
            channels: {
                currentChannelId: 'other-channel-id',
                channels: {
                    'dm-channel-id': {
                        id: 'dm-channel-id',
                        type: 'D',
                    },
                },
            },
            users: {
                currentUserId: 'current-user-id',
            },
        },
    });

    expect(shouldOpenDMThreadForOpenCodeBotPost(state, makePost(), 'D')).toBe(false);
});

test('buildSelectPostAction opens the thread root', () => {
    const post = makePost();

    expect(getThreadRootID(post)).toBe('root-post-id');
    expect(buildSelectPostAction(post)).toMatchObject({
        type: 'SELECT_POST',
        postId: 'root-post-id',
        channelId: 'dm-channel-id',
    });
});

test('shouldMoveThreadFocus does not steal focus from active editors', () => {
    const input = {tagName: 'INPUT', isContentEditable: false} as HTMLElement;
    const neutral = {tagName: 'DIV', isContentEditable: false} as HTMLElement;

    expect(shouldMoveThreadFocus(input)).toBe(false);
    expect(shouldMoveThreadFocus(neutral)).toBe(true);
});

test('isOpenCodeBotPost accepts custom type or bot props', () => {
    expect(isOpenCodeBotPost(makePost())).toBe(true);
    expect(isOpenCodeBotPost(makePost({type: '', props: {opencode_bot_id: 'assistant'}}))).toBe(true);
    expect(isOpenCodeBotPost(makePost({type: '', props: {}}))).toBe(false);
});
