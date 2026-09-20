import { useEffect, useRef, useState } from 'react';
import type { UseConnection } from '../hooks/useConnection';
import { ConnectDialog, DisconnectDialog } from './ConnectionDialogs';
import type { ConnectionStatus } from '../lib/types';

type Visual = { dot: string; label: string; open: boolean };

const VISUALS: Record<ConnectionStatus | 'checking', Visual> = {
  checking: { dot: 'bg-lock-idle animate-pulse', label: 'Checking', open: true },
  not_connected: { dot: 'bg-lock-idle', label: 'No AWS account', open: true },
  pending: { dot: 'bg-lock-idle', label: 'Setup unfinished', open: true },
  active: { dot: 'bg-lock-ok', label: 'AWS connected', open: false },
  broken: { dot: 'bg-lock-broken', label: 'AWS connection broken', open: true },
};

/**
 * Account-level, so it lives in the frame rather than inside a chat — a new user
 * must be able to connect before creating anything (§10.3).
 *
 * Four states, not two: "never connected" and "connected but broken" call for
 * different responses, and collapsing them would hide the one that matters.
 */
export function Padlock({ connection }: { connection: UseConnection }) {
  const [open, setOpen] = useState(false);
  const [dialog, setDialog] = useState<'connect' | 'disconnect' | null>(null);
  const ref = useRef<HTMLDivElement>(null);

  const status: ConnectionStatus | 'checking' =
    connection.phase === 'checking' ? 'checking' : (connection.state?.status ?? 'not_connected');
  const visual = VISUALS[status];
  const broken = status === 'broken';

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen((o) => !o)}
        aria-label={visual.label}
        className={[
          'flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm transition-colors',
          broken
            ? 'bg-lock-broken-bg text-lock-broken ring-1 ring-lock-broken'
            : 'text-ink-muted hover:bg-surface-2 hover:text-ink',
        ].join(' ')}
      >
        <LockGlyph open={visual.open} />
        <span className={`size-1.5 rounded-full ${visual.dot}`} aria-hidden />
        <span className={broken ? 'font-medium' : ''}>{visual.label}</span>
      </button>

      {open && (
        <div className="absolute right-0 top-full z-40 mt-2 w-80 rounded-lg border border-edge bg-surface-3 p-4 shadow-xl">
          {status === 'checking' && <p className="text-sm text-ink-muted">Checking with AWS…</p>}

          {status === 'not_connected' && (
            <>
              <p className="text-sm text-ink-muted">
                No AWS account is connected. Nothing can be planned or applied until one is.
              </p>
              <Primary onClick={() => setDialog('connect')}>Connect AWS account</Primary>
            </>
          )}

          {status === 'pending' && (
            <>
              <p className="text-sm text-ink-muted">
                The role was never verified, so the setup did not finish.
              </p>
              <Primary onClick={() => setDialog('connect')}>Resume setup</Primary>
            </>
          )}

          {(status === 'active' || status === 'broken') && connection.state?.connection && (
            <>
              {/* Users with more than one account need to see which one they are
                  about to provision into (§10.3). */}
              <Row label="Account" value={connection.state.connection.account_id} mono />
              <Row label="Region" value={connection.state.connection.region} mono />
              <Row label="Role" value={connection.state.connection.role_name} mono />

              {broken && connection.state.failure && (
                <div className="mt-3 rounded-md bg-lock-broken-bg p-3">
                  <p className="text-sm text-ink">{connection.state.failure.message}</p>
                  {connection.state.failure.aws_side && (
                    <p className="mt-1.5 text-xs text-ink-muted">
                      This looks like it is on the AWS side rather than your configuration.
                    </p>
                  )}
                </div>
              )}

              <div className="mt-4 flex gap-2">
                <button
                  onClick={() => void connection.reload(true)}
                  className="flex-1 rounded-md bg-surface-2 px-3 py-2 text-sm text-ink ring-1 ring-edge hover:ring-edge-strong"
                >
                  Re-check
                </button>
                {broken ? (
                  <button
                    onClick={() => setDialog('connect')}
                    className="flex-1 rounded-md bg-ink px-3 py-2 text-sm font-medium text-surface-0"
                  >
                    Reconnect
                  </button>
                ) : (
                  <button
                    onClick={() => setDialog('disconnect')}
                    className="flex-1 rounded-md px-3 py-2 text-sm text-ink-muted hover:text-ink"
                  >
                    Disconnect
                  </button>
                )}
              </div>
            </>
          )}

          {connection.phase === 'error' && (
            <p className="text-sm text-ink-muted">{connection.error}</p>
          )}
        </div>
      )}

      {dialog === 'connect' && (
        <ConnectDialog
          onDone={(state) => {
            connection.set(state);
            setDialog(null);
            setOpen(false);
          }}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'disconnect' && (
        <DisconnectDialog
          onDone={() => {
            setDialog(null);
            setOpen(false);
            void connection.reload(true);
          }}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1">
      <span className="text-xs text-ink-muted">{label}</span>
      <span className={`text-[13px] text-ink ${mono ? 'font-mono' : ''}`}>{value}</span>
    </div>
  );
}

function Primary({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className="mt-4 w-full rounded-md bg-ink px-3 py-2 text-sm font-medium text-surface-0"
    >
      {children}
    </button>
  );
}

/** The glyph changes with the state, so the padlock does not rely on colour. */
function LockGlyph({ open }: { open: boolean }) {
  return (
    <svg width="13" height="15" viewBox="0 0 13 15" fill="none" aria-hidden>
      <rect x="1" y="6" width="11" height="8" rx="1.5" stroke="currentColor" strokeWidth="1.4" />
      <path
        d={open ? 'M3.5 6V4a3 3 0 0 1 5.6-1.5' : 'M3.5 6V4a3 3 0 0 1 6 0v2'}
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}