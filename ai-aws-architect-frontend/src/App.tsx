import { useEffect, useState } from 'react';
import { AuthProvider, RequireAuth } from './lib/auth';
import { AppFrame } from './components/AppFrame';
import { AuthScreen } from './components/AuthScreen';
import { CatalogDialog } from './components/CatalogDialog';
import { ChatHistory } from './components/ChatHistory';
import { ChatSettings } from './components/ChatSettings';
import { ArchiveDialog, ConversationHeader } from './components/ConversationHeader';
import { Conversation } from './components/Conversation';
import { ConfigPanel } from './components/ConfigPanel';
import { DeploymentPanel } from './components/DeploymentPanel';
import { ErrorBoundary, FirstRun } from './components/Shell';
import { deployments } from './lib/api';
import { userMessage } from './lib/errors';
import { useChatList } from './hooks/useChatList';
import { useChatSession } from './hooks/useChatSession';
import { useDeployment } from './hooks/useDeployment';
import type { Chat } from './lib/types';

export default function App() {
  return (
    <ErrorBoundary>
      <AuthProvider>
        <RequireAuth
          fallback={<AuthScreen />}
          pending={<div className="min-h-screen bg-surface-0" />}
        >
          <Workspace />
        </RequireAuth>
      </AuthProvider>
    </ErrorBoundary>
  );
}

function Workspace() {
  const list = useChatList();
  // The open chat is held here rather than looked up in the list. Toggling
  // "show archived" changes what the list contains, and the chat you are
  // reading should not vanish out from under you because of a filter.
  const [activeChat, setActiveChat] = useState<Chat | null>(null);
  const [deploymentId, setDeploymentId] = useState<string | null>(null);
  const [settingsFor, setSettingsFor] = useState<Chat | null>(null);
  const [archiveFor, setArchiveFor] = useState<Chat | null>(null);
  const [archiveLive, setArchiveLive] = useState(0);
  const [catalogOpen, setCatalogOpen] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const activeId = activeChat?.id ?? null;
  const archived = Boolean(activeChat?.archived_at);

  const session = useChatSession(activeId, (chat) => {
    list.patch(chat);
    setActiveChat((prev) => (prev?.id === chat.id ? chat : prev));
  });
  const deployment = useDeployment(deploymentId);

  // Opening a chat should surface whatever deployment it already has, otherwise
  // a plan started in another session is invisible until someone plans again.
  useEffect(() => {
    if (!activeId) {
      setDeploymentId(null);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const { deployments: found } = await deployments.forChat(activeId, 1);
        if (!cancelled) setDeploymentId(found[0]?.id ?? null);
      } catch {
        if (!cancelled) setDeploymentId(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [activeId]);

  async function newChat() {
    setActiveChat(await list.create());
  }

  // The live count has to be in hand before the dialog opens, so the warning is
  // there the first time the user reads it rather than appearing a beat later.
  async function beginArchive(chat: Chat) {
    setArchiveLive(await list.liveCount());
    setArchiveFor(chat);
  }

  async function unarchive(chat: Chat) {
    setNotice(null);
    try {
      setActiveChat(await list.unarchive(chat.id));
    } catch (e) {
      setNotice(userMessage(e));
    }
  }

  const noChats = !list.loading && list.items.length === 0;

  return (
    <>
      <AppFrame
        history={
          <ChatHistory
            list={list}
            activeId={activeId}
            onSelect={setActiveChat}
            onNew={() => void newChat()}
            onSettings={setSettingsFor}
            onArchive={(chat) => void beginArchive(chat)}
            onCatalog={() => setCatalogOpen(true)}
          />
        }
        conversation={
          <div className="flex h-full min-h-0 flex-col">
            {activeChat && (
              <ConversationHeader
                chat={activeChat}
                archived={archived}
                onSettings={() => setSettingsFor(activeChat)}
                onArchive={() => void beginArchive(activeChat)}
                onUnarchive={() => void unarchive(activeChat)}
              />
            )}
            {notice && (
              <p className="shrink-0 border-b border-edge bg-surface-2 px-4 py-2 text-xs text-ink-muted">
                {notice}
              </p>
            )}
            <div className="min-h-0 flex-1">
              {noChats && !activeChat ? (
                <FirstRun onNewChat={() => void newChat()} />
              ) : (
                <Conversation
                  session={session}
                  disabled={!activeId}
                  archived={archived}
                  onConfirmBudget={async (change) => {
                    if (!activeId) return;
                    // Confirming is an ordinary authenticated update — there is
                    // no pending state on the server to resolve.
                    const updated = await list.update(activeId, {
                      monthlyBudgetUsd: change.clear ? undefined : (change.to_usd ?? undefined),
                      clearBudget: change.clear ? true : undefined,
                    });
                    setActiveChat((prev) => (prev?.id === updated.id ? updated : prev));
                  }}
                />
              )}
            </div>
          </div>
        }
        configPanel={
          <ConfigPanel
            chatId={activeId}
            config={session.config}
            frozen={archived}
            onPlanStarted={setDeploymentId}
            onVersionAdded={session.applyVersion}
          />
        }
        deploymentPanel={
          deploymentId ? (
            <DeploymentPanel
              state={deployment}
              currentConfigVersion={session.config?.version ?? null}
              frozen={archived}
              onRePlan={async () => {
                if (!activeId || !session.config) return;
                const { deployment: next } = await deployments.plan(
                  activeId,
                  session.config.version,
                );
                setDeploymentId(next.id);
              }}
            />
          ) : undefined
        }
      />

      {settingsFor && (
        <ChatSettings
          chat={settingsFor}
          onClose={() => setSettingsFor(null)}
          onSave={async (patch) => {
            const updated = await list.update(settingsFor.id, patch);
            setActiveChat((prev) => (prev?.id === updated.id ? updated : prev));
          }}
        />
      )}

      {archiveFor && (
        <ArchiveDialog
          chat={archiveFor}
          liveCount={archiveLive}
          onClose={() => setArchiveFor(null)}
          onConfirm={async () => {
            await list.archive(archiveFor.id);
            setActiveChat((prev) =>
              prev?.id === archiveFor.id
                ? { ...prev, archived_at: new Date().toISOString() }
                : prev,
            );
            setArchiveFor(null);
          }}
        />
      )}

      {catalogOpen && <CatalogDialog onClose={() => setCatalogOpen(false)} />}
    </>
  );
}