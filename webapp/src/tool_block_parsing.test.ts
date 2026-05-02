import {messageHasToolBlocks, splitMessageSegments, TOOL_END_MARKER, TOOL_START_MARKER} from './tool_block_parsing';

describe('splitMessageSegments', () => {
    test('splits a tool block out of surrounding text', () => {
        const message = [
            'Sure, let me check.',
            '',
            `${TOOL_START_MARKER}\n> 도구 호출: \`bash\`\n\n입력:\n\n\`\`\`console\nls\n\`\`\`\n${TOOL_END_MARKER}`,
            '',
            'Done — there are 3 files in the directory.',
        ].join('\n');

        const segments = splitMessageSegments(message);
        expect(segments).toHaveLength(3);
        expect(segments[0].kind).toBe('text');
        expect(segments[0].content.trim()).toBe('Sure, let me check.');
        expect(segments[1].kind).toBe('tool');
        if (segments[1].kind === 'tool') {
            expect(segments[1].toolName).toBe('bash');
            expect(segments[1].content).toContain('입력:');
        }
        expect(segments[2].kind).toBe('text');
        expect(segments[2].content.trim()).toBe('Done — there are 3 files in the directory.');
    });

    test('handles a tool block at the very start of the message', () => {
        const message = `${TOOL_START_MARKER}\n> 도구 호출: \`read\`\n${TOOL_END_MARKER}\n\nFinal answer.`;
        const segments = splitMessageSegments(message);
        expect(segments).toHaveLength(2);
        expect(segments[0].kind).toBe('tool');
        if (segments[0].kind === 'tool') {
            expect(segments[0].toolName).toBe('read');
        }
        expect(segments[1].kind).toBe('text');
    });

    test('falls back to a single text segment when there are no markers', () => {
        const segments = splitMessageSegments('Plain reply with no tools.');
        expect(segments).toEqual([{kind: 'text', content: 'Plain reply with no tools.'}]);
    });

    test('treats an unterminated tool block as a tool segment', () => {
        const message = `${TOOL_START_MARKER}\n> 도구 호출: \`bash\`\n\n입력:\n\n\`\`\`console\nls`;
        const segments = splitMessageSegments(message);
        expect(segments).toHaveLength(1);
        expect(segments[0].kind).toBe('tool');
    });

    test('extracts file labels for file blocks', () => {
        const message = `${TOOL_START_MARKER}\n> 파일: src/index.ts\n${TOOL_END_MARKER}`;
        const segments = splitMessageSegments(message);
        expect(segments).toHaveLength(1);
        expect(segments[0].kind).toBe('tool');
        if (segments[0].kind === 'tool') {
            expect(segments[0].toolName).toBe('파일: src/index.ts');
        }
    });
});

describe('messageHasToolBlocks', () => {
    test('detects a tool block', () => {
        expect(messageHasToolBlocks(`hello ${TOOL_START_MARKER} body ${TOOL_END_MARKER} world`)).toBe(true);
    });

    test('returns false for plain text', () => {
        expect(messageHasToolBlocks('plain text')).toBe(false);
    });

    test('returns false for an empty string', () => {
        expect(messageHasToolBlocks('')).toBe(false);
    });
});
