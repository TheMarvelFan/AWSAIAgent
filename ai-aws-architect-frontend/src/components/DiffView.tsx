import { useMemo, useState } from 'react';
import { describeChange, groupChanges, isWholeDocument, type ChangeTone } from '../lib/diffReadout';
import type { ConfigDocument, Diff } from '../lib/types';

type View = 'summary' | 'json' | 'text';

const TONE: Record<ChangeTone, { mark: string; label: string; chip: string; body: string }> = {
    added: {
        mark: '+',
        label: 'Added',
        chip: 'border border-diff-added bg-diff-added-bg text-diff-added',
        body: 'text-ink',
    },
    changed: {
        mark: '~',
        label: 'Modified',
        chip: 'border border-diff-modified bg-diff-modified-bg text-diff-modified',
        body: 'text-ink',
    },
    // Removed carries no hue — a strikethrough says it without spending a colour,
    // and it reads correctly whatever your vision does with hue.
    removed: {
        mark: '−',
        label: 'Removed',
        chip: 'border border-edge-strong bg-surface-3 text-ink-muted',
        body: 'text-ink-muted line-through decoration-ink-faint',
    },
};

export function DiffView({
    diff,
    document,
    fromDocument,
}: {
    diff: Diff;
    document: ConfigDocument | null;
    fromDocument?: ConfigDocument | null;
}) {
    const [view, setView] = useState<View>('summary');

    const changes = diff.changes ?? [];
    const wholeDoc = isWholeDocument(changes);

    const readable = useMemo(() => {
        if (changes.length === 0 || wholeDoc) return [];
        return groupChanges(changes.map((c) => describeChange(c, fromDocument ?? null, document)));
    }, [changes, wholeDoc, document, fromDocument]);

    const counts = useMemo(() => {
        const c = { added: 0, changed: 0, removed: 0 };
        for (const group of readable) for (const ch of group.changes) c[ch.tone] += 1;
        return c;
    }, [readable]);

    const canSummarise = readable.length > 0;
    const hasText = Boolean(diff.unified);

    return (
        <div className="rounded-md border border-edge bg-surface-2">
            <div className="flex items-center justify-between gap-2 border-b border-edge px-3 py-2">
                <span className="text-[11px] uppercase tracking-wide text-ink-faint">
                    v{diff.from_version} → v{diff.to_version}
                </span>
                <div className="flex rounded border border-edge">
                    {canSummarise && (
                        <Tab active={view === 'summary'} onClick={() => setView('summary')}>
                            Summary
                        </Tab>
                    )}
                    <Tab active={view === 'json'} onClick={() => setView('json')}>
                        JSON
                    </Tab>
                    {hasText && (
                        <Tab active={view === 'text'} onClick={() => setView('text')}>
                            Text
                        </Tab>
                    )}
                </div>
            </div>

            {view === 'summary' && canSummarise && (
                <div className="px-3 py-2.5">
                    <div className="mb-2.5 flex flex-wrap gap-1.5">
                        {counts.added > 0 && <Chip tone="added" n={counts.added} />}
                        {counts.changed > 0 && <Chip tone="changed" n={counts.changed} />}
                        {counts.removed > 0 && <Chip tone="removed" n={counts.removed} />}
                    </div>

                    {readable.map((group, gi) => (
                        <div key={gi} className="mb-3 last:mb-0">
                            {group.scope && <p className="mb-1 text-[11px] text-ink-faint">{group.scope}</p>}
                            <ul className="space-y-1">
                                {group.changes.map((c, i) => (
                                    <li key={i} className="flex items-baseline gap-2 text-[13px]">
                                        <span
                                            className={`mt-px shrink-0 rounded px-1 font-mono text-[11px] ${TONE[c.tone].chip}`}
                                        >
                                            {TONE[c.tone].mark}
                                        </span>
                                        <span className={TONE[c.tone].body}>{c.text}</span>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    ))}

                    {/* Nothing here destroys anything. §7: reverting changes what is
              proposed, not what is running. */}
                    <p className="mt-3 border-t border-edge pt-2 text-[11px] leading-relaxed text-ink-faint">
                        Changes to the proposal. Your running infrastructure is untouched until you plan and
                        approve.
                    </p>
                </div>
            )}

            {view === 'json' && (
                <div className="plan-output max-h-72 px-3 py-2 text-ink-muted">
                    {changes.length > 0
                        ? JSON.stringify(changes, null, 2)
                        : 'The server returned no structured changes for this version.'}
                </div>
            )}

            {view === 'text' && hasText && (
                <div className="plan-output max-h-72 px-3 py-2 text-ink-muted">{diff.unified}</div>
            )}

            {/* Say why the summary is unavailable rather than quietly showing raw. */}
            {!canSummarise && view === 'summary' && (
                <p className="px-3 py-3 text-sm text-ink-muted">
                    {wholeDoc
                        ? 'This is the first version, so there is nothing to compare it against.'
                        : changes.length === 0
                            ? 'The server returned no structured changes, so there is nothing to summarise. The JSON and text views show what did come back.'
                            : 'Nothing changed in this version.'}
                </p>
            )}
        </div>
    );
}

function Chip({ tone, n }: { tone: ChangeTone; n: number }) {
    return (
        <span className={`rounded px-1.5 py-0.5 text-[11px] ${TONE[tone].chip}`}>
            {n} {TONE[tone].label.toLowerCase()}
        </span>
    );
}

function Tab({
    active,
    onClick,
    children,
}: {
    active: boolean;
    onClick: () => void;
    children: React.ReactNode;
}) {
    return (
        <button
            onClick={onClick}
            className={`px-2 py-0.5 text-[11px] ${active ? 'bg-surface-3 text-ink' : 'text-ink-faint hover:text-ink-muted'
                }`}
        >
            {children}
        </button>
    );
}