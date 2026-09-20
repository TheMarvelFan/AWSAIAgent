/**
 * `GET /catalog` is the one endpoint that still omits empty fields, so a
 * parameter spec may be missing anything. Everything here is optional and read
 * defensively — an unrecognised shape degrades to a text field rather than
 * throwing or rendering nothing.
 */
export interface ParameterSpec {
  type?: string;
  description?: string;
  default?: unknown;
  allowed?: unknown[];
  min?: number;
  max?: number;
  pattern?: string;
  required?: boolean;
}

export type ControlKind = 'select' | 'number' | 'boolean' | 'text';

export function readSpec(raw: unknown): ParameterSpec {
  if (raw === null || typeof raw !== 'object') return {};
  const r = raw as Record<string, unknown>;
  return {
    type: typeof r.type === 'string' ? r.type : undefined,
    description: typeof r.description === 'string' ? r.description : undefined,
    default: r.default,
    allowed: Array.isArray(r.allowed) ? r.allowed : undefined,
    min: typeof r.min === 'number' ? r.min : undefined,
    max: typeof r.max === 'number' ? r.max : undefined,
    pattern: typeof r.pattern === 'string' ? r.pattern : undefined,
    required: typeof r.required === 'boolean' ? r.required : undefined,
  };
}

/** The spec decides the control: a select for enums, a bounded number input
 *  for ints, a checkbox for bools, a text field for everything else. */
export function controlFor(spec: ParameterSpec, value: unknown): ControlKind {
  if (spec.allowed && spec.allowed.length > 0) return 'select';

  const t = (spec.type ?? '').toLowerCase();
  if (t === 'bool' || t === 'boolean') return 'boolean';
  if (t === 'int' || t === 'integer' || t === 'number' || t === 'float') return 'number';
  if (t) return 'text';

  // No type given — fall back to what the current value looks like.
  if (typeof value === 'boolean') return 'boolean';
  if (typeof value === 'number') return 'number';
  return 'text';
}

/** Constraints as a short line under the control, when there are any. */
export function constraintHint(spec: ParameterSpec): string | null {
  const bits: string[] = [];
  if (spec.min !== undefined || spec.max !== undefined) {
    bits.push(`${spec.min ?? 'any'} to ${spec.max ?? 'any'}`);
  }
  if (spec.pattern) bits.push(`must match ${spec.pattern}`);
  if (spec.default !== undefined && spec.default !== null) {
    bits.push(`default ${String(spec.default)}`);
  }
  return bits.length > 0 ? bits.join(' · ') : null;
}

/**
 * Validation errors come back keyed by config path — `/blocks/0/parameters/cpu`.
 * Reduce them to `blockIndex:paramKey` so a field can find its own message, and
 * keep anything that does not fit that shape for a panel-level bucket.
 */
export function mapPathErrors(fields: Record<string, string>): {
  byParam: Record<string, string>;
  byBlock: Record<number, string>;
  general: string[];
} {
  const byParam: Record<string, string> = {};
  const byBlock: Record<number, string> = {};
  const general: string[] = [];

  for (const [path, message] of Object.entries(fields)) {
    const seg = path.split('/').slice(1);
    if (seg[0] === 'blocks' && seg[2] === 'parameters' && seg[3]) {
      byParam[`${seg[1]}:${seg[3]}`] = message;
    } else if (seg[0] === 'blocks' && seg[1] !== undefined && seg.length <= 2) {
      byBlock[Number(seg[1])] = message;
    } else {
      general.push(`${path} ${message}`);
    }
  }

  return { byParam, byBlock, general };
}