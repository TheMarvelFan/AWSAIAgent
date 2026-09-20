import { Component, useEffect, useState, type ReactNode } from 'react';
import { deployments } from '../lib/api';

/**
 * §10.10: the first-run screen offers connecting AWS *and* starting a chat.
 * Neither is a prerequisite for the other, and implying otherwise would send a
 * new user off to AWS before they have seen what the thing does.
 */
export function FirstRun({ onNewChat }: { onNewChat: () => void }) {
    return (
        <div className="flex h-full items-center justify-center p-8">
            <div className="max-w-sm text-center">
                <h2 className="text-base font-medium text-ink">Nothing here yet</h2>
                <p className="mt-2 text-sm leading-relaxed text-ink-muted">
                    Describe infrastructure in plain language and a proposal appears alongside the
                    conversation. Nothing reaches AWS until you read a plan and approve it.
                </p>

                <button
                    onClick={onNewChat}
                    className="mt-6 w-full rounded-md bg-ink px-4 py-2.5 text-sm font-medium text-surface-0"
                >
                    Start a chat
                </button>

                <p className="mt-4 text-[11px] leading-relaxed text-ink-faint">
                    You can connect an AWS account now with the padlock above, or later — proposals work
                    without one, and you only need it to plan.
                </p>
            </div>
        </div>
    );
}

/**
 * §8.9: everything that may still be billing, across every chat. Worth a
 * permanent indicator — a deployment left running in a chat you closed is
 * exactly the thing that costs money quietly.
 */
export function LiveIndicator() {
    const [count, setCount] = useState(0);

    useEffect(() => {
        let cancelled = false;
        const check = async () => {
            try {
                const { deployments: live } = await deployments.live();
                if (!cancelled) setCount(live.length);
            } catch {
                /* a failed check is not worth surfacing here */
            }
        };
        void check();
        const t = setInterval(() => void check(), 30_000);
        return () => {
            cancelled = true;
            clearInterval(t);
        };
    }, []);

    if (count === 0) return null;

    return (
        <span
            title="Deployments that may still be running and billing"
            className="flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm text-ink-muted"
        >
            <span className="size-1.5 rounded-full bg-lock-ok" aria-hidden />
            {count} running
        </span>
    );
}

/**
 * A render error in one panel should not blank the whole app. This is the
 * safety net the ConfigPanel crash went through — that was an empty screen and
 * a console trace, which is a bad way to find out.
 */
export class ErrorBoundary extends Component<
    { children: ReactNode },
    { error: Error | null }
> {
    state: { error: Error | null } = { error: null };

    static getDerivedStateFromError(error: Error) {
        return { error };
    }

    render() {
        if (!this.state.error) return this.props.children;

        return (
            <div className="flex min-h-screen items-center justify-center bg-surface-0 p-8">
                <div className="max-w-md">
                    <h2 className="text-base font-medium text-ink">Something broke while rendering</h2>
                    <p className="mt-2 text-sm text-ink-muted">
                        Nothing was sent to AWS by this failure. Reloading is safe — any deployment in flight
                        keeps running on the server.
                    </p>
                    <pre className="plan-output mt-4 max-h-48 rounded-md border border-edge bg-surface-2 p-3 text-[11px] text-ink-muted">
                        {this.state.error.message}
                    </pre>
                    <button
                        onClick={() => window.location.reload()}
                        className="mt-4 rounded-md bg-ink px-4 py-2 text-sm font-medium text-surface-0"
                    >
                        Reload
                    </button>
                </div>
            </div>
        );
    }
}