/**
 * Types for the AI AWS Architect API.
 *
 * Hand-maintained from the API reference — there is no OpenAPI spec to generate
 * from. Section markers (§) point at the doc so a field and its rules stay findable.
 */

/* ────────────────────────────── Errors (§1.2, §1.3) ───────────────────────── */

export type ErrorCode =
  | 'unauthorized'
  | 'invalid_credentials'
  | 'invalid_token'
  | 'validation_failed'
  | 'not_found'
  | 'conflict'
  | 'stale_config'
  | 'email_taken'
  | 'forbidden'
  | 'aws_not_connected'
  | 'upstream_failed'
  | 'internal_error';

export interface ApiErrorBody {
  error: {
    code: ErrorCode | (string & {});
    message: string;
    /** Only present on `validation_failed`. A `body` key means the whole request
     *  failed to bind and there is no single input to attach it to (§1.2). */
    fields?: Record<string, string>;
  };
}

/* ─────────────────────── Failure codes — separate vocabulary (§1.7) ────────── */

export type AwsFailureCode =
  | 'access_denied'
  | 'account_problem'
  | 'role_missing'
  | 'quota_exceeded'
  | 'region_unavailable'
  | 'invalid_request'
  | 'our_credentials'
  | 'throttled'
  | 'service_unavailable'
  | 'timeout'
  | 'unknown';

export type SystemFailureCode =
  | 'worker_lost'
  | 'unrecorded_outcome'
  | 'precondition_failed'
  | 'cancelled'
  | 'manually_resolved'
  | 'unknown_job_type';

export type FailureCode = AwsFailureCode | SystemFailureCode;

export interface FailureClass {
  retryable: boolean;
  awsSide: boolean;
  indeterminate: boolean;
}

/**
 * §1.7's classification table, for rendering failure messaging client-side.
 *
 * `indeterminate` is the one that matters: it means the operation may have
 * succeeded despite the error, so it must never be rendered as "nothing happened".
 *
 * NOTE: `unknown` is `retryable: true` here per §1.7, but §5.6 enumerates the
 * retryable set as throttled/service_unavailable/timeout only. Unresolved —
 * affects whether the UI shows an attempt counter for it.
 */
export const AWS_FAILURE_CLASS: Record<AwsFailureCode, FailureClass> = {
  access_denied: { retryable: false, awsSide: false, indeterminate: false },
  account_problem: { retryable: false, awsSide: false, indeterminate: false },
  role_missing: { retryable: false, awsSide: false, indeterminate: false },
  quota_exceeded: { retryable: false, awsSide: false, indeterminate: false },
  region_unavailable: { retryable: false, awsSide: false, indeterminate: false },
  invalid_request: { retryable: false, awsSide: false, indeterminate: false },
  our_credentials: { retryable: false, awsSide: false, indeterminate: false },
  throttled: { retryable: true, awsSide: true, indeterminate: false },
  service_unavailable: { retryable: true, awsSide: true, indeterminate: true },
  timeout: { retryable: true, awsSide: true, indeterminate: true },
  unknown: { retryable: true, awsSide: false, indeterminate: true },
};

/* ──────────────────────────────── Auth (§3) ───────────────────────────────── */

export interface User {
  id: string;
  email: string;
  display_name: string | null;
  created_at: string;
}

export interface Session {
  user: User;
  access_token: string;
  token_type: string;
  /** Access token only. The refresh token's 30-day expiry is not returned (§1.1). */
  expires_at: string;
  refresh_token: string;
}

/* ──────────────────────────── AWS connection (§4) ─────────────────────────── */

export type ConnectionStatus = 'not_connected' | 'pending' | 'active' | 'broken';

export interface Failure {
  code: FailureCode;
  message: string;
  retryable: boolean;
  aws_side: boolean;
  indeterminate: boolean;
}

export interface AwsConnection {
  account_id: string;
  region: string;
  role_name: string;
  verified_at: string | null;
  status: ConnectionStatus;
}

export interface ConnectionState {
  status: ConnectionStatus;
  connection: AwsConnection | null;
  failure: Failure | null;
}

export interface ConnectStart {
  connection: AwsConnection;
  /** Contains the external ID — a secret. Open it, never render or log it (§4). */
  launch_url: string;
  expected_role_arn_suffix: string;
}

export interface DisconnectResult {
  stack_console_url: string;
  warnings: string[];
}

/* ──────────────────────────────── Chats (§6) ──────────────────────────────── */

export interface Chat {
  id: string;
  title: string;
  /** What the conversation proposes — not what is deployed (§8.8). */
  current_config_version: number | null;
  message_count: number;
  last_message_at: string | null;
  monthly_budget_usd: number | null;
  /** Set when the chat is archived. */
  archived_at: string | null;
}

