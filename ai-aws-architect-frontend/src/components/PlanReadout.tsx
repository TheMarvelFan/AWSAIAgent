import { useMemo, useState } from 'react';
import { groupByModule, humanizeModule, parsePlan, type PlanAction } from '../lib/planParser';

const ACTION_LABEL: Record<PlanAction, string> = {
    create: 'create',
    update: 'change',
    replace: 'replace',
    destroy: 'destroy',
    read: 'read',
};

/** Only the two destructive actions get a signal colour. */
function actionClass(action: PlanAction): string {
    if (action === 'destroy') return 'border-2 border-destroy bg-destroy-bg text-destroy-ink';
    if (action === 'replace')
        return 'border-2 border-dashed border-replace bg-replace-bg text-replace-ink';
    return 'border border-edge bg-surface-3 text-ink-muted';
}

export function PlanReadout({ output }: { output: string }) {
    const [raw, setRaw] = useState(false);
    const groups = useMemo(() => groupByModule(parsePlan(output)), [output]);
    // If parsing found nothing, the plan is in a shape we do not recognise and
    // the raw text is the only honest thing to show.
    const parsed = groups.length > 0;

    return (
        <div className="mb-3 rounded-md border border-edge bg-surface-2">
            <div className="flex items-center justify-between border-b border-edge px-3 py-2">
                <span className="text-[11px] uppercase tracking-wide text-ink-faint">Terraform plan</span>
                {parsed && (
                    <div className="flex rounded border border-edge">
                        <Tab active={!raw} onClick={() => setRaw(false)}>
                            Summary
                        </Tab>
                        <Tab active={raw} onClick={() => setRaw(true)}>
                            Raw
                        </Tab>
                    </div>
                )}
            </div>

            {raw || !parsed ? (
                <div className="plan-output max-h-80 px-3 py-2 text-ink-muted">{output}</div>
            ) : (
                <div className="max-h-80 overflow-y-auto px-3 py-2">
                    {groups.map((group) => (
                        <div key={group.module} className="mb-3 last:mb-0">
                            <p className="mb-1.5 text-[11px] text-ink-faint">{humanizeModule(group.module)}</p>
                            <ul className="space-y-1">
                                {group.resources.map((r) => (
                                    <li key={r.address} className="flex items-baseline gap-2">
                                        <span
                                            className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] ${actionClass(r.action)}`}
                                        >
                                            {ACTION_LABEL[r.action]}
                                        </span>
                                        <span className="min-w-0 text-[13px] text-ink">
                                            {r.kind}
                                            {r.name && (
                                                <span className="ml-1.5 break-all font-mono text-[11px] text-ink-faint">
                                                    {r.name}
                                                </span>
                                            )}
                                        </span>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    ))}
                    <p className="mt-3 border-t border-edge pt-2 text-[11px] text-ink-faint">
                        A reading of the plan below it, not a replacement for it. Switch to Raw for the exact
                        text Terraform produced.
                    </p>
                </div>
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
            className={`px-2 py-0.5 text-[11px] ${active ? 'bg-surface-3 text-ink' : 'text-ink-faint hover:text-ink-muted'
                }`}
        >
            {children}
        </button>
    );
}