import { useState } from 'react';
import { useAuth } from '../lib/auth';
import { fieldMessage, isApiError, userMessage } from '../lib/errors';
import { ThemeToggle } from './ThemeToggle';

const KNOWN_FIELDS = ['email', 'password', 'display_name'];

export function AuthScreen() {
  const { login, signup } = useAuth();
  const [mode, setMode] = useState<'login' | 'signup'>('login');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [inline, setInline] = useState<Record<string, string>>({});
  const [general, setGeneral] = useState<string[]>([]);
  const [requestId, setRequestId] = useState<string | null>(null);

  async function submit() {
    if (busy || !email || !password) return;
    setBusy(true);
    setInline({});
    setGeneral([]);
    setRequestId(null);
    try {
      if (mode === 'login') await login(email, password);
      else await signup(email, password);
    } catch (e) {
      if (isApiError(e)) {
        // `body` and anything unrecognised land in general — a key we do not
        // render inline must never silently vanish (§1.2).
        const { inline: fields, general: rest } = e.splitFields(KNOWN_FIELDS);

        // Nothing landed anywhere — invalid_credentials and most non-422 codes
        // carry no fields at all. Never let a failed request render as silence.
        const leftover =
          Object.keys(fields).length === 0 && rest.length === 0 ? [userMessage(e)] : rest;

        // A wrong password is not a system fault. The request ID is for
        // failures the user cannot resolve alone.
        const worthReporting = e.status >= 500 || e.is('upstream_failed', 'internal_error');

        setInline(fields);
        setGeneral(leftover);
        setRequestId(worthReporting ? e.requestId : null);
      } else {
        setGeneral([userMessage(e)]);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center bg-surface-0 p-6">
      <div className="absolute right-4 top-4">
        <ThemeToggle />
      </div>

      <div className="w-full max-w-sm">
        <h1 className="text-lg font-medium tracking-tight text-ink">AI AWS Architect</h1>
        <p className="mt-1 text-sm text-ink-muted">
          Describe infrastructure in plain language. Nothing is created without your approval.
        </p>

        {/* A real form, so Enter submits and password managers recognise the
            fields. The mode switch below is type="button" so it cannot submit. */}
        <form
          className="mt-8 space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <Field
            label="Email"
            name="email"
            type="email"
            value={email}
            onChange={setEmail}
            error={inline.email && fieldMessage('Email', inline.email)}
            autoComplete="email"
          />
          <Field
            label="Password"
            name="password"
            type="password"
            value={password}
            onChange={setPassword}
            error={inline.password && fieldMessage('Password', inline.password)}
            hint={mode === 'signup' ? 'At least 10 characters.' : undefined}
            autoComplete={mode === 'signup' ? 'new-password' : 'current-password'}
          />

          {general.length > 0 && (
            <div className="rounded-md border border-edge bg-surface-2 p-3" role="alert">
              {general.map((m, i) => (
                <p key={i} className="text-sm text-ink">
                  {m}
                </p>
              ))}
              {requestId && (
                <p className="mt-2 font-mono text-[11px] text-ink-faint">{requestId}</p>
              )}
            </div>
          )}

          <button
            type="submit"
            disabled={busy || !email || !password}
            className="w-full rounded-md bg-ink px-4 py-2.5 text-sm font-medium text-surface-0 disabled:opacity-40"
          >
            {busy ? 'Working…' : mode === 'login' ? 'Sign in' : 'Create account'}
          </button>

          <button
            type="button"
            onClick={() => {
              setMode((m) => (m === 'login' ? 'signup' : 'login'));
              setInline({});
              setGeneral([]);
              setRequestId(null);
            }}
            className="w-full text-sm text-ink-muted hover:text-ink"
          >
            {mode === 'login' ? 'Create an account' : 'I already have an account'}
          </button>
        </form>
      </div>
    </div>
  );
}

function Field({
  label,
  name,
  type,
  value,
  onChange,
  error,
  hint,
  autoComplete,
}: {
  label: string;
  name: string;
  type: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
  hint?: string;
  autoComplete?: string;
}) {
  const [revealed, setRevealed] = useState(false);
  const isPassword = type === 'password';

  return (
    <div>
      <label htmlFor={name} className="mb-1.5 block text-xs text-ink-muted">
        {label}
      </label>
      <div className="relative">
        <input
          id={name}
          name={name}
          type={isPassword && revealed ? 'text' : type}
          value={value}
          autoComplete={autoComplete}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange(e.target.value)}
          className={`w-full rounded-md border border-edge bg-surface-2 px-3 py-2 text-sm text-ink outline-none focus:border-edge-strong ${isPassword ? 'pr-10' : ''
            }`}
        />
        {isPassword && (
          <button
            type="button"
            tabIndex={-1}
            onClick={() => setRevealed((r) => !r)}
            aria-label={revealed ? 'Hide password' : 'Show password'}
            className="absolute inset-y-0 right-0 flex items-center px-3 text-ink-faint hover:text-ink"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden>
              <path
                d="M1 8s2.5-4.5 7-4.5S15 8 15 8s-2.5 4.5-7 4.5S1 8 1 8Z"
                stroke="currentColor"
                strokeWidth="1.3"
                strokeLinejoin="round"
              />
              <circle cx="8" cy="8" r="1.9" stroke="currentColor" strokeWidth="1.3" />
              {!revealed && (
                <path
                  d="M2.5 13.5 13.5 2.5"
                  stroke="currentColor"
                  strokeWidth="1.3"
                  strokeLinecap="round"
                />
              )}
            </svg>
          </button>
        )}
      </div>
      {error ? (
        <p className="mt-1.5 text-xs text-destroy-ink">{error}</p>
      ) : hint ? (
        <p className="mt-1.5 text-xs text-ink-faint">{hint}</p>
      ) : null}
    </div>
  );
}