export interface ChatList {
  chats: Chat[];
  limit: number;
  offset: number;
  /** No total count is returned; a full page is the only hint more exist (§6). */
}

export type MessageRole = 'user' | 'assistant' | 'system';

export interface Message {
  id: string;
  chat_id: string;
  role: MessageRole;
  content: string;
  seq: number;
  created_at: string;
  /**
   * What produced this message — "stub" or a Bedrock model id. Ground truth per
   * message rather than a global claim, so it stays correct even if the server
   * restarts with a different provider mid-session.
   */
  model?: string | null;
}

export interface MessageList {
  messages: Message[];
  /** 0 when there is no more history. */
  next_before_seq: number;
}

/**
 * The server's reading of a budget instruction in the user's message.
 *
 * Tightening applies immediately; loosening does not. The guarantee is that a
 * ceiling is never raised on the strength of model output alone — confirming is
 * an ordinary authenticated PATCH, which is the human action.
 */
export interface BudgetChange {
  applied: boolean;
  from_usd: number | null;
  to_usd: number | null;
  clear: boolean;
  /** The phrase this was read from. Show it — a number alone gives the user no
   *  way to tell a correct reading from a misheard one. */
  quote: string;
  reason: string;
}

export interface SendMessageResult {
  chat: Chat;
  user_message: Message;
  assistant_message: Message;
  /** null when the turn changed nothing — a clarifying question, or already satisfied. */
  config_version: ConfigVersion | null;
  diff: Diff | null;
  budget_change: BudgetChange | null;
}

/* ────────────────────────── Configuration versions (§7) ───────────────────── */

export type Scope = 'supported' | 'needs_clarification' | 'out_of_scope';
export type VersionSource = 'assistant' | 'revert' | 'manual';

export interface Block {
  id: string;
  template_id: string;
  purpose: string;
  parameters: Record<string, unknown>;
  depends_on: string[];
}

/** A requirement the catalog cannot meet. Distinct from `scope: out_of_scope` (§7). */
export interface OutOfCatalogItem {
  need: string;
  reason: string;
  suggested_manual_step: string;
}

export interface EstimatedCost {
  currency: string;
  monthly_low: number;
  monthly_high: number;
  basis: string;
}

/**
 * Every collection here is always present: empty slices serialise as [] and
 * nullable fields as null. The only endpoint that still omits empty fields is
 * GET /catalog, whose types are marshalled into the model's system prompt —
 * see CatalogTemplate, which stays loosely typed for that reason.
 */
export interface ConfigDocument {
  schema_version: number;
  name: string;
  summary: string;
  /** Stamped by the server from the AWS connection, never from the model (§7). */
  region: string;
  blocks: Block[];
  out_of_catalog: OutOfCatalogItem[];
  open_questions: string[];
  /** Recomputed server-side on every validation (§7). */
  estimated_cost: EstimatedCost | null;
}

export interface ValidationIssue {
  path: string;
  message: string;
}

export interface Validation {
  verdict: 'ok' | 'not_applicable' | 'rejected';
  errors: ValidationIssue[];
  blockers: ValidationIssue[];
}

export interface ConfigVersion {
  version: number;
  source: VersionSource;
  summary: string;
  /** Prose, with the model's decisions folded in as lines. Do not parse (§7). */
  rationale: string;
  document: ConfigDocument;
  changes: DiffChange[];
  validation: Validation;
  /** false → show the proposal but disable Apply (§10.5). */
  applicable: boolean;
  blockers: ValidationIssue[];
  /** A default rather than a judgement when source is 'manual'. */
  scope: Scope;
  /**
   * null when nothing scored it — a manual edit, or a revert to one. Branch on
   * null, never on 0: a real 0 from a model means it had no confidence, which
   * is a different claim and worth showing.
   */
  confidence: number | null;
  parent_version: number | null;
  is_current?: boolean;
  reverted_from_version: number | null;
}

/** Metadata only — no document, so the history list stays cheap (§7). */
export interface VersionListEntry {
  version: number;
  source: VersionSource;
  summary: string;
  is_current: boolean;
  applicable: boolean;
  scope: Scope;
  confidence: number | null;
  parent_version: number | null;
  reverted_from_version: number | null;
}

export interface VersionList {
  versions: VersionListEntry[];
  current_version: number;
}

export interface RevertResult {
  config_version: ConfigVersion;
  message: Message;
}

/**
 * The counterpart to sending a message: same append-only history, same
 * validation, same system message narrating the change — the differences are
 * that no model is involved and that validation errors come back to the caller
 * instead of being discarded.
 */
