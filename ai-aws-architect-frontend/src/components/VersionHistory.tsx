import { useState } from 'react';
import type { UseVersions } from '../hooks/useVersions';
import { isWholeDocument } from '../lib/diffReadout';
import { DiffView } from './DiffView';
import { templateLabel } from '../lib/labels';
import { userMessage } from '../lib/errors';
import type { ConfigDocument, RevertResult, VersionListEntry } from '../lib/types';

const SOURCE_LABEL: Record<string, string> = {
    assistant: 'from the conversation',
    revert: 'a revert',
    manual: 'edited by hand',
};

export function VersionHistory({
    versions,
    currentDocument,
    onReverted,
}: {
    versions: UseVersions;
    currentDocument: ConfigDocument | null;
    onReverted: (result: RevertResult) => void;
}) {
    const [reverting, setReverting] = useState(false);
    const [revertError, setRevertError] = useState<string | null>(null);

    if (versions.loading && versions.versions.length === 0) {
        return <p className="p-4 text-sm text-ink-faint">Loading history…</p>;
    }

    if (versions.versions.length === 0) {
        return <p className="p-4 text-sm text-ink-faint">{versions.error ?? 'No versions yet.'}</p>;
    }

    if (versions.selected !== null) {
        return (
            <VersionDetail
                versions={versions}
                currentDocument={currentDocument}
                reverting={reverting}
                error={revertError}
                onRevert={async (v) => {
                    setReverting(true);
                    setRevertError(null);
                    try {
                        onReverted(await versions.revert(v));
                    } catch (e) {
                        setRevertError(userMessage(e));
                    } finally {
                        setReverting(false);
                    }
                }}
            />
        );
    }

    return (
        <div className="p-4">
            <h3 className="mb-2 text-[11px] uppercase tracking-wide text-ink-faint">Versions</h3>
            <ul className="space-y-1">
                {versions.versions.map((v) => (
                    <li key={v.version}>
                        <button
                            onClick={() => versions.select(v.version)}
                            className="w-full rounded-md border border-edge bg-surface-2 p-2.5 text-left hover:border-edge-strong"
                        >
                            <div className="flex items-baseline justify-between gap-2">
                                <span className="text-sm text-ink">Version {v.version}</span>
                                {v.is_current && (
                                    <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] text-ink-muted ring-1 ring-edge">
                                        current
                                    </span>
                                )}
                            </div>
                            {v.summary && (
                                <p className="mt-0.5 line-clamp-2 text-xs text-ink-muted">{v.summary}</p>
                            )}
                            <p className="mt-1 text-[11px] text-ink-faint">
                                {SOURCE_LABEL[v.source] ?? v.source}
                                {v.reverted_from_version != null && ` of version ${v.reverted_from_version}`}
                                {v.parent_version != null &&
                                    v.parent_version !== v.version - 1 &&
                                    ` · based on version ${v.parent_version}`}
                                {!v.applicable && ' · cannot be applied'}
                            </p>
                        </button>
                    </li>
                ))}
            </ul>
        </div>
    );
}

function VersionDetail({
    versions,
    currentDocument,
    reverting,
    error,
    onRevert,
}: {
    versions: UseVersions;
    currentDocument: ConfigDocument | null;
    reverting: boolean;
    error: string | null;
    onRevert: (version: number) => void;
}) {
    const v = versions.selectedVersion;
    const diff = versions.diff;
    const entry = versions.versions.find((e) => e.version === versions.selected);

    const entryIsCurrent = entry?.is_current ?? false;
    const wholeDoc = isWholeDocument(diff?.changes ?? []);

    return (
        <div className="p-4">
            <button
                onClick={() => versions.select(null)}
                className="mb-3 text-xs text-ink-muted hover:text-ink"
            >
                ← All versions
            </button>

            <h3 className="text-sm font-medium text-ink">Version {versions.selected}</h3>
            {entry?.summary && <p className="mt-1 text-sm text-ink-muted">{entry.summary}</p>}

            {versions.diffLoading && <p className="mt-3 text-xs text-ink-faint">Loading changes…</p>}

            {/* §10.6's first problem: v1's diff is one add at the root carrying the
          whole document. There is nothing to compare, so show the config. */}
            {!versions.diffLoading && wholeDoc && (
                <div className="mt-3">
                    <p className="mb-2 text-[11px] uppercase tracking-wide text-ink-faint">
                        The first proposal
                    </p>
                    <ul className="space-y-1">
                        {(v?.document?.blocks ?? []).map((b) => (
                            <li key={b.id} className="text-sm text-ink">
                                {templateLabel(b.template_id)}
                            </li>
                        ))}
                    </ul>
                </div>
            )}

            {!versions.diffLoading && diff && !wholeDoc && (
                <div className="mt-3">
                    <DiffView diff={diff} document={v?.document ?? currentDocument} />
                </div>
            )}

            {error && (
                <p className="mt-3 rounded-md border border-edge bg-surface-2 p-3 text-sm text-ink">
                    {error}
                </p>
            )}

            {!entryIsCurrent && versions.selected !== null && (
                <>
                    <button
                        onClick={() => onRevert(versions.selected!)}
                        disabled={reverting}
                        className="mt-4 w-full rounded-md bg-surface-2 px-4 py-2.5 text-sm font-medium text-ink ring-1 ring-edge hover:ring-edge-strong disabled:opacity-40"
                    >
                        {reverting ? 'Reverting…' : `Restore version ${versions.selected}`}
                    </button>
                    {/* Users expect revert to erase. Here it moves forward, and a revert
              can itself be reverted (§10.6). */}
                    <p className="mt-2 text-[11px] leading-relaxed text-ink-faint">
                        This adds a new version carrying this one's contents. Nothing is deleted, and the
                        restore can itself be undone.
                    </p>
                </>
            )}
        </div>
    );
}

export function versionCount(versions: VersionListEntry[]): number {
    return versions.length;
}