import { useEffect, useRef, useState } from 'react';
import type { UseChatSession } from '../hooks/useChatSession';
import type { BudgetChange, Message } from '../lib/types';
import { BudgetConfirm } from './BudgetConfirm';

const MAX_CHARS = 8000;

export function Conversation({
    session,
    disabled,
    archived,
    onConfirmBudget,
}: {
    session: UseChatSession;
    disabled?: boolean;
    archived?: boolean;
    onConfirmBudget: (change: BudgetChange) => Promise<void>;
}) {
    const [draft, setDraft] = useState('');
    const endRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        endRef.current?.scrollIntoView({ block: 'end' });
    }, [session.messages.length, session.pendingText]);

    async function submit() {
        const content = draft.trim();
        if (!content || archived || session.pendingText !== null) return;
        // Keep the draft until the turn lands, so a failure never loses typing.
        const ok = await session.send(content);
        if (ok) setDraft('');
    }

    return (
        <div className="flex h-full flex-col">
            <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="mx-auto max-w-2xl px-6 py-6">
                    {session.hasMore && (
                        <button
                            onClick={() => void session.loadMore()}
                            className="mb-6 w-full rounded-md py-2 text-xs text-ink-muted hover:bg-surface-2"
                        >
                            Load earlier messages
                        </button>
                    )}

                    {session.messages.length === 0 && !session.loading && session.pendingText === null && (
                        <div className="py-16 text-center">
                            <p className="text-sm text-ink-muted">
                                Describe what you want to build, in plain language.
                            </p>
                            <p className="mt-2 text-xs text-ink-faint">
                                For example: object storage for user uploads, with a small API in front of it.
                            </p>
                        </div>
                    )}

                    {session.messages.map((m) => (
                        <Bubble key={m.id} message={m} />
                    ))}

                    {session.pendingText !== null && (
                        <>
                            <div className="mb-5 flex justify-end">
                                <div className="max-w-[85%] rounded-xl rounded-br-sm bg-surface-2 px-3.5 py-2.5 text-sm text-ink opacity-60">
                                    {session.pendingText}
                                </div>
                            </div>
                            <div className="mb-5 text-sm text-ink-muted">
                                {/* Elapsed time rather than a spinner: the model call is slow,
                    unstreamed and cannot be cancelled (§6). */}
                                Working through it… {session.pendingFor ?? 0}s
                                {(session.pendingFor ?? 0) > 25 && (
                                    <span className="text-ink-faint"> — this can take up to two minutes.</span>
                                )}
                            </div>
                        </>
                    )}

                    {session.budgetChange && (
                        <BudgetConfirm
                            change={session.budgetChange}
                            onConfirm={async (c) => {
                                await onConfirmBudget(c);
                                session.dismissBudgetChange();
                            }}
                            onDismiss={session.dismissBudgetChange}
                        />
                    )}

                    {session.failure && (
                        <div
                            className={`mb-5 rounded-md border p-3 ${session.failure.stale ? 'border-replace bg-replace-bg' : 'border-edge bg-surface-2'
                                }`}
                        >
                            <p className={`text-sm ${session.failure.stale ? 'text-replace-ink' : 'text-ink'}`}>
                                {session.failure.message}
                            </p>
                            {session.failure.requestId && (
                                <p className="mt-2 font-mono text-[11px] text-ink-faint">{session.failure.requestId}</p>
                            )}
                            <button
                                onClick={session.dismissFailure}
                                className="mt-2 text-xs text-ink-muted hover:text-ink"
                            >
                                Dismiss
                            </button>
                        </div>
                    )}

                    <div ref={endRef} />
                </div>
            </div>

            <div className="shrink-0 border-t border-edge px-6 py-4">
                <div className="mx-auto max-w-2xl">
                    <div className="flex items-end gap-2 rounded-lg border border-edge bg-surface-1 p-2 focus-within:border-edge-strong">
                        <textarea
                            value={draft}
                            onChange={(e) => setDraft(e.target.value.slice(0, MAX_CHARS))}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter' && !e.shiftKey) {
                                    e.preventDefault();
                                    void submit();
                                }
                            }}
                            rows={1}
                            disabled={disabled || archived}
                            placeholder={
                                archived
                                    ? 'This chat is archived'
                                    : disabled
                                        ? 'Start a chat first'
                                        : 'Describe what you need…'
                            }
                            className="max-h-40 min-h-9.5 flex-1 resize-none bg-transparent px-2 py-2 text-sm text-ink outline-none placeholder:text-ink-faint"
                        />
                        <button
                            onClick={() => void submit()}
                            disabled={disabled || archived || !draft.trim() || session.pendingText !== null}
                            className="rounded-md bg-ink px-3.5 py-2 text-sm font-medium text-surface-0 disabled:opacity-30"
                        >
                            Send
                        </button>
                    </div>

                    {draft.length > MAX_CHARS - 500 && (
                        <p className="mt-1.5 text-right text-[11px] text-ink-faint">
                            {MAX_CHARS - draft.length} characters left
                        </p>
                    )}

                    {/* Below the box rather than a banner: it sits where the user is
              looking when they act, and costs no vertical space (§10.8). */}
                    <p className="mt-2.5 text-[11px] leading-relaxed text-ink-faint">
                        Resources are only created when you approve them here, and only in the AWS account you
                        connected. If you run the generated configuration yourself, we cannot see it and the
                        auto-teardown timer will not apply. AI can make mistakes — review the plan before
                        approving.
                    </p>
                </div>
            </div>
        </div>
    );
}

function Bubble({ message }: { message: Message }) {
    // The backend narrates events like a revert into the transcript. These explain
    // why the panel changed on its own, so they must not look like the assistant
    // talking (§10.4).
    if (message.role === 'system') {
        return (
            <div className="mb-5 flex items-center gap-2.5">
                <div className="h-px flex-1 bg-edge" />
                <span className="text-[11px] text-ink-faint">{message.content}</span>
                <div className="h-px flex-1 bg-edge" />
            </div>
        );
    }

    if (message.role === 'user') {
        return (
            <div className="mb-5 flex justify-end">
                <div className="max-w-[85%] whitespace-pre-wrap rounded-xl rounded-br-sm bg-surface-2 px-3.5 py-2.5 text-sm text-ink">
                    {message.content}
                </div>
            </div>
        );
    }

    const canned = message.model === 'stub';

    return (
        <div className="mb-5">
            <div className="whitespace-pre-wrap text-sm leading-relaxed text-ink">{message.content}</div>
            {message.model && (
                <p
                    title={message.model}
                    className={`mt-1 text-[10px] ${canned ? 'text-replace-ink' : 'text-ink-faint'}`}
                >
                    {canned ? 'canned keyword match, not model output' : message.model}
                </p>
            )}
        </div>
    );
}