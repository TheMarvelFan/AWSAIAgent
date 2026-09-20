import type { ApiErrorBody, ErrorCode } from './types';

/**
 * Every error from every endpoint has one shape (§1.2). Branch on `code`,
 * never on `message` — messages are written for humans and will change.
 */
export class ApiError extends Error {
  readonly code: ErrorCode | (string & {});
  readonly status: number;
  readonly fields: Record<string, string>;
  /** Echoed from the request. The only way to find the matching server log line. */
  readonly requestId: string | null;

  constructor(opts: {
    code: ApiError['code'];
    message: string;
    status: number;
    fields?: Record<string, string>;
    requestId?: string | null;
  }) {
    super(opts.message);
    this.name = 'ApiError';
    this.code = opts.code;
    this.status = opts.status;
    this.fields = opts.fields ?? {};
    this.requestId = opts.requestId ?? null;
  }

  is(...codes: Array<ErrorCode | (string & {})>): boolean {
    return codes.includes(this.code);
  }

  /**
   * Field errors split into ones that map to a named input and ones that do not.
   *
   * A `body` key means the whole request failed to bind — malformed JSON, or an
   * email the binder rejected — and there is no single input to attach it to
   * (§1.2). Anything else unrecognised goes the same way, so a new server-side
   * key never silently disappears from the UI.
   */
  splitFields(known: readonly string[]): { inline: Record<string, string>; general: string[] } {
    const inline: Record<string, string> = {};
    const general: string[] = [];
    for (const [key, message] of Object.entries(this.fields)) {
      if (known.includes(key)) inline[key] = message;
      else general.push(message);
    }
    return { inline, general };
  }
}

/** The network never reached the server, or CORS rejected it before our code ran. */
export class NetworkError extends Error {
  constructor(message = 'Could not reach the server.') {
    super(message);
    this.name = 'NetworkError';
  }
}

export class TimeoutError extends Error {
  constructor(message = 'The server took too long to respond.') {
    super(message);
    this.name = 'TimeoutError';
  }
}

/**
 * Field messages are fragments meant to follow the field name — "must be at
 * least 10 characters". Render them with the label or the sentence has no
 * subject.
 *
 * This replaces the earlier sanitiser that rewrote raw Go validator text; the
 * server now returns copy written for people, keyed by JSON field name.
 */
export function fieldMessage(label: string, message: string): string {
  const m = message.trim();
  return `${label} ${m.charAt(0).toLowerCase()}${m.slice(1)}`;
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** Parse the standard envelope, tolerating a response that is not one. */
export async function toApiError(response: Response): Promise<ApiError> {
  const requestId = response.headers.get('X-Request-ID');
  let body: Partial<ApiErrorBody> | null = null;
  try {
    body = (await response.json()) as Partial<ApiErrorBody>;
  } catch {
    // Non-JSON body — a proxy error page, or an empty response.
  }

  const err = body?.error;
  return new ApiError({
    code: err?.code ?? 'internal_error',
    message: err?.message ?? `Request failed with status ${response.status}.`,
    status: response.status,
    fields: err?.fields,
    requestId,
  });
}

/**
 * Copy for the failure states a user can actually act on. Errors explain what
 * went wrong and how to fix it; they do not apologise and are not vague.
 */
export function userMessage(e: unknown): string {
  if (e instanceof TimeoutError) return e.message;
  if (e instanceof NetworkError) return e.message;
  if (!isApiError(e)) return 'Something went wrong.';

  switch (e.code) {
    case 'invalid_credentials':
      return 'That email and password do not match an account.';
    case 'email_taken':
      return 'An account already uses that email. Sign in instead.';
    case 'aws_not_connected':
      return 'Connect an AWS account before running a plan.';
    case 'stale_config':
      return 'This changed while you were looking at it. Refresh and try again.';
    case 'conflict':
      return 'That is not possible in the current state. Refresh to see where things stand.';
    case 'not_found':
      return 'That no longer exists.';
    case 'upstream_failed':
      return 'The request reached AWS or the reasoning engine and came back failed. Try again.';
    default:
      return e.message;
  }
}