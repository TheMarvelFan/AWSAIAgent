import { paramLabel, paramValue, templateLabel } from './labels';
import type { Block, ConfigDocument, DiffChange } from './types';

/**
 * RFC 6901 pointers are precise and unreadable. §10.6 asks for a third
 * rendering — "API memory: 512 MB → 1024 MB" rather than
 * "replace /blocks/0/parameters/memory_mb".
 *
 * Nothing here is destructive in the AWS sense. Removing a block from a
 * proposal changes what would be built; §7 is explicit that reverting does not
 * touch deployed infrastructure. Destruction appears in the plan, not here, so
 * none of this uses the reserved destroy colour.
 */

export type ChangeTone = 'added' | 'removed' | 'changed';

export interface ReadableChange {
  /** Which block or section this belongs to, when it belongs to one. */
  scope: string | null;
  text: string;
  tone: ChangeTone;
  /** The original, for anyone who wants it. */
  path: string;
}

/**
 * §10.6's first rendering problem: v1's diff is a single add at the root
 * carrying the whole document. There is nothing to compare it against, so the
 * honest thing is to show the config rather than pretend it is a change.
 */
export function isWholeDocument(changes: DiffChange[]): boolean {
  return changes.length === 1 && (changes[0].path === '/' || changes[0].path === '');
}

function segments(path: string): string[] {
  return path
    .split('/')
    .slice(1)
    .map((s) => s.replace(/~1/g, '/').replace(/~0/g, '~'));
}

function blockAt(doc: ConfigDocument | null, index: number): Block | undefined {
  return doc?.blocks?.[index];
}

function blockName(
  index: number,
  toDoc: ConfigDocument | null,
  fromDoc: ConfigDocument | null,
): string {
  const block = blockAt(toDoc, index) ?? blockAt(fromDoc, index);
  return block ? templateLabel(block.template_id) : `Block ${index + 1}`;
}

export function describeChange(
  change: DiffChange,
  fromDoc: ConfigDocument | null,
  toDoc: ConfigDocument | null,
): ReadableChange {
  const seg = segments(change.path);
  const tone: ChangeTone =
    change.op === 'add' ? 'added' : change.op === 'remove' ? 'removed' : 'changed';
  const base = { tone, path: change.path };

  // /blocks/...
  if (seg[0] === 'blocks') {
    const index = Number(seg[1]);

    // A whole block appeared or vanished.
    if (seg.length === 2) {
      const block = (change.to ?? change.from) as Block | undefined;
      const label = block?.template_id ? templateLabel(block.template_id) : blockName(index, toDoc, fromDoc);
      return {
        ...base,
        scope: null,
        text:
          change.op === 'add'
            ? `Added ${label}`
            : change.op === 'remove'
              ? `Removed ${label}`
              : `Replaced ${label}`,
      };
    }

    const scope = blockName(index, toDoc, fromDoc);

    // /blocks/0/parameters/memory_mb
    if (seg[2] === 'parameters' && seg[3]) {
      const key = seg[3];
      const label = paramLabel(key);
      if (change.op === 'add') {
        return { ...base, scope, text: `${label} set to ${paramValue(key, change.to)}` };
      }
      if (change.op === 'remove') {
        return { ...base, scope, text: `${label} removed` };
      }
      return {
        ...base,
        scope,
        text: `${label}: ${paramValue(key, change.from)} → ${paramValue(key, change.to)}`,
      };
    }

    if (seg[2] === 'purpose') {
      return { ...base, scope, text: 'Purpose reworded' };
    }
    if (seg[2] === 'depends_on') {
      return { ...base, scope, text: 'Dependencies changed' };
    }
    return { ...base, scope, text: `${paramLabel(seg[2] ?? 'block')} changed` };
  }

  // /out_of_catalog/0
  if (seg[0] === 'out_of_catalog') {
    const item = (change.to ?? change.from) as { need?: string } | undefined;
    const need = item?.need ? `: ${item.need}` : '';
    return {
      ...base,
      scope: 'Catalog gaps',
      text:
        change.op === 'add'
          ? `Recorded an unmet requirement${need}`
          : change.op === 'remove'
            ? `Unmet requirement resolved${need}`
            : `Unmet requirement reworded${need}`,
    };
  }

  if (seg[0] === 'open_questions') {
    const text = typeof (change.to ?? change.from) === 'string' ? `: ${change.to ?? change.from}` : '';
    return {
      ...base,
      scope: 'Open questions',
      text:
        change.op === 'add'
          ? `Question added${text}`
          : change.op === 'remove'
            ? `Question resolved${text}`
            : `Question reworded${text}`,
    };
  }

  if (seg[0] === 'estimated_cost') {
    const which = seg[1] === 'monthly_low' ? 'floor' : seg[1] === 'monthly_high' ? 'ceiling' : seg[1];
    return {
      ...base,
      scope: 'Estimated cost',
      text: `Monthly ${which}: ${change.from ?? '—'} → ${change.to ?? '—'}`,
    };
  }

  if (seg[0] === 'name') return { ...base, scope: null, text: `Renamed to "${change.to}"` };
  if (seg[0] === 'summary') return { ...base, scope: null, text: 'Summary rewritten' };
  if (seg[0] === 'region')
    return { ...base, scope: null, text: `Region: ${change.from} → ${change.to}` };

  // Anything unrecognised keeps its pointer rather than being dropped — a diff
  // that quietly omits a change is worse than one that reads awkwardly.
  return { ...base, scope: null, text: `${change.op} ${change.path}` };
}

export function groupChanges(changes: ReadableChange[]): Array<{
  scope: string | null;
  changes: ReadableChange[];
}> {
  const order: Array<string | null> = [];
  const map = new Map<string | null, ReadableChange[]>();
  for (const c of changes) {
    if (!map.has(c.scope)) {
      map.set(c.scope, []);
      order.push(c.scope);
    }
    map.get(c.scope)!.push(c);
  }
  return order.map((scope) => ({ scope, changes: map.get(scope)! }));
}