export const TOOL_START_MARKER = '<!--OCS_TOOL_START-->';
export const TOOL_END_MARKER = '<!--OCS_TOOL_END-->';

export type MessageSegment =
    | {kind: 'text'; content: string}
    | {kind: 'tool'; content: string; toolName: string};

export function splitMessageSegments(message: string): MessageSegment[] {
    if (!message) {
        return [];
    }
    const segments: MessageSegment[] = [];
    let cursor = 0;

    while (cursor < message.length) {
        const startIdx = message.indexOf(TOOL_START_MARKER, cursor);
        if (startIdx === -1) {
            const remaining = message.slice(cursor);
            if (remaining.trim()) {
                segments.push({kind: 'text', content: remaining});
            }
            break;
        }

        if (startIdx > cursor) {
            const before = message.slice(cursor, startIdx);
            if (before.trim()) {
                segments.push({kind: 'text', content: before});
            }
        }

        const bodyStart = startIdx + TOOL_START_MARKER.length;
        const endIdx = message.indexOf(TOOL_END_MARKER, bodyStart);
        if (endIdx === -1) {
            const trailing = message.slice(bodyStart).trim();
            if (trailing) {
                segments.push({kind: 'tool', content: trailing, toolName: extractToolName(trailing)});
            }
            break;
        }

        const body = message.slice(bodyStart, endIdx).trim();
        if (body) {
            segments.push({kind: 'tool', content: body, toolName: extractToolName(body)});
        }
        cursor = endIdx + TOOL_END_MARKER.length;
    }

    return segments;
}

export function messageHasToolBlocks(message: string): boolean {
    return Boolean(message) && message.includes(TOOL_START_MARKER);
}

function extractToolName(content: string): string {
    const toolMatch = content.match(/^>\s*도구 호출:\s*`([^`]+)`/m);
    if (toolMatch && toolMatch[1]) {
        return toolMatch[1];
    }
    const fileMatch = content.match(/^>\s*파일(?::\s*(.+))?\s*$/m);
    if (fileMatch) {
        return fileMatch[1] ? `파일: ${fileMatch[1].trim()}` : '파일';
    }
    return 'tool';
}
