import { HEALTH_BASE, MESSAGE_TIMEOUT_MS, http, signOutLocally } from './client';
import { readTokens, saveSession } from './tokens';
import type {
  Catalog,
  Chat,
  ConfigDocument,
  ManualVersionResult,
  ChatDeployments,
  ChatList,
  ConfigVersion,
  ConnectStart,
  ConnectionState,
  DeploymentDetail,
  Diff,
  DisconnectResult,
  LiveDeployments,
  MessageList,
  Readiness,
  RevertResult,
  SendMessageResult,
  Session,
  User,
  VersionList,
} from './types';

/* ──────────────────────────────── Health (§2) ─────────────────────────────── */

export const health = {
  live: () => http.get<{ status: string }>('/healthz', { authenticated: false, base: HEALTH_BASE }),
  ready: () => http.get<Readiness>('/readyz', { authenticated: false, base: HEALTH_BASE }),
};

/* ──────────────────────────────── Auth (§3) ───────────────────────────────── */

export const auth = {
  async signup(email: string, password: string, displayName?: string): Promise<Session> {
    const session = await http.post<Session>('/auth/signup', {
      body: { email, password, display_name: displayName },
      authenticated: false,
    });
    saveSession(session);
    return session;
  },

  async login(email: string, password: string): Promise<Session> {
    const session = await http.post<Session>('/auth/login', {
      body: { email, password },
      authenticated: false,
    });
    saveSession(session);
    return session;
  },

  /**
   * Revokes the refresh token. The access token stays valid until it expires —
   * up to 15 minutes (§5.3) — so clearing local storage is the part that matters
   * to this client. Idempotent server-side, and we sign out locally regardless.
   */
  async logout(): Promise<void> {
    const tokens = readTokens();
    try {
      if (tokens) {
        await http.post<void>('/auth/logout', {
          body: { refresh_token: tokens.refreshToken },
          authenticated: false,
        });
      }
    } finally {
      signOutLocally('logout');
    }
  },

  me: () => http.get<User>('/auth/me'),
};

/* ──────────────────────────── AWS connection (§4) ─────────────────────────── */

export const aws = {
  /**
   * Call on page load. A stored "verified" flag proves nothing — the customer can
   * delete the role at any time. Never throws for "not connected"; that is a
   * state, not a failure (§4).
   */
  connection: (forceRefresh = false) =>
    http.get<ConnectionState>('/aws/connection', {
      query: forceRefresh ? { refresh: true } : undefined,
    }),

  /** Idempotent — reuses the same external ID and role name. */
  startConnect: () => http.post<ConnectStart>('/aws/connection'),

  /**
   * The ARN is a claim; the server assumes the role to prove it.
   *
   * A failure returns 502 with no detail, and the 502 alone cannot tell IAM
   * propagation apart from a permanently wrong trust policy. Follow a failure
   * with `connection()` and read `failure.code` (§4).
   */
  verify: (roleArn: string) =>
    http.post<ConnectionState>('/aws/connection/verify', { body: { role_arn: roleArn } }),

  /**
   * Removes our pointer. Does not revoke anything — the IAM role survives until
   * the customer deletes the CloudFormation stack. Show both warnings (§4).
   */
  disconnect: () => http.del<DisconnectResult>('/aws/connection'),
};

/* ──────────────────────────────── Chats (§6) ──────────────────────────────── */

export const chats = {
  list: (opts: { limit?: number; offset?: number; includeArchived?: boolean } = {}) =>
    http.get<ChatList>('/chats', {
      query: {
        limit: opts.limit,
        offset: opts.offset,
        include_archived: opts.includeArchived,
      },
    }),

  create: (opts: { title?: string; monthlyBudgetUsd?: number } = {}) =>
    http.post<Chat>('/chats', {
      body: { title: opts.title, monthly_budget_usd: opts.monthlyBudgetUsd },
    }),

  get: (chatId: string) => http.get<Chat>(`/chats/${chatId}`),

  /**
   * Clearing the budget needs `clearBudget`, so a field the user did not touch
   * can never silently drop a cost ceiling (§6).
   *
   * Changing the budget does not re-evaluate existing versions — they are
   * immutable. A version blocked as over-budget keeps its original blocker. Tell
   * the user to send a message to get a fresh proposal checked against it.
   */
  update: (
    chatId: string,
    patch: { title?: string; monthlyBudgetUsd?: number; clearBudget?: boolean },
  ) =>
    http.patch<Chat>(`/chats/${chatId}`, {
      body: {
        title: patch.title,
        monthly_budget_usd: patch.monthlyBudgetUsd,
        clear_budget: patch.clearBudget,
      },
    }),

  /**
   * Soft delete. Does not tear down deployments — check `deployments.live()`
   * first and warn. There is no un-archive endpoint (§6).
   */
  archive: (chatId: string) => http.del<void>(`/chats/${chatId}`),

  /**
   * §9 records archiving as one-way through the API, so this will 404 until the
   * endpoint exists. The shape mirrors clear_budget: an explicit flag, so a
   * field the user did not touch cannot silently restore a chat.
   */
  unarchive: (chatId: string) =>
    http.patch<Chat>(`/chats/${chatId}`, { body: { archived: false } }),

  messages: (chatId: string, opts: { limit?: number; beforeSeq?: number } = {}) =>
    http.get<MessageList>(`/chats/${chatId}/messages`, {
      query: { limit: opts.limit, before_seq: opts.beforeSeq },
    }),

  /**
   * Synchronous and slow — it blocks for the model call. No streaming and no way
   * to cancel a turn in flight, so show progress rather than a spinner (§6).
   */
  sendMessage: (chatId: string, content: string, signal?: AbortSignal) =>
    http.post<SendMessageResult>(`/chats/${chatId}/messages`, {
      body: { content },
      timeoutMs: MESSAGE_TIMEOUT_MS,
      signal,
    }),
};

