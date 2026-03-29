import {parseThinkTaggedMessage} from './think_parsing';

test('parseThinkTaggedMessage separates reasoning from visible response', () => {
    const parsed = parseThinkTaggedMessage('<think>Looking things up\nStep 2</think>\n\nFinal answer');

    expect(parsed.hasThinkTag).toBe(true);
    expect(parsed.thinkOpen).toBe(false);
    expect(parsed.thinkMessage).toBe('Looking things up\nStep 2');
    expect(parsed.responseMessage).toBe('Final answer');
});

test('parseThinkTaggedMessage keeps incomplete think blocks visible as reasoning only', () => {
    const parsed = parseThinkTaggedMessage('Before<think>Streaming reasoning');

    expect(parsed.hasThinkTag).toBe(true);
    expect(parsed.thinkOpen).toBe(true);
    expect(parsed.thinkMessage).toBe('Streaming reasoning');
    expect(parsed.responseMessage).toBe('Before');
});

test('parseThinkTaggedMessage preserves plain responses without think tags', () => {
    const parsed = parseThinkTaggedMessage('Plain answer only');

    expect(parsed.hasThinkTag).toBe(false);
    expect(parsed.thinkOpen).toBe(false);
    expect(parsed.thinkMessage).toBe('');
    expect(parsed.responseMessage).toBe('Plain answer only');
});

test('parseThinkTaggedMessage merges multiple think blocks and normalizes blank lines', () => {
    const parsed = parseThinkTaggedMessage('<think>first</think>\n\n<think>second</think>\n\n\nAnswer');

    expect(parsed.thinkMessage).toBe('first\n\nsecond');
    expect(parsed.responseMessage).toBe('Answer');
});
