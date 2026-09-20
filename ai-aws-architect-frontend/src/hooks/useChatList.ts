import { useCallback, useEffect, useState } from 'react';
import { chats as chatsApi, deployments } from '../lib/api';
import { userMessage } from '../lib/errors';
import type { Chat } from '../lib/types';

/** The sidebar list. Paired with useChatSession, which owns one open chat. */
export interface UseChatList {
  items: Chat[];
  loading: boolean;
  error: string | null;
  includeArchived: boolean;
  setIncludeArchived: (v: boolean) => void;
  reload: () => Promise<void>;
  create: (opts?: { title?: string; monthlyBudgetUsd?: number }) => Promise<Chat>;
  /** Returns the number of live deployments so the caller can warn first. */
  liveCount: () => Promise<number>;
  archive: (chatId: string) => Promise<void>;
  unarchive: (chatId: string) => Promise<Chat>;
  update: (
    chatId: string,
    patch: { title?: string; monthlyBudgetUsd?: number; clearBudget?: boolean },
  ) => Promise<Chat>;
  patch: (chat: Chat) => void;
}

export function useChatList(): UseChatList {
  const [items, setItems] = useState<Chat[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [includeArchived, setIncludeArchived] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const { chats } = await chatsApi.list({ includeArchived, limit: 100 });
      setItems(chats);
      setError(null);
    } catch (e) {
      setError(userMessage(e));
    } finally {
      setLoading(false);
    }
  }, [includeArchived]);

  useEffect(() => {
    void reload();
  }, [reload]);

  return {
    items,
    loading,
    error,
    includeArchived,
    setIncludeArchived,
    reload,

    async create(opts) {
      const chat = await chatsApi.create(opts);
      setItems((prev) => [chat, ...prev]);
      return chat;
    },

    async liveCount() {
      try {
        const { deployments: live } = await deployments.live();
        return live.length;
      } catch {
        return 0;
      }
    },

    async archive(chatId) {
      await chatsApi.archive(chatId);
      // Archiving only removes it from the default listing; the chat still works
      // through the API and there is no un-archive (§6).
      if (!includeArchived) setItems((prev) => prev.filter((c) => c.id !== chatId));
      else await reload();
    },

    async unarchive(chatId) {
      const chat = await chatsApi.unarchive(chatId);
      await reload();
      return chat;
    },

    async update(chatId, patch) {
      const chat = await chatsApi.update(chatId, patch);
      setItems((prev) => prev.map((c) => (c.id === chat.id ? chat : c)));
      return chat;
    },

    /** Keep the sidebar in step with a chat the message response just updated. */
    patch(chat) {
      setItems((prev) => prev.map((c) => (c.id === chat.id ? chat : c)));
    },
  };
}