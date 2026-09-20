import { useCallback, useEffect, useState } from 'react';
import { config as configApi } from '../lib/api';
import { userMessage } from '../lib/errors';
import type { ConfigVersion, Diff, RevertResult, VersionListEntry } from '../lib/types';

export interface UseVersions {
  versions: VersionListEntry[];
  currentVersion: number | null;
  loading: boolean;
  error: string | null;
  selected: number | null;
  selectedVersion: ConfigVersion | null;
  diff: Diff | null;
  diffLoading: boolean;
  select: (version: number | null) => void;
  reload: () => Promise<void>;
  revert: (version: number) => Promise<RevertResult>;
}

export function useVersions(chatId: string | null, refreshKey: unknown): UseVersions {
  const [versions, setVersions] = useState<VersionListEntry[]>([]);
  const [currentVersion, setCurrentVersion] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const [selectedVersion, setSelectedVersion] = useState<ConfigVersion | null>(null);
  const [diff, setDiff] = useState<Diff | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);

  const reload = useCallback(async () => {
    if (!chatId) return;
    setLoading(true);
    try {
      const result = await configApi.versions(chatId);
      setVersions(result.versions ?? []);
      setCurrentVersion(result.current_version);
      setError(null);
    } catch (e) {
      setError(userMessage(e));
      setVersions([]);
    } finally {
      setLoading(false);
    }
  }, [chatId]);

  // refreshKey lets the panel reload the list when a new version lands from a
  // message turn, without this hook knowing anything about messages.
  useEffect(() => {
    setSelected(null);
    setSelectedVersion(null);
    setDiff(null);
    void reload();
  }, [reload, refreshKey]);

  // Selecting a version fetches its document and its diff against the one
  // before it, so the list stays cheap and only the opened version costs a call.
  useEffect(() => {
    if (!chatId || selected === null) {
      setSelectedVersion(null);
      setDiff(null);
      return;
    }
    let cancelled = false;
    setDiffLoading(true);
    void (async () => {
      try {
        const [version, d] = await Promise.all([
          configApi.version(chatId, selected),
          // from=0 means "compare against nothing", which is the only sensible
          // base for v1 (§7).
          configApi.diff(chatId, { from: selected === 1 ? 0 : selected - 1, to: selected }),
        ]);
        if (cancelled) return;
        setSelectedVersion(version);
        setDiff(d);
      } catch (e) {
        if (!cancelled) {
          setError(userMessage(e));
          setDiff(null);
        }
      } finally {
        if (!cancelled) setDiffLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [chatId, selected]);

  return {
    versions,
    currentVersion,
    loading,
    error,
    selected,
    selectedVersion,
    diff,
    diffLoading,
    select: setSelected,
    reload,
    async revert(version) {
      if (!chatId) throw new Error('No chat');
      const result = await configApi.revert(chatId, version);
      await reload();
      setSelected(null);
      return result;
    },
  };
}