import { AwsClient } from "aws4fetch";
import type { Env } from "./env";

// Headers the edge forwards to the core service. Everything else (cookies,
// the browser's Authorization header, cf-* headers) is dropped; identity is
// passed explicitly by the auth middleware instead.
const FORWARDED_HEADERS = ["content-type", "accept"];

/**
 * Calls the Go core Lambda through its Function URL. In AWS the URL uses
 * AWS_IAM auth, so every request is SigV4-signed with the edge IAM user, whose
 * only permissions are lambda:InvokeFunctionUrl and sqs:SendMessage (plan §2).
 */
export async function callCore(
  env: Env,
  path: string,
  init: { method: string; headers?: Headers; body?: ArrayBuffer | string | null; extraHeaders?: Record<string, string> },
): Promise<Response> {
  if (!env.CORE_URL) throw new Error("CORE_URL is not configured");
  const url = new URL(path, env.CORE_URL);
  const headers = new Headers();
  for (const h of FORWARDED_HEADERS) {
    const v = init.headers?.get(h);
    if (v) headers.set(h, v);
  }
  for (const [k, v] of Object.entries(init.extraHeaders ?? {})) headers.set(k, v);
  const req = new Request(url.toString(), {
    method: init.method,
    headers,
    body: init.method === "GET" || init.method === "HEAD" ? null : (init.body ?? null),
  });
  if (env.ENV === "local") return fetch(req);
  return awsClient(env, "lambda").fetch(req);
}

export function awsClient(env: Env, service: "lambda" | "sqs"): AwsClient {
  if (!env.AWS_ACCESS_KEY_ID || !env.AWS_SECRET_ACCESS_KEY) {
    throw new Error("edge AWS credentials are not configured");
  }
  return new AwsClient({
    accessKeyId: env.AWS_ACCESS_KEY_ID,
    secretAccessKey: env.AWS_SECRET_ACCESS_KEY,
    region: env.AWS_REGION,
    service,
    // Core calls are not retried (a retried POST could double-apply); SQS
    // sends may be, because the worker dedupes deliveries.
    retries: service === "sqs" ? 2 : 0,
  });
}