/* ──────────────────────── Configuration versions (§7) ─────────────────────── */

export const config = {
  /** Returns null on 204 — no proposal yet. Render an empty panel, not an error. */
  current: (chatId: string) => http.get<ConfigVersion | null>(`/chats/${chatId}/config`),

  versions: (chatId: string) => http.get<VersionList>(`/chats/${chatId}/config/versions`),

  version: (chatId: string, version: number) =>
    http.get<ConfigVersion>(`/chats/${chatId}/config/versions/${version}`),

  /** `from: 0` means compare against nothing — renders v1 as all additions (§7). */
  diff: (
    chatId: string,
    opts: { from?: number; to?: number; format?: 'json' | 'text' | 'both'; context?: number } = {},
  ) =>
    http.get<Diff>(`/chats/${chatId}/config/diff`, {
      query: { from: opts.from, to: opts.to, format: opts.format, context: opts.context },
    }),

  /**
   * Appends a new version carrying a copy of the target document — nothing is
   * deleted, so a revert is itself revertible. Also writes a `system` message
   * into the transcript. Does not touch deployed infrastructure (§7).
   */
  revert: (chatId: string, version: number) =>
    http.post<RevertResult>(`/chats/${chatId}/config/versions/${version}/revert`),

  /**
   * Edit the running config directly, with no model involved.
   *
   * `document` is a complete configuration, not a patch. The server
   * re-validates and normalises it — region is stamped from the AWS
   * connection, defaults are filled in, cost is recomputed — so what gets
   * stored is never the raw submission. Read the returned version back rather
   * than assuming the document was kept verbatim.
   */
  createVersion: (
    chatId: string,
    body: { document: ConfigDocument; basedOnVersion: number; note?: string },
  ) =>
    http.post<ManualVersionResult>(`/chats/${chatId}/config/versions`, {
      body: {
        document: body.document,
        based_on_version: body.basedOnVersion,
        note: body.note,
      },
    }),
};

export const catalog = {
  get: () => http.get<Catalog>('/catalog'),
};

/* ───────────────────────────── Deployments (§8) ───────────────────────────── */

export const deployments = {
  /**
   * 202 — poll `get()`. A previous deployment in `awaiting_approval` is cancelled
   * automatically, so expect the plan on screen to become stale (§5.7).
   */
  plan: (chatId: string, version: number) =>
    http.post<DeploymentDetail>(`/chats/${chatId}/config/versions/${version}/plan`),

  get: (deploymentId: string) => http.get<DeploymentDetail>(`/deployments/${deploymentId}`),

  /** The hash must be the one the client was shown. It binds the approval to
   *  one specific plan (§8.4). */
  approve: (deploymentId: string, planHash: string) =>
    http.post<DeploymentDetail>(`/deployments/${deploymentId}/approve`, {
      body: { plan_hash: planHash },
    }),

  destroy: (deploymentId: string) =>
    http.post<DeploymentDetail>(`/deployments/${deploymentId}/destroy`),

  /**
   * Only for deployments that never created anything. Also the escape hatch for a
   * hung plan — it releases the one-in-flight-per-chat lock immediately rather
   * than waiting out JOB_STALE_AFTER (§8.6).
   */
  cancel: (deploymentId: string) =>
    http.post<DeploymentDetail>(`/deployments/${deploymentId}/cancel`),

  /**
   * The exit from `unknown`. This is the user asserting they checked AWS and
   * found nothing — a wrong assertion leaves real resources billing, so the UI
   * must make it feel like an assertion, not a dismissal (§8.7).
   */
  resolve: (deploymentId: string, note?: string) =>
    http.post<DeploymentDetail>(`/deployments/${deploymentId}/resolve`, {
      body: { acknowledged: true, note },
    }),

  forChat: (chatId: string, limit?: number) =>
    http.get<ChatDeployments>(`/chats/${chatId}/deployments`, { query: { limit } }),

  /** Everything that may still be billing, across all the user's chats (§8.9). */
  live: () => http.get<LiveDeployments>('/deployments/live'),
};