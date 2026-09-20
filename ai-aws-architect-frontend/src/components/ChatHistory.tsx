import type { UseChatList } from '../hooks/useChatList';
import type { Chat } from '../lib/types';

export function ChatHistory({
    list,
    activeId,
    onSelect,
    onNew,
    onSettings,
    onArchive,
    onCatalog,
}: {
    list: UseChatList;
    activeId: string | null;
    onSelect: (chat: Chat) => void;
    onNew: () => void;
    onSettings: (chat: Chat) => void;
    onArchive: (chat: Chat) => void;
    onCatalog: () => void;
}) {

    return (
        <div className="flex h-full flex-col">
            <div className="p-3">
                <button
                    onClick={onNew}
                    className="w-full rounded-md bg-surface-2 px-3 py-2 text-sm text-ink ring-1 ring-edge hover:ring-edge-strong"
                >
                    New chat
                </button>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
                {list.loading && <p className="px-2 py-3 text-sm text-ink-faint">Loading…</p>}

                {!list.loading && list.items.length === 0 && (
                    <p className="px-2 py-3 text-sm text-ink-faint">No chats yet.</p>
                )}

                {list.items.map((chat) => (
                    <div
                        key={chat.id}
                        className={`group mb-0.5 flex items-center gap-1 rounded-md px-2 ${chat.id === activeId ? 'bg-surface-2' : 'hover:bg-surface-2/60'
                            }`}
                    >
                        <button
                            onClick={() => onSelect(chat)}
                            className="min-w-0 flex-1 py-2 text-left"
                        >
                            <div className="truncate text-sm text-ink">{chat.title || 'Untitled'}</div>
                            <div className="mt-0.5 flex items-center gap-2 text-[11px] text-ink-faint">
                                <span>
                                    {chat.message_count} message{chat.message_count === 1 ? '' : 's'}
                                </span>
                                {typeof chat.monthly_budget_usd === 'number' && (
                                    <span className="font-mono">${chat.monthly_budget_usd}/mo</span>
                                )}
                            </div>
                        </button>
                        {!chat.archived_at && (
                            <button
                                onClick={() => onSettings(chat)}
                                aria-label="Chat settings"
                                className="shrink-0 p-1 text-ink-faint opacity-0 hover:text-ink group-hover:opacity-100"
                            >
                                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden>
                                    <circle cx="8" cy="8" r="2.2" stroke="currentColor" strokeWidth="1.3" />
                                    <path
                                        d="M8 1.5v1.6M8 12.9v1.6M14.5 8h-1.6M3.1 8H1.5m10.1-4.6-1.1 1.1M5.5 10.5l-1.1 1.1m0-8.2 1.1 1.1m5 5 1.1 1.1"
                                        stroke="currentColor"
                                        strokeWidth="1.3"
                                        strokeLinecap="round"
                                    />
                                </svg>
                            </button>
                        )}
                        {!chat.archived_at && (
                            <button
                                onClick={() => onArchive(chat)}
                                aria-label="Archive chat"
                                className="shrink-0 p-1 text-ink-faint opacity-0 hover:text-ink group-hover:opacity-100"
                            >
                                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden>
                                    <rect x="2" y="3" width="12" height="3" rx="1" stroke="currentColor" strokeWidth="1.3" />
                                    <path d="M3.5 6v6.5a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1V6M6.5 9h3" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
                                </svg>
                            </button>
                        )}
                    </div>
                ))}
            </div>

            <div className="space-y-2 border-t border-edge p-3">
                <button
                    onClick={onCatalog}
                    className="w-full rounded-md px-2 py-1.5 text-left text-xs text-ink-muted hover:bg-surface-2 hover:text-ink"
                >
                    What can be built
                </button>
                <label className="flex cursor-pointer items-center gap-2 px-2 text-xs text-ink-muted">
                    <input
                        type="checkbox"
                        checked={list.includeArchived}
                        onChange={(e) => list.setIncludeArchived(e.target.checked)}
                    />
                    Show archived
                </label>
            </div>

        </div>
    );
}