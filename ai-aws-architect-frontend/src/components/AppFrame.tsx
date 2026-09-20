import { useState, type ReactNode } from 'react';
import { useAuth } from '../lib/auth';
import { useConnection } from '../hooks/useConnection';
import { Padlock } from './Padlock';
import { ThemeToggle } from './ThemeToggle';
import { ResizeHandle, usePersistentSize } from './ResizeHandle';
import { StubBanner } from './StubBanner';
import { LiveIndicator } from './Shell';

const LEFT_DEFAULT = 256;
const RIGHT_DEFAULT = 384;
const DEPLOY_DEFAULT = 320;

/**
 * The persistent frame from §10.2: history left, conversation centre, config and
 * deployment stacked right. Both sides collapse, and all three boundaries drag.
 *
 * The separation between "talking" and "the thing that will actually happen" is
 * the point — it makes the propose/approve boundary legible in one frame.
 *
 * The top bar is not in the doc's sketch. The padlock has to live somewhere
 * account-level, and putting it in the left rail would hide it whenever that
 * rail is collapsed — which is exactly when a user is concentrating on a plan.
 */
export function AppFrame({
  history,
  conversation,
  configPanel,
  deploymentPanel,
}: {
  history: ReactNode;
  conversation: ReactNode;
  configPanel: ReactNode;
  deploymentPanel?: ReactNode;
}) {
  const [leftOpen, setLeftOpen] = useState(true);
  const [rightOpen, setRightOpen] = useState(true);
  const [leftWidth, setLeftWidth] = usePersistentSize('left', LEFT_DEFAULT);
  const [rightWidth, setRightWidth] = usePersistentSize('right', RIGHT_DEFAULT);
  const [deployHeight, setDeployHeight] = usePersistentSize('deploy', DEPLOY_DEFAULT);
  const connection = useConnection();
  const { user, logout } = useAuth();

  return (
    <div className="flex h-screen flex-col bg-surface-0 text-ink">
      <StubBanner />
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-edge px-3">
        <div className="flex items-center gap-1">
          <Toggle
            side="left"
            open={leftOpen}
            onClick={() => setLeftOpen((o) => !o)}
            label="chat history"
          />
          <span className="ml-1 text-sm font-medium tracking-tight">AI AWS Architect</span>
        </div>

        <div className="flex items-center gap-2">
          <LiveIndicator />
          <Padlock connection={connection} />
          <ThemeToggle />
          <button
            onClick={() => void logout()}
            title={user?.email ?? undefined}
            className="rounded-md px-2.5 py-1.5 text-sm text-ink-muted hover:bg-surface-2 hover:text-ink"
          >
            Sign out
          </button>
          <Toggle
            side="right"
            open={rightOpen}
            onClick={() => setRightOpen((o) => !o)}
            label="configuration"
          />
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {leftOpen && (
          <>
            <aside
              style={{ width: leftWidth }}
              className="shrink-0 overflow-y-auto bg-surface-1"
            >
              {history}
            </aside>
            <ResizeHandle
              axis="x"
              value={leftWidth}
              onChange={setLeftWidth}
              min={180}
              max={() => Math.min(560, window.innerWidth - 480)}
              reset={LEFT_DEFAULT}
              label="Resize chat history"
            />
          </>
        )}

        <main className="flex min-w-0 flex-1 flex-col">{conversation}</main>

        {rightOpen && (
          <>
            <ResizeHandle
              axis="x"
              invert
              value={rightWidth}
              onChange={setRightWidth}
              min={300}
              max={() => Math.min(900, window.innerWidth - 400)}
              reset={RIGHT_DEFAULT}
              label="Resize configuration panel"
            />
            <aside
              style={{ width: rightWidth }}
              className="flex shrink-0 flex-col overflow-hidden bg-surface-1"
            >
              <div className="min-h-0 flex-1 overflow-y-auto">{configPanel}</div>

              {/* Stacked below rather than a tab beside or a modal over: seeing
                  the proposal and the plan in one frame is the whole pitch
                  (§10.2). Only rendered when a deployment exists. */}
              {deploymentPanel && (
                <>
                  <ResizeHandle
                    axis="y"
                    invert
                    value={deployHeight}
                    onChange={setDeployHeight}
                    min={120}
                    max={() => window.innerHeight - 220}
                    reset={DEPLOY_DEFAULT}
                    label="Resize deployment panel"
                  />
                  <div
                    style={{ height: deployHeight }}
                    className="min-h-0 shrink-0 overflow-y-auto"
                  >
                    {deploymentPanel}
                  </div>
                </>
              )}
            </aside>
          </>
        )}
      </div>
    </div>
  );
}

function Toggle({
  side,
  open,
  onClick,
  label,
}: {
  side: 'left' | 'right';
  open: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      onClick={onClick}
      aria-label={`${open ? 'Hide' : 'Show'} ${label}`}
      className="rounded-md p-1.5 text-ink-faint hover:bg-surface-2 hover:text-ink"
    >
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden>
        <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" stroke="currentColor" strokeWidth="1.3" />
        <line
          x1={side === 'left' ? 6 : 10}
          y1="2.5"
          x2={side === 'left' ? 6 : 10}
          y2="13.5"
          stroke="currentColor"
          strokeWidth="1.3"
        />
        {open && (
          <rect
            x={side === 'left' ? 1.5 : 10}
            y="2.5"
            width="4.5"
            height="11"
            fill="currentColor"
            opacity="0.35"
          />
        )}
      </svg>
    </button>
  );
}