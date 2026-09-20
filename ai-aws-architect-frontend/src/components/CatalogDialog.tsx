import { useEffect, useRef, useState } from 'react';
import { useCatalog } from '../hooks/useCatalog';
import { paramLabel, templateLabel } from '../lib/labels';
import type { CatalogTemplate } from '../lib/types';

const FADE_MS = 150;

export function CatalogDialog({ onClose }: { onClose: () => void }) {
    const { catalog, error, stubRunner } = useCatalog();
    const [visible, setVisible] = useState(false);
    const panel = useRef<HTMLDivElement>(null);

    // Mount transparent, then fade in on the next frame so the transition runs.
    useEffect(() => {
        const id = requestAnimationFrame(() => setVisible(true));
        return () => cancelAnimationFrame(id);
    }, []);

    function close() {
        setVisible(false);
        setTimeout(onClose, FADE_MS);
    }

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') close();
        };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    });

    return (
        <div
            onMouseDown={(e) => {
                // Only a click that starts outside the panel closes it, so a drag that
                // ends on the backdrop after selecting text does not.
                if (!panel.current?.contains(e.target as Node)) close();
            }}
            className={`fixed inset-0 z-50 flex items-center justify-center p-4 backdrop-blur-sm transition-opacity duration-150 ${visible ? 'bg-black/50 opacity-100' : 'bg-black/0 opacity-0'
                }`}
            role="dialog"
            aria-modal="true"
            aria-label="Service catalog"
        >
            <div
                ref={panel}
                className={`flex max-h-[80vh] w-full max-w-lg flex-col rounded-xl border border-edge bg-surface-1 shadow-2xl transition-all duration-150 ${visible ? 'scale-100 opacity-100' : 'scale-[0.98] opacity-0'
                    }`}
            >
                <div className="flex shrink-0 items-start justify-between gap-4 border-b border-edge px-6 py-4">
                    <div>
                        <h2 className="text-base font-medium text-ink">What can be built</h2>
                        <p className="mt-0.5 text-xs text-ink-muted">
                            Anything outside this list is recorded as a gap rather than guessed at.
                        </p>
                    </div>
                    <button
                        onClick={close}
                        aria-label="Close"
                        className="-mr-2 -mt-1 rounded-md p-1.5 text-ink-faint hover:bg-surface-2 hover:text-ink"
                    >
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden>
                            <path
                                d="M3.5 3.5l9 9m0-9l-9 9"
                                stroke="currentColor"
                                strokeWidth="1.4"
                                strokeLinecap="round"
                            />
                        </svg>
                    </button>
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
                    {error && <p className="text-sm text-ink-muted">{error}</p>}
                    {!error && catalog === null && <p className="text-sm text-ink-faint">Loading…</p>}
                    {catalog?.templates?.length === 0 && (
                        <p className="text-sm text-ink-muted">The catalog is empty.</p>
                    )}

                    {/* Under the stub runner every template reads as buildable, because
              the stub fabricates a plan and never looks for a module. */}
                    {stubRunner && (
                        <p className="mb-3 border-l-2 border-replace bg-replace-bg/40 py-2 pl-3 text-xs text-replace-ink">
                            Plans are simulated right now, so everything below reads as buildable. That will not
                            hold once a real runner is in use.
                        </p>
                    )}

                    <ul className="space-y-3">
                        {catalog?.templates?.map((t) => (
                            <TemplateEntry key={t.id} template={t} />
                        ))}
                    </ul>
                </div>
            </div>
        </div>
    );
}

function TemplateEntry({ template }: { template: CatalogTemplate }) {
    const blocked = template.provisionable === false;
    const params = (template.parameters ?? {}) as Record<string, unknown>;

    return (
        <li className="rounded-md border border-edge bg-surface-2 p-3">
            <div className="flex items-baseline justify-between gap-2">
                <span className="text-sm font-medium text-ink">
                    {template.name ? String(template.name) : templateLabel(template.id)}
                </span>
                <span className="shrink-0 font-mono text-[11px] text-ink-faint">{template.id}</span>
            </div>

            {template.description != null && (
                <p className="mt-1 text-xs leading-relaxed text-ink-muted">
                    {String(template.description)}
                </p>
            )}

            {/* Proposable, validatable, and then it fails at plan time — after the
          user has approved. Worth saying before they ask for it. */}
            {blocked && (
                <p className="mt-2 border-l-2 border-replace bg-replace-bg/40 py-1.5 pl-2.5 text-xs text-replace-ink">
                    Can be proposed but not built
                    {template.unprovisionable_reason ? ` — ${template.unprovisionable_reason}` : '.'}
                </p>
            )}

            {Object.keys(params).length > 0 && (
                <dl className="mt-2 space-y-0.5 border-t border-edge pt-2">
                    {Object.entries(params).map(([key, spec]) => (
                        <div key={key} className="flex justify-between gap-3 text-[11px]">
                            <dt className="text-ink-faint">{paramLabel(key)}</dt>
                            <dd className="text-right font-mono text-ink-muted">{describeSpec(spec)}</dd>
                        </div>
                    ))}
                </dl>
            )}
        </li>
    );
}

/** The catalog's parameter shape is not documented, so read it defensively. */
function describeSpec(spec: unknown): string {
    if (spec === null || spec === undefined) return '—';
    if (typeof spec !== 'object') return String(spec);

    const s = spec as Record<string, unknown>;
    const bits: string[] = [];
    if (s.type != null) bits.push(String(s.type));
    if (Array.isArray(s.allowed)) bits.push(s.allowed.join(' | '));
    if (s.min != null || s.max != null) bits.push(`${s.min ?? '?'}–${s.max ?? '?'}`);
    if (s.default != null) bits.push(`default ${String(s.default)}`);

    return bits.length > 0 ? bits.join(', ') : JSON.stringify(spec);
}