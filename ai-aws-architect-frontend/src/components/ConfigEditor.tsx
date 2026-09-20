import { useMemo, useState } from 'react';
import { config as configApi } from '../lib/api';
import { isApiError, userMessage } from '../lib/errors';
import { paramLabel, templateLabel } from '../lib/labels';
import {
    constraintHint,
    controlFor,
    mapPathErrors,
    readSpec,
    type ParameterSpec,
} from '../lib/paramSpec';
import { useCatalog } from '../hooks/useCatalog';
import type { ConfigDocument, ConfigVersion, ManualVersionResult } from '../lib/types';

/**
 * Direct edits to the running config, for the two cases the model is worst at:
 * changing a parameter, and removing a block.
 *
 * Adding a block is deliberately not offered — choosing a template from a
 * requirement is reasoning, which is what the chat is for. Nor is free-form
 * JSON: for the audience this is built for, a textarea is a worse interface
 * than the conversation it is meant to complement.
 */
export function ConfigEditor({
    chatId,
    config,
    onSaved,
    onCancel,
}: {
    chatId: string;
    config: ConfigVersion;
    onSaved: (result: ManualVersionResult) => void;
    onCancel: () => void;
}) {
    const { template } = useCatalog();
    const [draft, setDraft] = useState<ConfigDocument>(() => structuredClone(config.document));
    // Number inputs need a string buffer, or typing "5" into "512" fights the
    // controlled value on every keystroke.
    const [raw, setRaw] = useState<Record<string, string>>({});
    const [note, setNote] = useState('');
    const [saving, setSaving] = useState(false);
    const [paramErrors, setParamErrors] = useState<Record<string, string>>({});
    const [blockErrors, setBlockErrors] = useState<Record<number, string>>({});
    const [general, setGeneral] = useState<string[]>([]);

    const dirty = useMemo(
        () => JSON.stringify(draft) !== JSON.stringify(config.document),
        [draft, config.document],
    );

    function setParam(blockIndex: number, key: string, value: unknown) {
        setDraft((prev) => {
            const next = structuredClone(prev);
            const block = next.blocks?.[blockIndex];
            if (block) block.parameters = { ...block.parameters, [key]: value };
            return next;
        });
    }

    function removeBlock(blockIndex: number) {
        setDraft((prev) => {
            const next = structuredClone(prev);
            const removed = next.blocks?.[blockIndex];
            next.blocks = (next.blocks ?? []).filter((_, i) => i !== blockIndex);
            // Leaving a dangling reference behind would just earn a validation error
            // the user cannot see the cause of.
            if (removed) {
                for (const b of next.blocks) {
                    b.depends_on = (b.depends_on ?? []).filter((id) => id !== removed.id);
                }
            }
            return next;
        });
        setRaw({});
    }

    async function save() {
        setSaving(true);
        setParamErrors({});
        setBlockErrors({});
        setGeneral([]);
        try {
            // The server normalises what it stores — region stamped, defaults filled,
            // cost recomputed — so the returned version is the truth, not the draft.
            onSaved(
                await configApi.createVersion(chatId, {
                    document: draft,
                    basedOnVersion: config.version,
                    note: note.trim() || undefined,
                }),
            );
        } catch (e) {
            if (isApiError(e) && e.is('validation_failed')) {
                const mapped = mapPathErrors(e.fields);
                setParamErrors(mapped.byParam);
                setBlockErrors(mapped.byBlock);
                setGeneral(mapped.general);
            } else if (isApiError(e) && e.is('stale_config')) {
                setGeneral([
                    'The configuration moved while you were editing. Close the editor and reopen it to start from the current version.',
                ]);
            } else if (isApiError(e) && e.is('conflict')) {
                setGeneral([
                    'The server would not record this — either nothing changed, or the chat is archived.',
                ]);
            } else {
                setGeneral([userMessage(e)]);
            }
        } finally {
            setSaving(false);
        }
    }

    const blocks = draft.blocks ?? [];

    return (
        <div className="p-4">
            <div className="mb-3">
                <h2 className="text-sm font-medium text-ink">Editing version {config.version}</h2>
                <p className="mt-0.5 text-xs text-ink-muted">
                    Change a setting or remove a block. To add something, ask in the chat.
                </p>
            </div>

            {blocks.length === 0 && (
                <p className="mb-3 border-l-2 border-replace bg-replace-bg/40 py-2 pl-3 text-xs text-replace-ink">
                    Every block has been removed. The server will almost certainly reject an empty
                    configuration.
                </p>
            )}

            <div className="space-y-2">
                {blocks.map((block, i) => {
                    const spec = template(block.template_id);
                    const specs = (spec?.parameters ?? {}) as Record<string, unknown>;
                    const keys = Object.keys(block.parameters ?? {});

                    return (
                        <div key={block.id} className="rounded-md border border-edge bg-surface-2 p-3">
                            <div className="flex items-start justify-between gap-2">
                                <div className="min-w-0">
                                    <span className="text-sm text-ink">{templateLabel(block.template_id)}</span>
                                    <span className="ml-2 font-mono text-[11px] text-ink-faint">{block.id}</span>
                                </div>
                                <button
                                    onClick={() => removeBlock(i)}
                                    className="shrink-0 rounded px-2 py-0.5 text-[11px] text-ink-muted hover:bg-surface-3 hover:text-destroy-ink"
                                >
                                    Remove
                                </button>
                            </div>

                            {blockErrors[i] && (
                                <p className="mt-1.5 text-xs text-destroy-ink">{blockErrors[i]}</p>
                            )}

                            {keys.length > 0 && (
                                <div className="mt-2.5 space-y-2.5 border-t border-edge pt-2.5">
                                    {keys.map((key) => (
                                        <ParamField
                                            key={key}
                                            label={paramLabel(key)}
                                            spec={readSpec(specs[key])}
                                            value={block.parameters[key]}
                                            rawKey={`${i}:${key}`}
                                            raw={raw}
                                            setRaw={setRaw}
                                            error={paramErrors[`${i}:${key}`]}
                                            onChange={(v) => setParam(i, key, v)}
                                        />
                                    ))}
                                </div>
                            )}
                        </div>
                    );
                })}
            </div>

            <label className="mt-4 block text-xs text-ink-muted">Note (optional)</label>
            <input
                value={note}
                maxLength={500}
                onChange={(e) => setNote(e.target.value)}
                placeholder="why you changed it"
                className="mt-1.5 w-full rounded-md border border-edge bg-surface-2 px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-edge-strong"
            />

            {general.length > 0 && (
                <div className="mt-3 rounded-md border border-edge bg-surface-2 p-3">
                    {general.map((m, i) => (
                        <p key={i} className="text-sm text-ink">
                            {m}
                        </p>
                    ))}
                </div>
            )}

            <div className="mt-4 flex gap-2">
                <button
                    onClick={onCancel}
                    className="flex-1 rounded-md px-3 py-2 text-sm text-ink-muted hover:bg-surface-2 hover:text-ink"
                >
                    Discard
                </button>
                <button
                    onClick={() => void save()}
                    disabled={saving || !dirty}
                    className="flex-1 rounded-md bg-ink px-3 py-2 text-sm font-medium text-surface-0 disabled:opacity-40"
                >
                    {saving ? 'Saving…' : 'Save as new version'}
                </button>
            </div>
            <p className="mt-2 text-center text-[11px] leading-relaxed text-ink-faint">
                This appends a version rather than overwriting one, and does not touch anything already
                deployed.
            </p>
        </div>
    );
}

