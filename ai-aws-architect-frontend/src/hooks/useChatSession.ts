import { useCallback, useEffect, useRef, useState } from 'react';
import { chats as chatsApi, config as configApi } from '../lib/api';
import { isApiError, userMessage } from '../lib/errors';
import type { BudgetChange, Chat, ConfigVersion, Diff, Message } from '../lib/types';

export interface SendFailure {
  message: string;
  /** stale_config — the config moved under us and the turn was discarded. */
  stale: boolean;
  retryable: boolean;
  requestId: string | null;
}

/** One open chat: its transcript, its running config, and the send turn. */
export interface UseChatSession {
  messages: Message[];
  config: ConfigVersion | null;
  lastDiff: Diff | null;
  loading: boolean;
  /** Seconds since the turn was sent. The model call is slow and unstreamed. */
  pendingFor: number | null;
  pendingText: string | null;
  failure: SendFailure | null;
  /** The server's reading of a budget instruction, when the turn carried one. */
  budgetChange: BudgetChange | null;
  dismissBudgetChange: () => void;
  hasMore: boolean;
  loadMore: () => Promise<void>;
  send: (content: string) => Promise<boolean>;
  dismissFailure: () => void;
  reloadConfig: () => Promise<void>;
  /** A revert or a manual edit: both return a new version and the system
   *  message that explains it. */
  applyVersion: (result: { config_version: ConfigVersion; message: Message }) => void;
}

export function useChatSession(
  chatId: string | null,
  onChatUpdated: (chat: Chat) => void,
): UseChatSession {
  const [messages, setMessages] = useState<Message[]>([]);
  const [config, setConfig] = useState<ConfigVersion | null>(null);
  const [lastDiff, setLastDiff] = useState<Diff | null>(null);
  const [loading, setLoading] = useState(false);
  const [beforeSeq, setBeforeSeq] = useState(0);
  const [pendingText, setPendingText] = useState<string | null>(null);
  const [pendingFor, setPendingFor] = useState<number | null>(null);
  const [failure, setFailure] = useState<SendFailure | null>(null);
  const [budgetChange, setBudgetChange] = useState<BudgetChange | null>(null);
  const tick = useRef<ReturnType<typeof setInterval> | null>(null);

  const reloadConfig = useCallback(async () => {
    if (!chatId) return;
    try {
      // 204 arrives as null — a new chat renders an empty panel, not an error (§7).
      setConfig(await configApi.current(chatId));
    } catch {
      setConfig(null);
    }
  }, [chatId]);

  useEffect(() => {
    setMessages([]);
    setConfig(null);
    setLastDiff(null);
    setFailure(null);
    setBudgetChange(null);
    setBeforeSeq(0);
    if (!chatId) return;

    let cancelled = false;
    setLoading(true);
    void (async () => {
      try {
        const [list, current] = await Promise.all([
          chatsApi.messages(chatId, { limit: 50 }),
          configApi.current(chatId).catch(() => null),
        ]);
        if (cancelled) return;
        setMessages(list.messages);
        setBeforeSeq(list.next_before_seq);
        setConfig(current);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [chatId]);

  useEffect(() => {
    if (pendingText === null) {
      if (tick.current) clearInterval(tick.current);
      tick.current = null;
      setPendingFor(null);
      return;
    }
    setPendingFor(0);
    tick.current = setInterval(() => setPendingFor((s) => (s ?? 0) + 1), 1000);
    return () => {
      if (tick.current) clearInterval(tick.current);
    };
  }, [pendingText]);

  return {
    messages,
    config,
    lastDiff,
    loading,
    pendingFor,
    pendingText,
    failure,
    budgetChange,
    dismissBudgetChange: () => setBudgetChange(null),
    hasMore: beforeSeq !== 0,
    dismissFailure: () => setFailure(null),
    reloadConfig,

    applyVersion(result) {
      setConfig(result.config_version);
      // The backend narrates the change into the transcript so the chat explains
      // why the panel moved on its own (§7).
      if (result.message) setMessages((prev) => [...prev, result.message]);
    },

    async loadMore() {
      if (!chatId || beforeSeq === 0) return;
      const page = await chatsApi.messages(chatId, { limit: 50, beforeSeq });
      setMessages((prev) => [...page.messages, ...prev]);
      setBeforeSeq(page.next_before_seq);
    },

    /** Resolves true when the turn landed. False leaves the draft in the box. */
    async send(content: string) {
      if (!chatId) return false;
      setFailure(null);
      setBudgetChange(null);
      setPendingText(content);
      try {
        const result = await chatsApi.sendMessage(chatId, content);
        setMessages((prev) => [...prev, result.user_message, result.assistant_message]);
        // Both panels update from this one response rather than a refetch (§10.4).
        // A null config_version means the turn changed nothing — a clarifying
        // question, or a request already satisfied. Keep what is on screen.
        if (result.config_version) setConfig(result.config_version);
        setLastDiff(result.diff);
        // A tightening is already applied and the chat carries the new figure.
        // A loosening needs the user to confirm, so it surfaces in the transcript.
        setBudgetChange(result.budget_change ?? null);
        onChatUpdated(result.chat);
        return true;
      } catch (e) {
        const stale = isApiError(e) && e.is('stale_config');
        if (stale) {
          // Another tab landed a version first and the whole turn was discarded.
          // Refetch so the panel is honest, then let the user resend — never
          // silently retry, because their message was not applied (§5.7).
          await reloadConfig();
        }
        setFailure({
          message: stale
            ? 'The configuration changed in another tab, so this message was not applied. The panel has been refreshed — send it again if it still makes sense.'
            : userMessage(e),
          stale,
          retryable: !stale,
          requestId: isApiError(e) && e.status >= 500 ? e.requestId : null,
        });
        return false;
      } finally {
        setPendingText(null);
      }
    },
  };
}