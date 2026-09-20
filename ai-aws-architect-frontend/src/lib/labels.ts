/**
 * Turning catalog field names into something a reader recognises.
 *
 * Shared by the config panel and the diff renderer so "memory_mb" reads the same
 * way in both places — a diff that says "Memory" while the panel says
 * "memory_mb" makes the two look like different things.
 */

const PARAM_LABELS: Record<string, string> = {
  memory_mb: 'Memory',
  timeout_seconds: 'Timeout',
  log_retention_days: 'Logs kept',
  function_name: 'Function name',
  budget_name: 'Budget name',
  notify_email: 'Alerts to',
  monthly_limit_usd: 'Monthly limit',
  bucket_name: 'Bucket name',
  table_name: 'Table name',
  versioning: 'Versioning',
  billing_mode: 'Billing mode',
  partition_key: 'Partition key',
  sort_key: 'Sort key',
};

export function paramLabel(key: string): string {
  return PARAM_LABELS[key] ?? key.replace(/[_-]/g, ' ').replace(/^./, (c) => c.toUpperCase());
}

export function paramValue(key: string, value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'boolean') return value ? 'on' : 'off';
  if (key === 'memory_mb') return `${value} MB`;
  if (key === 'timeout_seconds') return `${value}s`;
  if (key === 'log_retention_days') return `${value} day${value === 1 ? '' : 's'}`;
  if (key === 'monthly_limit_usd') return `$${value}`;
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

/** Template ids are already readable enough; just soften the separators. */
export function templateLabel(templateId: string): string {
  return templateId.replace(/[_-]/g, ' ').replace(/^./, (c) => c.toUpperCase());
}