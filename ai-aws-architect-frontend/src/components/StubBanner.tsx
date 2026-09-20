import { useCatalog } from '../hooks/useCatalog';

/**
 * §10.11: the stub reasoning engine and stub runner each log a warning at
 * startup, and the UI should surface both. Presenting canned keyword matching
 * as model output, unnoticed, is the worst available outcome.
 *
 * Both facts now come from GET /catalog. Nothing here is a build-time flag,
 * so it cannot go stale against the server it is talking to.
 *
 * The asymmetry worth keeping in view: a simulated plan is visibly simulated,
 * while a canned proposal looks exactly like a real one. That is why the engine
 * is the more dangerous of the two to get wrong.
 */
export function StubBanner() {
    const { stubRunner, stubEngine } = useCatalog();

    if (!stubEngine && !stubRunner) return null;

    return (
        <div className="shrink-0 border-b border-replace/40 bg-replace-bg px-4 py-1.5 text-center text-[11px] leading-relaxed text-replace-ink">
            {stubEngine && <>Proposals are canned keyword matches, not model output. </>}
            {stubRunner && <>Plans and applies are simulated and create nothing.</>}
            {stubEngine && !stubRunner && (
                <strong className="font-medium">Plans and applies are real.</strong>
            )}
        </div>
    );
}