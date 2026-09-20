import { useState } from 'react';
import { deployments } from '../lib/api';
import { isApiError, userMessage } from '../lib/errors';
import type { ConfigVersion, ManualVersionResult, Message, Scope } from '../lib/types';
import { paramLabel, paramValue } from '../lib/labels';
import { useVersions } from '../hooks/useVersions';
import { useCatalog } from '../hooks/useCatalog';
import { VersionHistory } from './VersionHistory';
import { ConfigEditor } from './ConfigEditor';

const SCOPE_LABEL: Record<Scope, string> = {
    supported: 'Supported',
    needs_clarification: 'Needs clarification',
    out_of_scope: 'Outside the catalog',
};

export function ConfigPanel({
    chatId,
    config,
    frozen,
    onPlanStarted,
    onVersionAdded,
}: {
    chatId: string | null;
    config: ConfigVersion | null;
    /** Archived chats are read-only: no planning, no reverting. */
    frozen?: boolean;
    onPlanStarted: (deploymentId: string) => void;
    onVersionAdded: (result: { config_version: ConfigVersion; message: Message }) => void;
}) {
    const [planning, setPlanning] = useState(false);
    const [planError, setPlanError] = useState<string | null>(null);
    const [view, setView] = useState<'proposal' | 'history' | 'edit'>('proposal');
    // The version number is the refresh key: a new one means the list is stale.
    const versions = useVersions(chatId, config?.version ?? null);
    const { template } = useCatalog();

    if (!chatId) {
        return <Empty>Start a chat to see a proposal here.</Empty>;
    }

    // 204 from the config endpoint. An empty panel, not an error (§7).
    if (!config) {
        return <Empty>Describe what you want to build and a proposal will appear here.</Empty>;
    }

    const doc = config.document;

    // The server omits empty arrays rather than sending []. Every array that could
    // be absent gets its default here, once, instead of at each use site.
    const blocks = doc.blocks ?? [];
    const gaps = doc.out_of_catalog ?? [];
    const questions = doc.open_questions ?? [];
    const blockers = config.blockers ?? [];
    // A manual edit was not scored by anything, and a revert carries the verdict
    // of the version it restored. Showing 0.00 for a config the user wrote
    // themselves reads as the system rating their work at zero (§4).
    const scored = config.source !== 'manual' && config.confidence !== null;
    const confidence = scored ? config.confidence : null;

    // The catalog knows whether the active runner can actually build each block.
    // Catching it here is the cheapest place — the alternative is a failure that
    // lands after the user has read a plan and approved it.
    const unbuildable = blocks
        .map((b) => ({ block: b, spec: template(b.template_id) }))
        .filter((x) => x.spec?.provisionable === false);

    async function plan() {
        if (!chatId || !config) return;
        setPlanning(true);
        setPlanError(null);
        try {
            const { deployment } = await deployments.plan(chatId, config.version);
            onPlanStarted(deployment.id);
        } catch (e) {
            if (isApiError(e) && e.is('aws_not_connected')) {
                setPlanError('Connect an AWS account before planning. Use the padlock at the top right.');
            } else if (isApiError(e) && e.is('conflict')) {
                setPlanError(
                    'Something is already in flight for this chat, or this version cannot be applied. Refresh and check the deployment below.',
                );
            } else {
                setPlanError(userMessage(e));
            }
        } finally {
            setPlanning(false);
        }
    }

    if (view === 'edit' && chatId) {
        return (
            <ConfigEditor
                chatId={chatId}
                config={config}
                onCancel={() => setView('proposal')}
                onSaved={(result: ManualVersionResult) => {
                    onVersionAdded(result);
                    setView('proposal');
                }}
            />
        );
    }

    if (view === 'history') {
        return (
            <div>
                <ViewTabs
                    view={view}
                    setView={setView}
                    count={versions.versions.length}
                    canEdit={!frozen}
                />
                <VersionHistory
                    versions={versions}
                    currentDocument={config.document}
                    onReverted={(result) => {
                        onVersionAdded(result);
                        setView('proposal');
                    }}
                />
            </div>
        );
    }

    return (
        <div className="p-4">
            <ViewTabs
                view={view}
                setView={setView}
                count={versions.versions.length}
                canEdit={!frozen}
                inline
            />

            <div className="mb-4 flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <h2 className="truncate text-sm font-medium text-ink">{doc.name}</h2>
                    <p className="mt-0.5 text-xs text-ink-faint">
                        Version {config.version} · {doc.region}
                    </p>
                </div>
                {config.source === 'manual' ? (
                    <span className="shrink-0 rounded px-1.5 py-0.5 text-[11px] text-ink-muted ring-1 ring-edge">
                        Edited by hand
                    </span>
                ) : (
                    <span className="shrink-0 rounded px-1.5 py-0.5 text-[11px] text-ink-muted ring-1 ring-edge">
                        {SCOPE_LABEL[config.scope]}
                    </span>
                )}
            </div>

            {doc.summary && <p className="mb-4 text-sm leading-relaxed text-ink-muted">{doc.summary}</p>}

            {/* The guardrail moment: show what would be built, and why it will not be
          (§10.5). Apply is disabled, the proposal stays visible. */}
            {!config.applicable && blockers.length > 0 && (
                <div className="mb-4 rounded-md border border-edge bg-surface-2 p-3">
                    <p className="text-sm font-medium text-ink">This cannot be applied</p>
                    <ul className="mt-2 space-y-1.5">
                        {blockers.map((b, i) => (
                            <li key={i} className="text-sm text-ink-muted">
                                {b.message}
                            </li>
                        ))}
                    </ul>
                </div>
            )}

            {config.scope === 'needs_clarification' && config.applicable && (
                <p className="mb-4 text-xs text-ink-muted">
                    Some of this was inferred rather than stated. Worth a closer look at the plan.
                </p>
            )}

            <Section title="Blocks">
                <div className="space-y-2">
                    {blocks.map((block) => (
                        <div key={block.id} className="rounded-md border border-edge bg-surface-2 p-3">
                            <div className="flex items-baseline justify-between gap-2">
                                <span className="font-mono text-[12px] text-ink">{block.template_id}</span>
                                <span className="shrink-0 text-[11px] text-ink-faint">{block.id}</span>
                            </div>
                            {block.purpose && (
                                <p className="mt-1 text-xs leading-relaxed text-ink-muted">{block.purpose}</p>
                            )}
                            {block.parameters && Object.keys(block.parameters).length > 0 && (
                                <dl className="mt-2 space-y-0.5">
                                    {Object.entries(block.parameters).map(([k, v]) => (
                                        <div key={k} className="flex justify-between gap-3 text-[11px]">
                                            <dt className="text-ink-faint">{paramLabel(k)}</dt>
                                            <dd className="font-mono text-ink-muted">{paramValue(k, v)}</dd>
                                        </div>
                                    ))}
                                </dl>
                            )}
                        </div>
                    ))}
                </div>
            </Section>

            {/* Requirements the catalog cannot meet. Hiding these would defeat the
          point of recording them (§10.5). Distinct from scope: out_of_scope. */}
            {gaps.length > 0 && (
                <Section title="Not covered by the catalog">
                    <div className="space-y-2">
                        {gaps.map((item, i) => (
                            <div key={i} className="border-l-2 border-replace bg-replace-bg/40 py-2 pl-3">
                                <p className="text-sm text-ink">{item.need}</p>
                                <p className="mt-0.5 text-xs text-ink-muted">{item.reason}</p>
                                {item.suggested_manual_step && (
                                    <p className="mt-1 text-xs text-ink-faint">{item.suggested_manual_step}</p>
                                )}
                            </div>
                        ))}
                    </div>
                </Section>
            )}

            {questions.length > 0 && (
                <Section title="Open questions">
                    <ul className="space-y-1.5">
                        {questions.map((q, i) => (
                            <li key={i} className="text-sm text-ink-muted">
                                {q}
                            </li>
                        ))}
                    </ul>
                </Section>
            )}

            {doc.estimated_cost && typeof doc.estimated_cost.monthly_low === 'number' && (
                <Section title="Estimated cost">
                    <p className="font-mono text-sm text-ink">
                        {doc.estimated_cost.currency} {doc.estimated_cost.monthly_low.toFixed(2)} –{' '}
                        {doc.estimated_cost.monthly_high.toFixed(2)}
                        <span className="text-ink-faint"> / month</span>
                    </p>
                    {/* Static per-template figures. For usage-priced services they will be
              wrong, and saying so is cheaper than being believed (§10.5). */}
                    <p className="mt-1 text-[11px] leading-relaxed text-ink-faint">
                        A rough sum of per-template estimates. Services priced by usage are not modelled, so
                        treat this as an order of magnitude rather than a quote.
                    </p>
                </Section>
            )}

            {config.rationale && (
                <Section title="Reasoning">
                    <p className="whitespace-pre-wrap text-xs leading-relaxed text-ink-muted">
                        {config.rationale}
                    </p>
                </Section>
            )}

            {confidence !== null && (
                <div className="mt-4 border-t border-edge pt-3">
                    <div className="flex items-baseline justify-between text-[11px]">
                        <span className="text-ink-faint">Self-reported confidence</span>
                        <span className="font-mono text-ink-muted">{confidence.toFixed(2)}</span>
                    </div>
                    <p className="mt-1 text-[11px] leading-relaxed text-ink-faint">
                        Reported by the model and not calibrated against anything. Not a probability.
                    </p>
                </div>
            )}

            {unbuildable.length > 0 && (
                <div className="mt-4 rounded-md border-2 border-dashed border-replace bg-replace-bg p-3">
                    <p className="text-sm font-medium text-replace-ink">
                        {unbuildable.length === 1 ? 'One block cannot' : `${unbuildable.length} blocks cannot`}{' '}
                        be built
                    </p>
                    <ul className="mt-2 space-y-1.5">
                        {unbuildable.map(({ block, spec }) => (
                            <li key={block.id} className="text-xs text-replace-ink">
                                <span className="font-mono">{block.template_id}</span>
                                {spec?.unprovisionable_reason ? ` — ${spec.unprovisionable_reason}` : ''}
                            </li>
                        ))}
                    </ul>
                    <p className="mt-2 text-[11px] leading-relaxed text-ink-muted">
                        Planning is blocked here rather than letting it fail partway through. Ask in the chat
                        for something the catalog can build instead.
                    </p>
                </div>
            )}

            {planError && (
                <p className="mt-4 rounded-md border border-edge bg-surface-2 p-3 text-sm text-ink">
                    {planError}
                </p>
            )}

            <button
                onClick={() => void plan()}
                disabled={!config.applicable || planning || frozen || unbuildable.length > 0}
                className="mt-4 w-full rounded-md bg-ink px-4 py-2.5 text-sm font-medium text-surface-0 disabled:cursor-not-allowed disabled:opacity-30"
            >
                {planning ? 'Starting…' : 'Plan this'}
            </button>
            <p className="mt-2 text-center text-[11px] text-ink-faint">
                {frozen
                    ? 'This chat is archived. Unarchive it to plan.'
                    : unbuildable.length > 0
                        ? 'Blocked because part of this configuration has no module to build it.'
                        : 'Planning changes nothing. You approve before anything is created.'}
            </p>
        </div>
    );
}

