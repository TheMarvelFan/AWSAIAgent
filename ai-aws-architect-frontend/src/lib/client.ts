import { ApiError, NetworkError, TimeoutError, toApiError } from './errors';
import {
  clearTokens,
  isExpired,
  readTokens,
  saveSession,
  type StoredTokens,
} from './tokens';
import type { Session } from './types';

export const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080/v1';
/** Health probes sit outside /v1 (§2). */
export const HEALTH_BASE = import.meta.env.VITE_HEALTH_BASE ?? 'http://localhost:8080';

/** Default ceiling for a request. POST /messages overrides it — see below. */
const DEFAULT_TIMEOUT_MS = 30_000;

/**
 * The server abandons the model call after AGENT_TIMEOUT (90s) and its own write
 * timeout is AGENT_TIMEOUT + 30s, so a client that gives up sooner reports a
 * failure for a turn that is still going to be written (§6).
 */
export const MESSAGE_TIMEOUT_MS = 120_000;

/* ─────────────────────────── Session-lost broadcast ───────────────────────── */

type SessionLostReason = 'invalid_token' | 'refresh_failed' | 'logout' | 'other_tab';
type SessionLostListener = (reason: SessionLostReason) => void;
const sessionLostListeners = new Set<SessionLostListener>();

export function onSessionLost(fn: SessionLostListener): () => void {
  sessionLostListeners.add(fn);
  return () => sessionLostListeners.delete(fn);
}

function loseSession(reason: SessionLostReason) {
  clearTokens();
  for (const fn of sessionLostListeners) fn(reason);
}

/* ──────────────────────────── Single-flight refresh ───────────────────────── */

/**
 * One in-flight refresh at a time (§10.9). The server tolerates two concurrent
 * refreshes inside a 30-second grace window, but relying on that is fragile:
 * outside the window a reused token is read as theft and every session for the
 * user is revoked (§5.2).
 */
let inFlightRefresh: Promise<StoredTokens> | null = null;

export function refreshSession(): Promise<StoredTokens> {
  if (inFlightRefresh) return inFlightRefresh;

  inFlightRefresh = (async () => {
    const current = readTokens();
    if (!current) throw new ApiError({ code: 'invalid_token', message: 'No session.', status: 401 });

    try {
      const session = await rawRequest<Session>('POST', '/auth/refresh', {
        body: { refresh_token: current.refreshToken },
        authenticated: false,
      });
      // The old refresh token is dead the moment this returns. Store the new one
      // immediately (§3).
      return saveSession(session);
    } catch (e) {
      // Never retry a failed refresh. On invalid_token a cascade may have revoked
      // every session for this user, so clear everything and go to login (§5.2).
      if (e instanceof ApiError && e.is('invalid_token')) loseSession('invalid_token');
      else loseSession('refresh_failed');
      throw e;
    } finally {
      inFlightRefresh = null;
    }
  })();

  return inFlightRefresh;
}

/** Refresh only if the token is already dead. Used before a request goes out. */
async function ensureFreshToken(): Promise<StoredTokens | null> {
  const tokens = readTokens();
  if (!tokens) return null;
  if (!isExpired(tokens)) return tokens;
  try {
    return await refreshSession();
  } catch {
    return null;
  }
}

/* ──────────────────────────────── The request ─────────────────────────────── */

interface RequestOptions {
  body?: unknown;
  query?: Record<string, string | number | boolean | undefined | null>;
  /** Default true. False for signup, login, refresh — and to stop 401 recursion. */
  authenticated?: boolean;
  timeoutMs?: number;
  signal?: AbortSignal;
  base?: string;
}

function buildUrl(path: string, query: RequestOptions['query'], base: string): string {
  // Never send a trailing slash: Gin 301-redirects them, which a client that does
  // not follow redirects sees as an unexpected status (§1.5).
  const clean = path.replace(/\/+$/, '');
  const url = new URL(base + clean);
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== null) url.searchParams.set(key, String(value));
  }
  return url.toString();
}

function newRequestId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  return `req-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function rawRequest<T>(
  method: string,
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const {
    body,
    query,
    authenticated = true,
    timeoutMs = DEFAULT_TIMEOUT_MS,
    signal,
    base = API_BASE,
  } = options;

  const headers: Record<string, string> = {
    // Only Authorization, Content-Type and X-Request-ID survive preflight (§1.4).
    'X-Request-ID': newRequestId(),
  };
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  if (authenticated) {
    const tokens = await ensureFreshToken();
    if (tokens) headers.Authorization = `Bearer ${tokens.accessToken}`;
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  if (signal) signal.addEventListener('abort', () => controller.abort(), { once: true });

  let response: Response;
  try {
    response = await fetch(buildUrl(path, query, base), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
      // Cookies are not in play — AllowCredentials is false (§1.4).
      credentials: 'omit',
    });
  } catch (e) {
    if (controller.signal.aborted && !signal?.aborted) throw new TimeoutError();
    if (signal?.aborted) throw e;
    // A CORS rejection is indistinguishable from an offline server here: the
    // browser fails before our code runs and gives no readable reason (§1.4).
    throw new NetworkError();
  } finally {
    clearTimeout(timer);
  }

  if (!response.ok) throw await toApiError(response);

  if (response.status === 204) return null as T;

  const text = await response.text();
  if (!text) return null as T;
  return JSON.parse(text) as T;
}

/**
 * The public entry point. Adds one retry after a refresh when a request comes
 * back `unauthorized` — the token expired between the freshness check and the
 * server reading it (§1.3).
 */
export async function request<T>(
  method: string,
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  try {
    return await rawRequest<T>(method, path, options);
  } catch (e) {
    const retryable =
      e instanceof ApiError && e.is('unauthorized') && (options.authenticated ?? true);
    if (!retryable) throw e;

    try {
      await refreshSession();
    } catch {
      throw e; // refreshSession already cleared the session and notified listeners
    }
    return rawRequest<T>(method, path, options);
  }
}

export const http = {
  get: <T>(path: string, options?: RequestOptions) => request<T>('GET', path, options),
  post: <T>(path: string, options?: RequestOptions) => request<T>('POST', path, options),
  patch: <T>(path: string, options?: RequestOptions) => request<T>('PATCH', path, options),
  // No PUT: the server's AllowMethods does not include it (§1.4).
  del: <T>(path: string, options?: RequestOptions) => request<T>('DELETE', path, options),
};

export function signOutLocally(reason: SessionLostReason = 'logout') {
  loseSession(reason);
}
