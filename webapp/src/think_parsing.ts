export type ThinkTaggedMessage = {
    rawMessage: string;
    responseMessage: string;
    thinkMessage: string;
    hasThinkTag: boolean;
    thinkOpen: boolean;
};

const thinkTagPattern = /<\/?think>/gi;

export function parseThinkTaggedMessage(message: string): ThinkTaggedMessage {
    const rawMessage = typeof message === 'string' ? message : '';
    if (!rawMessage) {
        return {
            rawMessage: '',
            responseMessage: '',
            thinkMessage: '',
            hasThinkTag: false,
            thinkOpen: false,
        };
    }

    const responseParts: string[] = [];
    const thinkParts: string[] = [];
    let hasThinkTag = false;
    let thinkOpen = false;
    let cursor = 0;

    const tagPattern = new RegExp(thinkTagPattern);
    let match = tagPattern.exec(rawMessage);
    for (; match; match = tagPattern.exec(rawMessage)) {
        const index = match.index ?? 0;
        const chunk = rawMessage.slice(cursor, index);
        if (thinkOpen) {
            thinkParts.push(chunk);
        } else {
            responseParts.push(chunk);
        }

        const tag = match[0].toLowerCase();
        if (tag === '<think>') {
            hasThinkTag = true;
            thinkOpen = true;
        } else {
            thinkOpen = false;
        }

        cursor = index + match[0].length;
    }

    const trailingChunk = rawMessage.slice(cursor);
    if (thinkOpen) {
        thinkParts.push(trailingChunk);
    } else {
        responseParts.push(trailingChunk);
    }

    if (!hasThinkTag) {
        return {
            rawMessage,
            responseMessage: rawMessage,
            thinkMessage: '',
            hasThinkTag: false,
            thinkOpen: false,
        };
    }

    return {
        rawMessage,
        responseMessage: normalizeVisibleMessage(responseParts.join('')),
        thinkMessage: normalizeThinkingMessage(thinkParts.filter(Boolean).join('\n\n')),
        hasThinkTag: true,
        thinkOpen,
    };
}

function normalizeVisibleMessage(value: string) {
    return value
        .replace(/\n{3,}/g, '\n\n')
        .replace(/^[\t ]*\n+/, '')
        .replace(/\n+[\t ]*$/, '');
}

function normalizeThinkingMessage(value: string) {
    return value
        .replace(/\n{3,}/g, '\n\n')
        .replace(/^[\t ]*\n+/, '')
        .replace(/\n+[\t ]*$/, '');
}
