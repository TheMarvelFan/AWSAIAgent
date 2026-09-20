import { useEffect, useState } from 'react';
import { deployments as api } from '../lib/api';
import type { UseDeployment } from '../hooks/useDeployment';
import type { Deployment, DeploymentStatus, Job } from '../lib/types';
import { NEEDS_ATTENTION_STATUSES, shouldPoll } from '../lib/types';
import { PlanReadout } from './PlanReadout';

const STATUS_LABEL: Record<DeploymentStatus, string> = {
  planning: 'Planning',
  awaiting_approval: 'Waiting for your approval',
  applying: 'Applying',
  applied: 'Applied',
  superseded: 'Superseded by a later deployment',
  plan_failed: 'Plan failed',
  apply_failed: 'Apply failed',
  destroying: 'Destroying',
  destroyed: 'Destroyed',
  destroy_failed: 'Teardown failed',
  cancelled: 'Cancelled',
  unknown: 'Outcome unknown',
};

export function DeploymentPanel({
  state,
  currentConfigVersion,
  frozen,
  onRePlan,
}: {
  state: UseDeployment;
  currentConfigVersion: number | null;
  /** Archived chat. Approving would create resources from a frozen chat. */
  frozen?: boolean;
  onRePlan: () => void;
}) {
  const [approving, setApproving] = useState(false);
  const [resolveOpen, setResolveOpen] = useState(false);
  const d = state.deployment;

  if (!d) {
    return (
      <div className="p-4 text-sm text-ink-faint">
        {state.error ?? (state.loading ? 'Loading deployment…' : 'No deployment.')}
      </div>
    );
  }

  const summary = d.plan_summary;
  const destructive = summary ? summary.replace + summary.destroy : 0;
  const names = summary?.destructive_resources ?? [];
  const needsAttention = NEEDS_ATTENTION_STATUSES.includes(d.status);
  // Reading a plan for v5 while the panel has moved to v7 is legal — the hash
  // binds to the plan, not the current version — but it must be said (§10.7).
  const drifted =
    d.status === 'awaiting_approval' &&
    currentConfigVersion !== null &&
    currentConfigVersion !== d.config_version;

  async function approve() {
    if (!d?.plan_hash) return;
    setApproving(true);
    try {
      await state.apply(() => api.approve(d.id, d.plan_hash!));
    } finally {
      setApproving(false);
    }
  }

  return (
    <div className="p-4">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h2
          className={`text-sm font-medium ${
            d.status === 'unknown'
              ? 'text-replace-ink'
              : needsAttention
                ? 'text-destroy-ink'
                : 'text-ink'
          }`}
        >
          {STATUS_LABEL[d.status]}
        </h2>
        <span className="shrink-0 text-[11px] text-ink-faint">
          v{d.config_version}
          {shouldPoll(d.status) && <Elapsed since={d.created_at} />}
        </span>
      </div>

      {d.aws_account_id && (
        <p className="mb-3 font-mono text-[11px] text-ink-faint">
          {d.aws_account_id} · {d.region}
        </p>
      )}

      {/* "We do not know" is not a failure. It gets the replace colour rather
          than the destroy one, and the honest next actions (§5.1). */}
      {d.status === 'unknown' && (
        <div className="mb-3 border-l-2 border-replace bg-replace-bg/40 py-2 pl-3">
          <p className="text-sm text-replace-ink">
            {d.failure_message ??
              'We cannot tell what was created, so nothing is being assumed.'}
          </p>
          <p className="mt-1 text-xs text-ink-muted">
            Run a plan to ask AWS what exists, tear down what might be there, or state that you
            checked and it is empty.
          </p>
        </div>
      )}

      {needsAttention && d.status !== 'unknown' && d.failure_message && (
        <div className="mb-3 rounded-md border-2 border-destroy bg-destroy-bg p-3">
          <p className="text-sm text-destroy-ink">{d.failure_message}</p>
          {d.failure_code && (
            <p className="mt-1 font-mono text-[11px] text-ink-faint">{d.failure_code}</p>
          )}
        </div>
      )}

      {d.may_have_live_resources && d.status !== 'applied' && (
        <p className="mb-3 text-xs text-destroy-ink">
          Resources may exist from this deployment and may still be billing.
        </p>
      )}

      {summary && (
        <div className="mb-3 grid grid-cols-4 gap-1.5">
          <Count n={summary.add} label="add" />
          <Count n={summary.change} label="change" />
          <Count n={summary.replace} label="replace" tone="replace" />
          <Count n={summary.destroy} label="destroy" tone="destroy" />
        </div>
      )}

      {/* A replace is destroy-then-create. Naming the resources is the difference
          between "a setting changes" and "your data goes" (§8.3). */}
      {destructive > 0 && (
        <div className="mb-3 rounded-md border-2 border-destroy bg-destroy-bg p-3">
          <p className="text-sm font-medium text-destroy-ink">
            {destructive} resource{destructive === 1 ? '' : 's'} will be destroyed or replaced.
          </p>
          <p className="mt-1 text-xs text-destroy-ink/80">
            A replace deletes the resource and creates a new one. Anything stored in it is gone.
          </p>
          {names.length > 0 && (
            <ul className="mt-2 space-y-0.5">
              {names.map((n) => (
                <li key={n} className="font-mono text-[11px] text-destroy-ink">
                  {n}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {drifted && (
        <div className="mb-3 border-l-2 border-replace bg-replace-bg/40 py-2 pl-3">
          <p className="text-sm text-replace-ink">
            This plan is for version {d.config_version}. Your configuration has since moved to
            version {currentConfigVersion}. Approving applies version {d.config_version}.
          </p>
          <button onClick={onRePlan} className="mt-1.5 text-xs text-ink-muted hover:text-ink">
            Plan the current version instead
          </button>
        </div>
      )}

      {d.teardown_after && <Teardown at={d.teardown_after} />}

      <Jobs jobs={state.jobs} />

      {d.plan_output && <PlanReadout output={d.plan_output} />}

      {state.error && (
        <p className="mb-3 rounded-md border border-edge bg-surface-2 p-3 text-sm text-ink">
          {state.error}
        </p>
      )}

      <Actions
        d={d}
        frozen={frozen}
        approving={approving}
        onApprove={approve}
        onDestroy={() => void state.apply(() => api.destroy(d.id))}
        onCancel={() => void state.apply(() => api.cancel(d.id))}
        onResolve={() => setResolveOpen(true)}
      />

      {resolveOpen && (
        <ResolveDialog
          deployment={d}
          onClose={() => setResolveOpen(false)}
          onConfirm={async (note) => {
            await state.apply(() => api.resolve(d.id, note));
            setResolveOpen(false);
          }}
        />
      )}
    </div>
  );
}

function Actions({
  d,
  frozen,
  approving,
  onApprove,
  onDestroy,
  onCancel,
  onResolve,
}: {
  d: Deployment;
  frozen?: boolean;
  approving: boolean;
  onApprove: () => void;
  onDestroy: () => void;
  onCancel: () => void;
  onResolve: () => void;
}) {
  // Destroy, cancel and resolve stay available on an archived chat. Freezing a
  // chat must never strand live resources — the only thing archiving blocks is
  // creating more.
  const canDestroy = ['applied', 'apply_failed', 'destroy_failed', 'unknown', 'superseded'].includes(
    d.status,
  );
  const canCancel = ['planning', 'awaiting_approval', 'plan_failed'].includes(d.status);
  const canResolve = ['unknown', 'apply_failed', 'destroy_failed'].includes(d.status);

  return (
    <div className="space-y-2">
      {d.status === 'awaiting_approval' && (
        <>
          <button
            onClick={onApprove}
            disabled={approving || frozen || !d.plan_hash}
            className="w-full rounded-md bg-ink px-4 py-2.5 text-sm font-medium text-surface-0 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {approving ? 'Applying…' : 'Approve and apply'}
          </button>
          {frozen && (
            <p className="text-center text-[11px] text-ink-faint">
              This chat is archived. Unarchive it to apply.
            </p>
          )}
        </>
      )}

      <div className="flex gap-2">
        {canCancel && (
          <button
            onClick={onCancel}
            className="flex-1 rounded-md px-3 py-2 text-sm text-ink-muted hover:bg-surface-2 hover:text-ink"
          >
            {d.status === 'planning' ? 'Cancel plan' : 'Cancel'}
          </button>
        )}
        {canDestroy && (
          <button
            onClick={onDestroy}
            className="flex-1 rounded-md border-2 border-destroy bg-destroy-bg px-3 py-2 text-sm font-medium text-destroy-ink"
          >
            Destroy
          </button>
        )}
        {canResolve && (
          <button
            onClick={onResolve}
            className="flex-1 rounded-md border-l-2 border-resolve bg-resolve-bg px-3 py-2 text-sm text-resolve-ink"
          >
            Already empty
          </button>
        )}
      </div>
    </div>
  );
}

/**
 * An assertion, not a dismissal. The user is stating they checked AWS — a wrong
 * answer leaves real resources billing, so it takes a typed confirmation (§8.7).
 */
function ResolveDialog({
  deployment,
  onClose,
  onConfirm,
}: {
  deployment: Deployment;
  onClose: () => void;
  onConfirm: (note: string) => Promise<void>;
}) {
  const [typed, setTyped] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-md rounded-xl border border-edge bg-surface-1 p-6">
        <h2 className="text-base font-medium text-ink">State that nothing remains</h2>

        <p className="mt-2 text-sm text-ink-muted">
          This records that <em>you</em> checked the AWS console and found nothing left. Nothing is
          torn down by us. If resources do still exist, they will keep billing and we will have
          stopped tracking them.
        </p>

        <div className="mt-4 border-l-2 border-resolve bg-resolve-bg py-2 pl-3">
          <p className="text-xs text-resolve-ink">
            Last known: {STATUS_LABEL[deployment.status]}
            {deployment.failure_message ? ` — ${deployment.failure_message}` : ''}
          </p>
        </div>

        <label className="mt-4 block text-xs text-ink-muted">
          Type <span className="font-mono text-ink">I checked</span> to confirm
        </label>
        <input
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="mt-1.5 w-full rounded-md border border-edge bg-surface-2 px-3 py-2 text-sm text-ink outline-none focus:border-edge-strong"
        />

        <label className="mt-3 block text-xs text-ink-muted">Note (optional)</label>
        <input
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="checked the console, nothing remains"
          className="mt-1.5 w-full rounded-md border border-edge bg-surface-2 px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-edge-strong"
        />

        <div className="mt-5 flex justify-end gap-2">
          <button onClick={onClose} className="px-3 py-2 text-sm text-ink-muted hover:text-ink">
            Cancel
          </button>
          <button
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm(note);
              } finally {
                setBusy(false);
              }
            }}
            disabled={typed.trim().toLowerCase() !== 'i checked' || busy}
            className="rounded-md border-l-2 border-resolve bg-resolve-bg px-4 py-2 text-sm font-medium text-resolve-ink disabled:opacity-40"
          >
            {busy ? 'Recording…' : 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  );
}

function Count({ n, label, tone }: { n: number; label: string; tone?: 'replace' | 'destroy' }) {
  const loud = (tone === 'destroy' || tone === 'replace') && n > 0;
  const cls =
    tone === 'destroy' && loud
      ? 'border-2 border-destroy bg-destroy-bg text-destroy-ink'
      : tone === 'replace' && loud
        ? 'border-2 border-dashed border-replace bg-replace-bg text-replace-ink'
        : 'border border-edge bg-surface-2 text-ink-muted';

  return (
    <div className={`rounded-md px-2 py-1.5 text-center ${cls}`}>
      <div className="font-mono text-base leading-tight">{n}</div>
      <div className="text-[10px]">{label}</div>
    </div>
  );
}

/** "attempt 2 of 3, retrying" beats a spinner that looks stuck (§5.6). */
function Jobs({ jobs }: { jobs: Job[] }) {
  const active = jobs.find((j) => j.status === 'running' || j.status === 'pending');
  if (!active) return null;

  return (
    <p className="mb-3 text-xs text-ink-muted">
      {active.type} · attempt {active.attempts} of {active.max_attempts}
      {active.last_error ? ` — retrying after ${active.last_error}` : ''}
    </p>
  );
}

/** Resources vanishing without warning is worse than resources existing (§8.10). */
function Teardown({ at }: { at: string }) {
  const [left, setLeft] = useState(() => Date.parse(at) - Date.now());

  useEffect(() => {
    const t = setInterval(() => setLeft(Date.parse(at) - Date.now()), 1000);
    return () => clearInterval(t);
  }, [at]);

  if (left <= 0) {
    return <p className="mb-3 text-xs text-ink-muted">Automatic teardown is due.</p>;
  }

  const mins = Math.floor(left / 60000);
  const secs = Math.floor((left % 60000) / 1000);

  return (
    <p className="mb-3 text-xs text-ink-muted">
      Automatic teardown in{' '}
      <span className="font-mono text-ink">
        {mins}m {String(secs).padStart(2, '0')}s
      </span>
    </p>
  );
}

function Elapsed({ since }: { since: string }) {
  const [secs, setSecs] = useState(() => Math.floor((Date.now() - Date.parse(since)) / 1000));
  useEffect(() => {
    const t = setInterval(() => setSecs(Math.floor((Date.now() - Date.parse(since)) / 1000)), 1000);
    return () => clearInterval(t);
  }, [since]);
  const m = Math.floor(secs / 60);
  return <span className="ml-2 font-mono">{m > 0 ? `${m}m ${secs % 60}s` : `${secs}s`}</span>;
}