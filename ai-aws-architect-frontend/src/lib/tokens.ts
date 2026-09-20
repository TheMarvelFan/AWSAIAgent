import type { Session } from './types';

/**
 * Token storage.
 *
 * `AllowCredentials: false` on the server rules out an httpOnly refresh cookie
 * (§1.4), so the refresh token has to live somewhere the page can read. We chose
 * localStorage over memory: memory means a full re-login on every page reload,
 * which is painful to develop against and worse to record.
 *
 * The cost is real and accepted rather than hidden — any XSS on this origin can
 * read a 30-day refresh token. That is a documented trade-off for this build,
 * not a secure default. Keep third-party script tags off this app.
 */

const ACCESS_KEY = 'aaa.access_token';
const EXPIRES_KEY = 'aaa.access_expires_at';
const REFRESH_KEY = 'aaa.refresh_token';

export interface StoredTokens {
  accessToken: string;
  /** Absolute expiry of the access token, epoch ms. */
  expiresAt: number;
  refreshToken: string;
}

type Listener = (tokens: StoredTokens | null) => void;
const listeners = new Set<Listener>();

function emit(tokens: StoredTokens | null) {
  for (const fn of listeners) fn(tokens);
}

/** Notified on save and clear, including clears triggered by another tab. */
export function onTokensChanged(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function readTokens(): StoredTokens | null {
  try {
    const accessToken = localStorage.getItem(ACCESS_KEY);
    const refreshToken = localStorage.getItem(REFRESH_KEY);
    const expiresRaw = localStorage.getItem(EXPIRES_KEY);
    if (!accessToken || !refreshToken || !expiresRaw) return null;

    const expiresAt = Number(expiresRaw);
    if (!Number.isFinite(expiresAt)) return null;

    return { accessToken, expiresAt, refreshToken };
  } catch {
    // Private browsing modes can throw on access.
    return null;
  }
}

export function saveSession(session: Session): StoredTokens {
  const tokens: StoredTokens = {
    accessToken: session.access_token,
    expiresAt: new Date(session.expires_at).getTime(),
    refreshToken: session.refresh_token,
  };
  try {
    localStorage.setItem(ACCESS_KEY, tokens.accessToken);
    localStorage.setItem(EXPIRES_KEY, String(tokens.expiresAt));
    localStorage.setItem(REFRESH_KEY, tokens.refreshToken);
  } catch {
    // Storage unavailable — the session still works for this page load.
  }
  emit(tokens);
  return tokens;
}

export function clearTokens() {
  try {
    localStorage.removeItem(ACCESS_KEY);
    localStorage.removeItem(EXPIRES_KEY);
    localStorage.removeItem(REFRESH_KEY);
  } catch {
    /* ignore */
  }
  emit(null);
}

/**
 * When to refresh: 80% of the way through the access token's life (§10.9).
 * Proactive, not reactive on 401 — a 401 means a request already failed.
 */
export function refreshDelayMs(tokens: StoredTokens, now = Date.now()): number {
  const remaining = tokens.expiresAt - now;
  if (remaining <= 0) return 0;
  return Math.max(0, Math.floor(remaining * 0.8));
}

export function isExpired(tokens: StoredTokens, now = Date.now()): boolean {
  return tokens.expiresAt <= now;
}

/**
 * Another tab logging out should log this one out too. Fires only for changes
 * made by other documents, which is exactly what we want.
 */
export function watchCrossTabLogout(onLost: () => void): () => void {
  const handler = (e: StorageEvent) => {
    if (e.key === REFRESH_KEY && e.newValue === null) onLost();
  };
  window.addEventListener('storage', handler);
  return () => window.removeEventListener('storage', handler);
}
