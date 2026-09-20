import { useState } from 'react';
import { userMessage } from '../lib/errors';
import type { Chat } from '../lib/types';

/**
 * The budget here is the guardrail, not the AWS Budgets template. Exceeding it
 * makes a version `applicable: false` and disables Apply (§10.5). It is
 * reachable only through PATCH /chats — nothing in the conversation sets it —
 * which is why it needs a control of its own.
 */
export function ChatSettings({
    chat,
    onSave,
    onClose,
}: {
    chat: Chat;
    onSave: (patch: {
        title?: string;
        monthlyBudgetUsd?: number;
        clearBudget?: boolean;
    }) => Promise<void>;
    onClose: () => void;
}) {
    const [title, setTitle] = useState(chat.title ?? '');
    const [budget, setBudget] = useState(
        typeof chat.monthly_budget_usd === 'number' ? String(chat.monthly_budget_usd) : '',
    );
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const had = typeof chat.monthly_budget_usd === 'number';
    const parsed = budget.trim() === '' ? null : Number(budget);
    const budgetInvalid =
        parsed !== null && (!Number.isFinite(parsed) || parsed <= 0 || parsed > 1_000_000);

    async function save() {
        if (budgetInvalid) return;
        setBusy(true);
        setError(null);
        try {
            await onSave({
                title: title.trim() === chat.title ? undefined : title.trim(),
                monthlyBudgetUsd: parsed ?? undefined,
                // Clearing needs to be explicit, so a field the user did not touch can
                // never silently drop a cost ceiling (§6).
                clearBudget: had && parsed === null ? true : undefined,
            });
            onClose();
        } catch (e) {
            setError(userMessage(e));
        } finally {
            setBusy(false);
        }
    }

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
            <div className="w-full max-w-md rounded-xl border border-edge bg-surface-1 p-6">
                <h2 className="text-base font-medium text-ink">Chat settings</h2>

                <label className="mt-5 block text-xs text-ink-muted">Title</label>
                <input
                    value={title}
                    onChange={(e) => setTitle(e.target.value)}
                    placeholder="Untitled"
                    className="mt-1.5 w-full rounded-md border border-edge bg-surface-2 px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-edge-strong"
                />

                <label className="mt-4 block text-xs text-ink-muted">Monthly budget (USD)</label>
                <input
                    value={budget}
                    inputMode="decimal"
                    onChange={(e) => setBudget(e.target.value)}
                    placeholder="no limit"
                    className="mt-1.5 w-full rounded-md border border-edge bg-surface-2 px-3 py-2 font-mono text-sm text-ink outline-none placeholder:font-sans placeholder:text-ink-faint focus:border-edge-strong"
                />
                {budgetInvalid ? (
                    <p className="mt-1.5 text-xs text-destroy-ink">
                        Must be more than 0 and no more than 1,000,000.
                    </p>
                ) : (
                    <p className="mt-1.5 text-[11px] leading-relaxed text-ink-faint">
                        A proposal estimated above this cannot be applied. Leave empty for no ceiling.
                    </p>
                )}

                {/* Versions are immutable, so raising the limit does not re-check a
            proposal that was already blocked (§6). */}
                {had && parsed !== null && parsed !== chat.monthly_budget_usd && (
                    <p className="mt-3 border-l-2 border-replace bg-replace-bg/40 py-2 pl-3 text-xs text-replace-ink">
                        Existing proposals keep the verdict they were given. Send a message to get one checked
                        against the new limit.
                    </p>
                )}

                {had && parsed === null && (
                    <p className="mt-3 border-l-2 border-replace bg-replace-bg/40 py-2 pl-3 text-xs text-replace-ink">
                        Removing the ceiling means nothing will block an expensive proposal.
                    </p>
                )}

                {error && <p className="mt-3 text-sm text-destroy-ink">{error}</p>}

                <div className="mt-5 flex justify-end gap-2">
                    <button onClick={onClose} className="px-3 py-2 text-sm text-ink-muted hover:text-ink">
                        Cancel
                    </button>
                    <button
                        onClick={() => void save()}
                        disabled={busy || budgetInvalid}
                        className="rounded-md bg-ink px-4 py-2 text-sm font-medium text-surface-0 disabled:opacity-40"
                    >
                        {busy ? 'Saving…' : 'Save'}
                    </button>
                </div>
            </div>
        </div>
    );
}