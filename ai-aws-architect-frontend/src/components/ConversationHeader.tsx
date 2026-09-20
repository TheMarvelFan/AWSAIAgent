import { useState } from 'react';
import { userMessage } from '../lib/errors';
import type { Chat } from '../lib/types';

/**
 * A header for the open chat. Archiving lives here rather than in the top bar
 * because it acts on one chat — sitting above the conversation, next to that
 * chat's title, is what makes the scope obvious.
 */
export function ConversationHeader({
    chat,
    archived,
    onSettings,
    onArchive,
    onUnarchive,
}: {
    chat: Chat;
    archived: boolean;
    onSettings: () => void;
    onArchive: () => void;
    onUnarchive: () => void;
}) {
    // An archived chat is frozen: nothing about it can change while it is
    // archived, so the only action offered is restoring it.
    if (archived) {
        return (
            <div className="flex h-11 shrink-0 items-center gap-2 border-b border-edge bg-surface-2/50 px-4">
                <span className="min-w-0 flex-1 truncate text-sm text-ink-muted">
                    {chat.title || 'Untitled'}
                </span>
                <span className="shrink-0 rounded px-1.5 py-0.5 text-[11px] text-ink-faint ring-1 ring-edge">
                    archived
                </span>
                <button
                    onClick={onUnarchive}
                    className="shrink-0 rounded-md px-2.5 py-1 text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink"
                >
                    Unarchive
                </button>
            </div>
        );
    }

    return (
        <div className="flex h-11 shrink-0 items-center gap-2 border-b border-edge px-4">
            <span className="min-w-0 flex-1 truncate text-sm text-ink">
                {chat.title || 'Untitled'}
            </span>

            {typeof chat.monthly_budget_usd === 'number' && (
                <span
                    title="Proposals estimated above this cannot be applied"
                    className="shrink-0 rounded px-1.5 py-0.5 font-mono text-[11px] text-ink-muted ring-1 ring-edge"
                >
                    ${chat.monthly_budget_usd}/mo
                </span>
            )}

            <button
                onClick={onSettings}
                aria-label="Chat settings"
                title="Chat settings"
                className="shrink-0 rounded-md p-1.5 text-ink-faint hover:bg-surface-2 hover:text-ink"
            >
                <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
                    <circle cx="8" cy="8" r="2.2" stroke="currentColor" strokeWidth="1.3" />
                    <path
                        d="M8 1.5v1.6M8 12.9v1.6M14.5 8h-1.6M3.1 8H1.5m10.1-4.6-1.1 1.1M5.5 10.5l-1.1 1.1m0-8.2 1.1 1.1m5 5 1.1 1.1"
                        stroke="currentColor"
                        strokeWidth="1.3"
                        strokeLinecap="round"
                    />
                </svg>
            </button>

            <button
                onClick={onArchive}
                aria-label="Archive this chat"
                title="Archive this chat"
                className="shrink-0 rounded-md p-1.5 text-ink-faint hover:bg-surface-2 hover:text-ink"
            >
                <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
                    <rect x="2" y="3" width="12" height="3" rx="1" stroke="currentColor" strokeWidth="1.3" />
                    <path
                        d="M3.5 6v6.5a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1V6M6.5 9h3"
                        stroke="currentColor"
                        strokeWidth="1.3"
                        strokeLinecap="round"
                    />
                </svg>
            </button>
        </div>
    );
}

/**
 * Archiving does not tear anything down, so a chat with live resources needs
 * the count in front of the user before they hide it (§6).
 */
export function ArchiveDialog({
    chat,
    liveCount,
    onConfirm,
    onClose,
}: {
    chat: Chat;
    liveCount: number;
    onConfirm: () => Promise<void>;
    onClose: () => void;
}) {
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string | null>(null);

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
            <div className="w-full max-w-md rounded-xl border border-edge bg-surface-1 p-6">
                <h2 className="text-base font-medium text-ink">
                    Archive "{chat.title || 'Untitled'}"?
                </h2>
                <p className="mt-2 text-sm text-ink-muted">
                    It leaves the list but keeps its messages, versions and deployments. There is no way to
                    un-archive it from here — you would need to turn on "Show archived" to find it again.
                </p>

                {liveCount > 0 && (
                    <div className="mt-4 rounded-md border-2 border-destroy bg-destroy-bg p-3">
                        <p className="text-sm text-destroy-ink">
                            {liveCount} deployment{liveCount === 1 ? '' : 's'} may still be running. Archiving
                            does not tear anything down, and they keep billing.
                        </p>
                    </div>
                )}

                {error && <p className="mt-3 text-sm text-destroy-ink">{error}</p>}

                <div className="mt-5 flex justify-end gap-2">
                    <button onClick={onClose} className="px-3 py-2 text-sm text-ink-muted hover:text-ink">
                        Cancel
                    </button>
                    <button
                        onClick={async () => {
                            setBusy(true);
                            setError(null);
                            try {
                                await onConfirm();
                            } catch (e) {
                                setError(userMessage(e));
                            } finally {
                                setBusy(false);
                            }
                        }}
                        disabled={busy}
                        className="rounded-md bg-ink px-4 py-2 text-sm font-medium text-surface-0 disabled:opacity-40"
                    >
                        {busy ? 'Archiving…' : 'Archive'}
                    </button>
                </div>
            </div>
        </div>
    );
}