/**
 * Turns Terraform's plan text into something a non-expert can read.
 *
 * §10.6 makes the same point about diffs: RFC 6901 pointers and a unified JSON
 * diff are good engineering and illegible to the audience the spec describes.
 * The raw plan has the same problem — it is the authoritative artifact and it
 * is not a summary. Both views stay available; this is the default.
 *
 * Parsing output meant for humans is inherently approximate. When a line does
 * not match, it is dropped rather than guessed at, and the raw view is always
 * one click away.
 */

export type PlanAction = 'create' | 'update' | 'replace' | 'destroy' | 'read';

export interface PlanResource {
  /** Full Terraform address, e.g. module.api.aws_lambda_function.this */
  address: string;
  /** The module segment, which maps to a config block. */
  module: string | null;
  type: string;
  /** Friendly type, e.g. "Lambda function". */
  kind: string;
  /** The resource's real name where the plan reveals one. */
  name: string | null;
  action: PlanAction;
}

const KINDS: Record<string, string> = {
  aws_apigatewayv2_api: 'API Gateway HTTP API',
  aws_apigatewayv2_integration: 'API Gateway integration',
  aws_apigatewayv2_route: 'API Gateway route',
  aws_apigatewayv2_stage: 'API Gateway stage',
  aws_budgets_budget: 'Spending budget',
  aws_cloudfront_distribution: 'CloudFront distribution',
  aws_cloudwatch_log_group: 'CloudWatch log group',
  aws_dynamodb_table: 'DynamoDB table',
  aws_iam_role: 'IAM role',
  aws_iam_role_policy: 'IAM role policy',
  aws_iam_policy: 'IAM policy',
  aws_lambda_function: 'Lambda function',
  aws_lambda_permission: 'Lambda permission',
  aws_s3_bucket: 'S3 bucket',
  aws_s3_bucket_lifecycle_configuration: 'S3 lifecycle rules',
  aws_s3_bucket_policy: 'S3 bucket policy',
  aws_s3_bucket_public_access_block: 'S3 public access block',
  aws_s3_bucket_server_side_encryption_configuration: 'S3 encryption',
  aws_s3_bucket_versioning: 'S3 versioning',
};

/** Fall back to de-prefixing rather than showing nothing useful. */
function friendlyKind(type: string): string {
  if (KINDS[type]) return KINDS[type];
  const words = type.replace(/^aws_/, '').split('_');
  return words.join(' ').replace(/^./, (c) => c.toUpperCase());
}

const HEADER =
  /^\s*#\s+(\S+)\s+(?:will be (created|destroyed|read during apply)|must be replaced|will be updated in-place|will be replaced)/;

const NAME_ATTR = /^\s*[+~-]?\s*(?:function_name|bucket|table_name|name)\s+=\s+"([^"]+)"/;

export function parsePlan(output: string): PlanResource[] {
  const lines = output.split('\n');
  const found: PlanResource[] = [];

  for (let i = 0; i < lines.length; i++) {
    const m = HEADER.exec(lines[i]);
    if (!m) continue;

    const address = m[1];
    const raw = lines[i];
    const action: PlanAction = raw.includes('must be replaced') || raw.includes('will be replaced')
      ? 'replace'
      : raw.includes('updated in-place')
        ? 'update'
        : m[2] === 'destroyed'
          ? 'destroy'
          : m[2] === 'read during apply'
            ? 'read'
            : 'create';

    const parts = address.split('.');
    const moduleName = parts[0] === 'module' ? parts[1] : null;
    const type = parts[0] === 'module' ? parts[2] : parts[0];

    // Scan the resource body for something worth calling it by.
    let name: string | null = null;
    for (let j = i + 1; j < lines.length && !HEADER.test(lines[j]); j++) {
      const n = NAME_ATTR.exec(lines[j]);
      if (n) {
        name = n[1];
        break;
      }
    }

    found.push({ address, module: moduleName, type, kind: friendlyKind(type), name, action });
  }

  return found;
}

export function groupByModule(resources: PlanResource[]): Array<{
  module: string;
  resources: PlanResource[];
}> {
  const groups = new Map<string, PlanResource[]>();
  for (const r of resources) {
    const key = r.module ?? 'Other resources';
    const list = groups.get(key) ?? [];
    list.push(r);
    groups.set(key, list);
  }
  return [...groups.entries()].map(([module, resources]) => ({ module, resources }));
}

/** module.serverless_api_lambda → "Serverless api lambda" */
export function humanizeModule(name: string): string {
  if (name === 'Other resources') return name;
  return name.replace(/[_-]/g, ' ').replace(/^./, (c) => c.toUpperCase());
}