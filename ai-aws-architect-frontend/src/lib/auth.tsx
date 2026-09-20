import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { onSessionLost, refreshSession, signOutLocally } from './client';
import { auth as authApi } from './api';
import { ApiError } from './errors';
import { readTokens, refreshDelayMs, watchCrossTabLogout } from './tokens';
import type { User } from './types';

type AuthStatus = 'loading' | 'authenticated' | 'anonymous';

interface AuthValue {
  status: AuthStatus;
  user: User | null;
  login: (email: string, password: string) => Promise<void>;
  signup: (email: string, password: string, displayName?: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('loading');
  const [user, setUser] = useState<User | null>(null);
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopRefreshLoop = useCallback(() => {
    if (refreshTimer.current) clearTimeout(refreshTimer.current);
    refreshTimer.current = null;
  }, []);

  /**
   * Refresh proactively at ~80% of the access token's life, not reactively on a
   * 401 (§10.9). A 401 means a request already failed; the point is that none do.
   */
  const scheduleRefresh = useCallback(() => {
    stopRefreshLoop();
    const tokens = readTokens();
    if (!tokens) return;

    refreshTimer.current = setTimeout(async () => {
      try {
        await refreshSession();
        scheduleRefresh();
      } catch {
        // refreshSession has already cleared the session and told listeners.
        // Never retry a failed refresh.
      }
    }, refreshDelayMs(tokens));
  }, [stopRefreshLoop]);

  // Bootstrap: a token in storage is a claim, so verify it before rendering the app.
  useEffect(() => {
    let cancelled = false;

    (async () => {
      if (!readTokens()) {
        if (!cancelled) setStatus('anonymous');
        return;
      }
      try {
        const me = await authApi.me();
        if (cancelled) return;
        setUser(me);
        setStatus('authenticated');
        scheduleRefresh();
      } catch (e) {
        if (cancelled) return;
        if (e instanceof ApiError) signOutLocally('refresh_failed');
        setStatus('anonymous');
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [scheduleRefresh]);

  // The client clears tokens on invalid_token; the UI has to follow it to login.
  useEffect(() => {
    const unsubscribe = onSessionLost(() => {
      stopRefreshLoop();
      setUser(null);
      setStatus('anonymous');
    });
    return unsubscribe;
  }, [stopRefreshLoop]);

  // A cascade revokes every session for the user, so a sibling tab losing its
  // session is a signal this one has lost it too (§5.2).
  useEffect(() => watchCrossTabLogout(() => signOutLocally('other_tab')), []);

  useEffect(() => stopRefreshLoop, [stopRefreshLoop]);

  const value = useMemo<AuthValue>(
    () => ({
      status,
      user,
      async login(email, password) {
        const session = await authApi.login(email, password);
        setUser(session.user);
        setStatus('authenticated');
        scheduleRefresh();
      },
      async signup(email, password, displayName) {
        const session = await authApi.signup(email, password, displayName);
        setUser(session.user);
        setStatus('authenticated');
        scheduleRefresh();
      },
      async logout() {
        stopRefreshLoop();
        await authApi.logout();
        setUser(null);
        setStatus('anonymous');
      },
    }),
    [status, user, scheduleRefresh, stopRefreshLoop],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error('useAuth must be used inside AuthProvider');
  return value;
}

/** Renders children once there is a verified session, `fallback` otherwise. */
export function RequireAuth({
  children,
  fallback,
  pending,
}: {
  children: ReactNode;
  fallback: ReactNode;
  pending?: ReactNode;
}) {
  const { status } = useAuth();
  if (status === 'loading') return <>{pending ?? null}</>;
  if (status === 'anonymous') return <>{fallback}</>;
  return <>{children}</>;
}
