import { useCallback, useEffect, useRef, useState } from 'react';
import { deployments } from '../lib/api';
import { userMessage } from '../lib/errors';
import { shouldPoll } from '../lib/types';
import type { Deployment, Job } from '../lib/types';

const POLL_MS = 4000;

export interface UseDeployment {
  deployment: Deployment | null;
  jobs: Job[];
  error: string | null;
  loading: boolean;
  refresh: () => Promise<void>;
  apply: (fn: () => Promise<{ deployment: Deployment; jobs: Job[] }>) => Promise<void>;
}

/**
 * Polls while the deployment is in flight and stops on anything settled (§1.6).
 * There is no push channel, so this is the only way the UI learns that a plan
 * finished or an apply failed.
 */
export function useDeployment(deploymentId: string | null): UseDeployment {
  const [deployment, setDeployment] = useState<Deployment | null>(null);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const refresh = useCallback(async () => {
    if (!deploymentId) return;
    try {
      const result = await deployments.get(deploymentId);
      setDeployment(result.deployment);
      setJobs(result.jobs ?? []);
      setError(null);
    } catch (e) {
      setError(userMessage(e));
    }
  }, [deploymentId]);

  useEffect(() => {
    setDeployment(null);
    setJobs([]);
    setError(null);
    if (!deploymentId) return;

    let cancelled = false;

    const loop = async () => {
      if (cancelled) return;
      await refresh();
      if (cancelled) return;
      timer.current = setTimeout(() => void loop(), POLL_MS);
    };

    setLoading(true);
    void refresh().finally(() => {
      if (!cancelled) {
        setLoading(false);
        timer.current = setTimeout(() => void loop(), POLL_MS);
      }
    });

    return () => {
      cancelled = true;
      if (timer.current) clearTimeout(timer.current);
    };
  }, [deploymentId, refresh]);

  // Stop polling the moment nothing is moving. A settled deployment cannot
  // change on its own except through an action the user takes here.
  useEffect(() => {
    if (!deployment) return;
    if (!shouldPoll(deployment.status) && timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  }, [deployment]);

  return {
    deployment,
    jobs,
    error,
    loading,
    refresh,
    async apply(fn) {
      setError(null);
      try {
        const result = await fn();
        setDeployment(result.deployment);
        setJobs(result.jobs ?? []);
        // An action that starts work needs the poll running again.
        if (timer.current) clearTimeout(timer.current);
        timer.current = setTimeout(() => void refresh(), POLL_MS);
      } catch (e) {
        setError(userMessage(e));
        await refresh();
      }
    },
  };
}