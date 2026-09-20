/**
 * THROWAWAY. Delete once the checks pass.
 *
 * Exercises the foundation layer against a running server: CORS, the error
 * envelope, the Authorization header, token rotation, and the 204 path.
 */
import { useState } from 'react';
import { aws, auth, chats, config, deployments, health } from './lib/api';
import { refreshSession } from './lib/client';
import { isApiError, userMessage } from './lib/errors';
import { readTokens } from './lib/tokens';

type Result = { name: string; ok: boolean; detail: string };

const EMAIL = `smoke+${Date.now()}@example.com`;
const PASSWORD = 'smoke-test-password';

export default function App() {
  const [results, setResults] = useState<Result[]>([]);
  const [running, setRunning] = useState(false);

  const push = (r: Result) => setResults((prev) => [...prev, r]);

  async function check(name: string, fn: () => Promise<string>) {
    try {
      push({ name, ok: true, detail: await fn() });
    } catch (e) {
      push({ name, ok: false, detail: userMessage(e) });
    }
  }

  async function run() {
    setResults([]);
    setRunning(true);

    // Server is up, and CORS allows this origin. A NetworkError here is almost
    // always CORS rather than a down server.
    await check('Health probe', async () => {
      const r = await health.ready();
      return `${r.status} / database ${r.database}`;
    });

    // POST with a JSON body, and the session gets stored.
    await check('Signup', async () => {
      const s = await auth.signup(EMAIL, PASSWORD, 'Smoke Test');
      return `${s.user.email}, access token expires ${s.expires_at}`;
    });

    // Proves the Authorization header survives preflight and is accepted.
    await check('Authenticated request', async () => {
      const me = await auth.me();
      return `id ${me.id}`;
    });

    // Deliberate 422. Proves the error envelope parses, `fields` arrives, and
    // X-Request-ID is readable — which only works because it is in ExposeHeaders.
    await check('Error envelope', async () => {
      try {
        await auth.signup(`short+${Date.now()}@example.com`, 'tooshort');
        throw new Error('expected a 422, got a success');
      } catch (e) {
        if (!isApiError(e)) throw e;
        const id = e.requestId ?? 'MISSING — check ExposeHeaders';
        return `${e.code}, fields: ${Object.keys(e.fields).join(', ') || 'none'}, request id: ${id}`;
      }
    });

    // Rotation. The old refresh token is dead after this; if storage did not
    // take the new one, the next refresh fails.
    await check('Token rotation', async () => {
      const before = readTokens()?.refreshToken;
      await refreshSession();
      const after = readTokens()?.refreshToken;
      if (!after) throw new Error('no token stored after refresh');
      if (before === after) throw new Error('refresh token did not rotate');
      return 'rotated and stored';
    });

    // Rotating twice in a row proves the stored token is the live one.
    await check('Second rotation', async () => {
      await refreshSession();
      return 'still valid';
    });

    // A state, not an error — this must not throw on a fresh account.
    await check('AWS connection', async () => {
      const c = await aws.connection();
      return `status ${c.status}`;
    });

    // The 204 path. A new chat has no config; this must return null.
    await check('Empty config returns null', async () => {
      const chat = await chats.create({ title: 'Smoke test' });
      const current = await config.current(chat.id);
      if (current !== null) throw new Error('expected null from a 204');
      return 'null, as expected';
    });

    // Empty list, never null.
    await check('Live deployments', async () => {
      const live = await deployments.live();
      return `${live.deployments.length} live`;
    });

    setRunning(false);
  }

  return (
    <div className="mx-auto max-w-2xl p-8 font-sans">
      <h1 className="text-xl font-semibold">Foundation smoke test</h1>
      <p className="mt-1 text-sm text-neutral-500">
        Creates a throwaway account and one chat. Run against a dev database only.
      </p>

      <button
        onClick={run}
        disabled={running}
        className="mt-6 rounded bg-neutral-900 px-4 py-2 text-sm text-white disabled:opacity-50"
      >
        {running ? 'Running' : 'Run checks'}
      </button>

      <ul className="mt-6 space-y-2">
        {results.map((r, i) => (
          <li key={i} className="rounded border border-neutral-200 p-3 text-sm">
            <span className={r.ok ? 'text-green-700' : 'text-red-700'}>{r.ok ? 'pass' : 'FAIL'}</span>
            <span className="ml-2 font-medium">{r.name}</span>
            <div className="mt-1 text-neutral-600">{r.detail}</div>
          </li>
        ))}
      </ul>
    </div>
  );
}
