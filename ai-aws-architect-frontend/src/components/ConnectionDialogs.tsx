import { useEffect, useState } from 'react';
import { aws } from '../lib/api';
import { isApiError, userMessage } from '../lib/errors';
import type { ConnectStart, ConnectionState } from '../lib/types';

/**
 * Around 90 seconds of the user's time, in someone else's browser tab. The flow
 * is: start (idempotent), open the CloudFormation launch URL, paste back the
 * RoleArn from the stack outputs, verify.
 */
export function ConnectDialog({
  onDone,
  onClose,
}: {
  onDone: (state: ConnectionState) => void;
  onClose: () => void;
}) {
  const [start, setStart] = useState<ConnectStart | null>(null);
  const [arn, setArn] = useState('');
  const [busy, setBusy] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [failure, setFailure] = useState<{ message: string; retryable: boolean } | null>(null);
  const [cooldown, setCooldown] = useState(0);
  const [startError, setStartError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        setStart(await aws.startConnect());
      } catch (e) {
        setStartError(userMessage(e));
      }
    })();
  }, []);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  async function verify() {
    setBusy(true);
    setFieldError(null);
    setFailure(null);
    try {
      onDone(await aws.verify(arn.trim()));
    } catch (e) {
      if (isApiError(e) && e.is('validation_failed')) {
        setFieldError(e.fields.role_arn ?? e.message);
      } else if (isApiError(e) && e.is('upstream_failed')) {
        // The 502 alone cannot tell IAM propagation apart from a permanently
        // wrong trust policy. The connection carries the detail (§4).
        try {
          const after = await aws.connection();
          setFailure({
            message: after.failure?.message ?? userMessage(e),
            retryable: true,
          });
        } catch {
          setFailure({ message: userMessage(e), retryable: true });
        }
        setCooldown(30);
      } else {
        setFailure({ message: userMessage(e), retryable: false });
      }
    } finally {
      setBusy(false);
    }
  }

  const suffixOk =
    !start || arn.trim() === '' || arn.trim().endsWith(start.expected_role_arn_suffix);

  return (
    <Shell title="Connect an AWS account" onClose={onClose}>
      {startError ? (
        <p className="text-sm text-destroy-ink">{startError}</p>
      ) : !start ? (
        <p className="text-sm text-ink-muted">Preparing…</p>
      ) : (
        <div className="space-y-5">
          <ol className="space-y-3 text-sm text-ink-muted">
            <li>
              <span className="text-ink">1.</span> Open the CloudFormation stack in an AWS
              console signed into the account you want to provision into.
            </li>
            <li>
              <span className="text-ink">2.</span> Tick the IAM acknowledgement and create the
              stack.
            </li>
            <li>
              <span className="text-ink">3.</span> Copy <code className="font-mono">RoleArn</code>{' '}
              from the Outputs tab and paste it below.
            </li>
          </ol>

          {/* The launch URL carries the external ID and is a secret. Opened,
              never rendered as text, never logged. */}
          <button
            onClick={() => window.open(start.launch_url, '_blank', 'noopener,noreferrer')}
            className="w-full rounded-md bg-accent-bg px-4 py-2.5 text-sm font-medium text-accent ring-1 ring-accent/40 hover:ring-accent"
          >
            Open the AWS console
          </button>

          <div>
            <label className="mb-1.5 block text-xs text-ink-muted">Role ARN</label>
            <input
              value={arn}
              onChange={(e) => setArn(e.target.value)}
              spellCheck={false}
              placeholder={`arn:aws:iam::000000000000${start.expected_role_arn_suffix}`}
              className="w-full rounded-md border border-edge bg-surface-2 px-3 py-2 font-mono text-[13px] text-ink outline-none placeholder:text-ink-faint focus:border-edge-strong"
            />
            {!suffixOk && (
              <p className="mt-1.5 text-xs text-replace-ink">
                That is not the role this account expects. It should end with{' '}
                <span className="font-mono">{start.expected_role_arn_suffix}</span>.
              </p>
            )}
            {fieldError && <p className="mt-1.5 text-xs text-destroy-ink">{fieldError}</p>}
          </div>

          {failure && (
            <div className="rounded-md border border-edge bg-surface-2 p-3">
              <p className="text-sm text-ink">{failure.message}</p>
              {cooldown > 0 ? (
                <p className="mt-2 text-xs text-ink-muted">
                  A role created moments ago can take up to 30 seconds to become usable. Retry in{' '}
                  {cooldown}s.
                </p>
              ) : (
                <p className="mt-2 text-xs text-ink-muted">
                  If a retry fails the same way, check that the stack was created in the account
                  named in the ARN, and that it completed rather than rolling back.
                </p>
              )}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <button onClick={onClose} className="px-3 py-2 text-sm text-ink-muted hover:text-ink">
              Cancel
            </button>
            <button
              onClick={verify}
              disabled={busy || arn.trim() === '' || cooldown > 0}
              className="rounded-md bg-ink px-4 py-2 text-sm font-medium text-surface-0 disabled:opacity-40"
            >
              {busy ? 'Verifying…' : 'Verify'}
            </button>
          </div>
        </div>
      )}
    </Shell>
  );
}

/**
 * Deleting our row revokes nothing. Both server warnings go on screen, and so do
 * any live resources — disconnecting means auto-teardown can never run and those
 * resources bill indefinitely (§4).
 */
export function DisconnectDialog({
  onDone,
  onClose,
}: {
  onDone: () => void;
  onClose: () => void;
}) {
  const [liveCount, setLiveCount] = useState<number | null>(null);
  const [result, setResult] = useState<{ url: string; warnings: string[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { deployments } = await import('../lib/api').then((m) => m.deployments.live());
        setLiveCount(deployments.length);
      } catch {
        setLiveCount(null);
      }
    })();
  }, []);

  async function disconnect() {
    setBusy(true);
    try {
      const r = await aws.disconnect();
      setResult({ url: r.stack_console_url, warnings: r.warnings });
    } catch (e) {
      setError(userMessage(e));
    } finally {
      setBusy(false);
    }
  }

  if (result) {
    return (
      <Shell title="Disconnected" onClose={onDone}>
        <div className="space-y-4">
          {result.warnings.map((w, i) => (
            <p key={i} className="border-l-2 border-replace pl-3 text-sm text-ink">
              {w}
            </p>
          ))}
          <button
            onClick={() => window.open(result.url, '_blank', 'noopener,noreferrer')}
            className="w-full rounded-md bg-surface-2 px-4 py-2.5 text-sm text-ink ring-1 ring-edge hover:ring-edge-strong"
          >
            Open the stack in CloudFormation
          </button>
          <button
            onClick={onDone}
            className="w-full rounded-md bg-ink px-4 py-2 text-sm font-medium text-surface-0"
          >
            Done
          </button>
        </div>
      </Shell>
    );
  }

  return (
    <Shell title="Disconnect this AWS account?" onClose={onClose}>
      <div className="space-y-4">
        <p className="text-sm text-ink-muted">
          This removes our pointer to your account. It does not revoke anything — the IAM role
          keeps working until you delete the CloudFormation stack yourself.
        </p>

        {liveCount !== null && liveCount > 0 && (
          <div className="rounded-md border-2 border-destroy bg-destroy-bg p-3">
            <p className="text-sm text-destroy-ink">
              {liveCount} deployment{liveCount === 1 ? '' : 's'} may still be running. Disconnecting
              means automatic teardown can never run, and they bill until you remove them by hand.
            </p>
          </div>
        )}

        {error && <p className="text-sm text-destroy-ink">{error}</p>}

        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="px-3 py-2 text-sm text-ink-muted hover:text-ink">
            Cancel
          </button>
          <button
            onClick={disconnect}
            disabled={busy}
            className="rounded-md border-2 border-destroy bg-destroy-bg px-4 py-2 text-sm font-medium text-destroy-ink disabled:opacity-40"
          >
            {busy ? 'Disconnecting…' : 'Disconnect'}
          </button>
        </div>
      </div>
    </Shell>
  );
}

function Shell({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-md rounded-xl border border-edge bg-surface-1 p-6">
        <div className="mb-4 flex items-start justify-between gap-4">
          <h2 className="text-base font-medium text-ink">{title}</h2>
          <button onClick={onClose} aria-label="Close" className="text-ink-faint hover:text-ink">
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}