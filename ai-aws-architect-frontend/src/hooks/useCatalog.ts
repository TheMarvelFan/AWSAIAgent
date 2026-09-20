import { useEffect, useState } from 'react';
import { catalog as catalogApi } from '../lib/api';
import { userMessage } from '../lib/errors';
import type { Catalog, CatalogTemplate } from '../lib/types';

/**
 * The catalog is read by three places — the stub banner, the catalog dialog,
 * and the config panel's provisionability check — and it does not change while
 * the page is open. One fetch, shared.
 *
 * It carries two facts the API exposes nowhere else: which runner is active,
 * and whether each template can actually be built by it. Both used to be
 * build-time guesses on the client, which is a second source of truth for
 * something the server already knows.
 */
let cached: Catalog | null = null;
let inFlight: Promise<Catalog> | null = null;

export interface UseCatalog {
  catalog: Catalog | null;
  error: string | null;
  /** True once loaded and the runner is the stub. Null-safe before then. */
  stubRunner: boolean;
  /** True once loaded and proposals are canned rather than model output. */
  stubEngine: boolean;
  template: (id: string) => CatalogTemplate | undefined;
}

export function useCatalog(): UseCatalog {
  const [data, setData] = useState<Catalog | null>(cached);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (cached) return;
    let cancelled = false;

    inFlight ??= catalogApi.get();
    inFlight
      .then((result) => {
        cached = result;
        if (!cancelled) setData(result);
      })
      .catch((e) => {
        // Let a later mount retry rather than caching the failure.
        inFlight = null;
        if (!cancelled) setError(userMessage(e));
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return {
    catalog: data,
    error,
    stubRunner: data?.runner === 'stub',
    stubEngine: data?.reasoning_engine === 'stub',
    template: (id) => data?.templates?.find((t) => t.id === id),
  };
}