function ViewTabs({
    view,
    setView,
    count,
    canEdit,
    inline,
}: {
    view: 'proposal' | 'history' | 'edit';
    setView: (v: 'proposal' | 'history' | 'edit') => void;
    count: number;
    canEdit: boolean;
    inline?: boolean;
}) {
    return (
        <div
            className={
                inline
                    ? 'mb-3 flex items-center gap-1'
                    : 'flex items-center gap-1 border-b border-edge px-4 py-3'
            }
        >
            <Tab active={view === 'proposal'} onClick={() => setView('proposal')}>
                Proposal
            </Tab>
            <Tab active={view === 'history'} onClick={() => setView('history')}>
                History{count > 0 ? ` (${count})` : ''}
            </Tab>
            {canEdit && (
                <button
                    onClick={() => setView('edit')}
                    className="ml-auto rounded px-2 py-1 text-[11px] text-ink-faint hover:text-ink"
                >
                    Edit
                </button>
            )}
        </div>
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
            className={`rounded px-2 py-1 text-[11px] ${active ? 'bg-surface-2 text-ink' : 'text-ink-faint hover:text-ink-muted'
                }`}
        >
            {children}
        </button>
    );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
    return (
        <div className="mb-4">
            <h3 className="mb-2 text-[11px] uppercase tracking-wide text-ink-faint">{title}</h3>
            {children}
        </div>
    );
}

function Empty({ children }: { children: React.ReactNode }) {
    return (
        <div className="flex h-full items-center justify-center p-6 text-center">
            <p className="max-w-[16rem] text-sm text-ink-faint">{children}</p>
        </div>
    );
}