function ParamField({
    label,
    spec,
    value,
    rawKey,
    raw,
    setRaw,
    error,
    onChange,
}: {
    label: string;
    spec: ParameterSpec;
    value: unknown;
    rawKey: string;
    raw: Record<string, string>;
    setRaw: (fn: (prev: Record<string, string>) => Record<string, string>) => void;
    error?: string;
    onChange: (value: unknown) => void;
}) {
    const kind = controlFor(spec, value);
    const hint = constraintHint(spec);
    const field = 'w-full rounded-md border border-edge bg-surface-3 px-2.5 py-1.5 text-[13px] text-ink outline-none focus:border-edge-strong';

    return (
        <div>
            <div className="mb-1 flex items-baseline justify-between gap-2">
                <label className="text-[11px] text-ink-muted">{label}</label>
                {spec.type && <span className="font-mono text-[10px] text-ink-faint">{spec.type}</span>}
            </div>

            {kind === 'boolean' && (
                <label className="flex cursor-pointer items-center gap-2 text-[13px] text-ink">
                    <input
                        type="checkbox"
                        checked={value === true}
                        onChange={(e) => onChange(e.target.checked)}
                    />
                    {value === true ? 'on' : 'off'}
                </label>
            )}

            {kind === 'select' && (
                <select
                    value={String(value ?? '')}
                    onChange={(e) => {
                        const picked = spec.allowed?.find((a) => String(a) === e.target.value);
                        onChange(picked ?? e.target.value);
                    }}
                    className={field}
                >
                    {spec.allowed?.map((option) => (
                        <option key={String(option)} value={String(option)}>
                            {String(option)}
                        </option>
                    ))}
                </select>
            )}

            {kind === 'number' && (
                <input
                    type="number"
                    min={spec.min}
                    max={spec.max}
                    value={raw[rawKey] ?? String(value ?? '')}
                    onChange={(e) => {
                        const text = e.target.value;
                        setRaw((prev) => ({ ...prev, [rawKey]: text }));
                        const n = Number(text);
                        if (text !== '' && Number.isFinite(n)) onChange(n);
                    }}
                    className={`${field} font-mono`}
                />
            )}

            {kind === 'text' && (
                <input
                    type="text"
                    value={String(value ?? '')}
                    onChange={(e) => onChange(e.target.value)}
                    className={`${field} font-mono`}
                />
            )}

            {error ? (
                <p className="mt-1 text-[11px] text-destroy-ink">{error}</p>
            ) : spec.description ? (
                <p className="mt-1 text-[11px] leading-relaxed text-ink-faint">{spec.description}</p>
            ) : hint ? (
                <p className="mt-1 text-[11px] text-ink-faint">{hint}</p>
            ) : null}
        </div>
    );
}