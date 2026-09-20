import { useState } from 'react';
import { userMessage } from '../lib/errors';
import type { BudgetChange } from '../lib/types';

/**
 * What to do with a budget_change the server returned.
 *
 * `none` — already applied, or a no-op. The assistant's reply says what
 *          happened; adding a second sentence would duplicate it.
 * `confirm` — a loosening, which needs the human action.
 * `inform` — out of range or unusable. Show the reason, offer nothing.
 */
export function budgetPrompt(change: BudgetChange): 'none' | 'confirm' | 'inform' {
    if (change.applied) return 'none';
    if (change.clear) return 'confirm';
    if (change.to_usd === null) return 'inform';
    if (change.from_usd !== null && change.to_usd === change.from_usd) return 'none';
    if (change.to_usd < 1 || change.to_usd > 1_000_000) return 'inform';
    return 'confirm';
}

/**
 * The server never raises a ceiling on the strength of model output alone, so
 * this exists to turn a read instruction into a deliberate act. Confirming is
 * an ordinary PATCH — there is no pending state on the server.
 */
export function BudgetConfirm({
    change,
    onConfirm,
    onDismiss,
}: {
    change: BudgetChange;
    onConfirm: (change: BudgetChange) => Promise<void>;
    onDismiss: () => void;
}) {
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const mode = budgetPrompt(change);

    if (mode === 'none') return null;

    return (
        <div className="mb-5 border-l-2 border-replace bg-replace-bg/40 py-2.5 pl-3 pr-3">
            {/* A number alone gives no way to tell a correct reading from a misheard
          one. "We spent $500 last month" and "cap it at $500" look identical
          until the phrase is next to the figure. */}
            <p className="text-sm italic text-ink-muted">"{change.quote}"</p>
            <p className="mt-1.5 text-sm text-replace-ink">{change.reason}</p>

            {error && <p className="mt-1.5 text-xs text-destroy-ink">{error}</p>}

            {mode === 'confirm' && (
                <div className="mt-2.5 flex items-center gap-2">
                    <button
                        onClick={async () => {
                            setBusy(true);
                            setError(null);
                            try {
                                await onConfirm(change);
                            } catch (e) {
                                setError(userMessage(e));
                            } finally {
                                setBusy(false);
                            }
                        }}
                        disabled={busy}
                        className="rounded-md bg-ink px-3 py-1.5 text-xs font-medium text-surface-0 disabled:opacity-40"
                    >
                        {busy
                            ? 'Saving…'
                            : change.clear
                                ? 'Remove the limit'
                                : `Set limit to $${change.to_usd}`}
                    </button>
                    <button onClick={onDismiss} className="px-2 py-1.5 text-xs text-ink-muted hover:text-ink">
                        Not now
                    </button>
                </div>
            )}

            {mode === 'inform' && (
                <button
                    onClick={onDismiss}
                    className="mt-2 text-xs text-ink-muted hover:text-ink"
                >
                    Dismiss
                </button>
            )}
        </div>
    );
}