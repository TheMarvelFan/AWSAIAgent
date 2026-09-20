import { useCallback, useEffect, useState } from 'react';
import { aws } from '../lib/api';
import type { ConnectionState } from '../lib/types';

export type ConnectionPhase = 'checking' | 'ready' | 'error';

export interface UseConnection {
  phase: ConnectionPhase;
  state: ConnectionState | null;
  error: string | null;
  /** `force` runs a live STS check instead of using the 60s cached result. */
  reload: (force?: boolean) => Promise<void>;
  set: (state: ConnectionState) => void;
}

/**
 * The connection is account-level, not per chat, so this is fetched once near
 * the root and read wherever it is needed.
 *
 * `checking` exists as a real phase because without it a slow network flashes
 * the broken state on every page load (§10.3).
 */
export function useConnection(): UseConnection {
  const [state, setState] = useState<ConnectionState | null>(null);
  const [phase, setPhase] = useState<ConnectionPhase>('checking');
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async (force = false) => {
    setPhase((p) => (p === 'ready' ? p : 'checking'));
    try {
      const next = await aws.connection(force);
      setState(next);
      setError(null);
      setPhase('ready');
    } catch (e) {
      // "Not connected" is a state, not an error — anything thrown here is a
      // real failure to reach the server (§4).
      setError(e instanceof Error ? e.message : 'Could not read the connection.');
      setPhase('error');
    }
  }, []);

  // A stored "verified" flag proves nothing: the customer can delete the role at
  // any time without telling us. Discovering a dead connection on page load is
  // fine; discovering it when Apply is pressed is not (§4).
  useEffect(() => {
    void reload();
  }, [reload]);

  return { phase, state, error, reload, set: setState };
}
