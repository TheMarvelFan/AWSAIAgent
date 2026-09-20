import { useEffect, useRef, useState } from 'react';

export function clamp(n: number, min: number, max: number) {
    return Math.min(max, Math.max(min, n));
}

/** A pane size that survives a reload. Sizes are in CSS pixels. */
export function usePersistentSize(key: string, initial: number) {
    const [size, setSize] = useState<number>(() => {
        try {
            const raw = localStorage.getItem(`aaa.size.${key}`);
            const n = raw === null ? NaN : Number(raw);
            return Number.isFinite(n) ? n : initial;
        } catch {
            return initial;
        }
    });

    useEffect(() => {
        try {
            localStorage.setItem(`aaa.size.${key}`, String(size));
        } catch {
            /* ignore */
        }
    }, [key, size]);

    return [size, setSize] as const;
}

/**
 * A draggable boundary between two panes.
 *
 * `axis: 'x'` is a vertical bar dragged sideways; `axis: 'y'` is a horizontal
 * bar dragged up and down. `invert` is for panes anchored to the right or
 * bottom, where growing the pane means moving the pointer the other way.
 *
 * Arrow keys move it too — a 4px target is hard to hit, and a pane you cannot
 * resize without a mouse is a pane some people cannot resize.
 */
export function ResizeHandle({
    axis,
    invert,
    value,
    onChange,
    min,
    max,
    reset,
    label,
}: {
    axis: 'x' | 'y';
    invert?: boolean;
    value: number;
    onChange: (n: number) => void;
    min: number;
    max: number | (() => number);
    reset?: number;
    label: string;
}) {
    const start = useRef<{ pos: number; size: number } | null>(null);
    const [dragging, setDragging] = useState(false);

    const ceiling = () => (typeof max === 'function' ? max() : max);
    const sign = invert ? -1 : 1;

    // Without this, dragging selects every label it passes over.
    useEffect(() => {
        if (!dragging) return;
        const prev = document.body.style.userSelect;
        document.body.style.userSelect = 'none';
        document.body.style.cursor = axis === 'x' ? 'col-resize' : 'row-resize';
        return () => {
            document.body.style.userSelect = prev;
            document.body.style.cursor = '';
        };
    }, [dragging, axis]);

    return (
        <div
            role="separator"
            aria-orientation={axis === 'x' ? 'vertical' : 'horizontal'}
            aria-label={label}
            aria-valuenow={Math.round(value)}
            aria-valuemin={min}
            tabIndex={0}
            onPointerDown={(e) => {
                e.currentTarget.setPointerCapture(e.pointerId);
                start.current = { pos: axis === 'x' ? e.clientX : e.clientY, size: value };
                setDragging(true);
            }}
            onPointerMove={(e) => {
                if (!start.current) return;
                const now = axis === 'x' ? e.clientX : e.clientY;
                onChange(clamp(start.current.size + sign * (now - start.current.pos), min, ceiling()));
            }}
            onPointerUp={(e) => {
                e.currentTarget.releasePointerCapture(e.pointerId);
                start.current = null;
                setDragging(false);
            }}
            onDoubleClick={() => reset !== undefined && onChange(reset)}
            onKeyDown={(e) => {
                const step = e.shiftKey ? 64 : 16;
                const less = axis === 'x' ? 'ArrowLeft' : 'ArrowUp';
                const more = axis === 'x' ? 'ArrowRight' : 'ArrowDown';
                if (e.key === less || e.key === more) {
                    e.preventDefault();
                    const dir = e.key === more ? 1 : -1;
                    onChange(clamp(value + sign * dir * step, min, ceiling()));
                }
            }}
            className={[
                'group relative flex shrink-0 touch-none items-center justify-center',
                axis === 'x' ? 'w-0.75 cursor-col-resize' : 'h-0.75 cursor-row-resize',
                dragging ? 'bg-accent' : 'bg-edge hover:bg-edge-strong',
                'focus-visible:outline-none',
            ].join(' ')}
        >
            {/* The grab area extends past the visible track, so the target is ~11px
          without the divider looking that heavy. */}
            <div
                className={
                    axis === 'x'
                        ? 'absolute inset-y-0 -left-1 -right-1'
                        : 'absolute inset-x-0 -top-1 -bottom-1'
                }
            />
            {/* A grip marker, so the boundary reads as draggable rather than as a
          border that happens to respond. */}
            <div
                className={[
                    'pointer-events-none absolute rounded-full transition-colors',
                    axis === 'x' ? 'h-8 w-0.75' : 'h-0.75 w-8',
                    dragging ? 'bg-accent' : 'bg-edge-strong group-hover:bg-accent',
                ].join(' ')}
            />
        </div>
    );
}