export interface ManualVersionResult {
  config_version: ConfigVersion;
  message: Message;
  diff: Diff | null;
}

/* ─────────────────────────────── Diffs (§7) ───────────────────────────────── */

export interface DiffChange {
  op: 'add' | 'remove' | 'replace';
  /** RFC 6901 JSON Pointer. */
  path: string;
  from?: unknown;
  to?: unknown;
}

export interface DiffStats {
  added: number;
  removed: number;
  replaced: number;
}

export interface Diff {
  from_version: number;
  to_version: number;
  changes: DiffChange[];
  stats: DiffStats;
  /** Can be "" for very large documents — fall back to `changes` (§10.6). */
  unified: string;
}

/* ───────────────────────────── Deployments (§8) ───────────────────────────── */

export type DeploymentStatus =
  | 'planning'
  | 'awaiting_approval'
  | 'applying'
  | 'applied'
  | 'superseded'
  | 'plan_failed'
  | 'apply_failed'
  | 'destroying'
  | 'destroyed'
  | 'destroy_failed'
  | 'cancelled'
  | 'unknown';

export interface PlanSummary {
  add: number;
  change: number;
  replace: number;
  destroy: number;
  destructive_resources: string[];
}

/** Shape taken from a real response; the doc's field list is incomplete. */
export interface Job {
  id: string;
  type: 'plan' | 'apply' | 'destroy' | (string & {});
  deployment_id: string;
  chat_id: string;
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'unknown' | (string & {});
  attempts: number;
  max_attempts: number;
  run_after?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  /** Present on failures only. */
  last_error?: string | null;
}

export interface Deployment {
  id: string;
  chat_id: string;
  /** Which config version this deployment is for. §10.7 compares it against the
   *  chat's current_config_version to warn about drift. Confirmed present in
   *  real responses even though §8.3 omits it. */
  config_version: number;
  status: DeploymentStatus;
  aws_account_id: string | null;
  region: string | null;
  /** Computed server-side, so a client never has to know the enum to answer
   *  "is this costing money" (§8.1). */
  may_have_live_resources: boolean;
  plan_hash: string | null;
  plan_output: string | null;
  plan_summary: PlanSummary | null;
  /** null when no automatic teardown is scheduled (DEPLOY_TEARDOWN_AFTER=0). */
  teardown_after: string | null;
  failure_code: FailureCode | null;
  failure_message: string | null;
  created_at: string;
  updated_at: string;
}

export interface DeploymentDetail {
  deployment: Deployment;
  /** Newest first. */
  jobs: Job[];
}

export interface ChatDeployments {
  deployments: Deployment[];
  /** What actually exists, as opposed to what the conversation proposes (§8.8). */
  deployed_config_version: number | null;
}

export interface LiveDeployments {
  deployments: Deployment[];
}

/** Status groupings for rendering (§10.7). */
export const IN_PROGRESS_STATUSES: DeploymentStatus[] = ['planning', 'applying', 'destroying'];
export const SETTLED_STATUSES: DeploymentStatus[] = ['applied', 'destroyed', 'cancelled', 'superseded'];
export const NEEDS_ATTENTION_STATUSES: DeploymentStatus[] = ['apply_failed', 'destroy_failed', 'unknown'];

export function isTerminal(status: DeploymentStatus): boolean {
  return status === 'destroyed' || status === 'cancelled';
}

export function shouldPoll(status: DeploymentStatus): boolean {
  return IN_PROGRESS_STATUSES.includes(status);
}

/* ─────────────────────────────── Catalog (§7) ─────────────────────────────── */

/**
 * Loosely typed on purpose: /catalog is the one endpoint that still omits empty
 * fields, because these types are marshalled into the model's system prompt
 * where `"min": null` on every parameter is noise it pays tokens to ignore.
 */
export interface CatalogTemplate {
  id: string;
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  /**
   * Whether the *current runner* can build this — a property of the runner, not
   * of the catalog. Under the stub runner everything reads true, because the
   * stub fabricates a plan from the document and never looks for a module.
   */
  provisionable: boolean;
  unprovisionable_reason: string;
  [key: string]: unknown;
}

export interface Catalog {
  /** "stub" means every `provisionable: true` above is provisional. */
  runner: 'terraform' | 'stub' | (string & {});
  /** "stub" means proposals are canned keyword matches, not model output. */
  reasoning_engine: 'bedrock' | 'stub' | (string & {});
  reasoning_model: string;
  templates: CatalogTemplate[];
}

/* ─────────────────────────────── Health (§2) ──────────────────────────────── */

export interface Readiness {
  status: 'ok' | 'degraded';
  database: 'ok' | 'unreachable';
}