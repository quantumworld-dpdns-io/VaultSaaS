import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { awsClient } from "./core";
import type { Env } from "./env";

// Per-tenant webhook URL: /webhooks/bitgo/<token>, a 256-bit base64url token.
const TOKEN = /^[A-Za-z0-9_-]{43}$/;
const MAX_BODY = 200 * 1024; // leaves room for the envelope inside SQS limits

export interface QueueEnvelope {
  v: 1;
  provider: "bitgo";
  orgToken: string;
  receivedAt: string;
  body: string; // raw body, byte-for-byte; the core hashes it for dedupe
}

/**
 * Webhook ingress. The edge only validates shape and enqueues; it never
 * interprets the event or touches the database (core principles 1 and 2).
 * The token is resolved to a tenant by the worker, so unknown tokens cost one
 * queue message and are dropped there; the zone rate limit bounds abuse.
 */
export const webhooks = new Hono<{ Bindings: Env }>();

webhooks.post(
  "/bitgo/:token",
  bodyLimit({ maxSize: MAX_BODY, onError: (c) => c.json({ error: "payload_too_large" }, 413) }),
  async (c) => {
    const token = c.req.param("token");
    if (!TOKEN.test(token)) return c.json({ error: "not_found" }, 404);
    const raw = await c.req.text();
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      return c.json({ error: "invalid_json" }, 400);
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return c.json({ error: "invalid_json" }, 400);
    }
    // TODO(Week 0): verify BitGo's webhook signature here if BitGo provides one.
    const envelope: QueueEnvelope = { v: 1, provider: "bitgo", orgToken: token, receivedAt: new Date().toISOString(), body: raw };
    const res = await sendToQueue(c.env, JSON.stringify(envelope));
    if (!res.ok) {
      console.error(JSON.stringify({ msg: "enqueue failed", status: res.status }));
      // 503 makes BitGo retry the delivery later.
      return c.json({ error: "temporarily_unavailable" }, 503);
    }
    return c.json({ status: "queued" }, 202);
  },
);

/** SQS SendMessage over the JSON protocol, SigV4-signed with the edge IAM user. */
export async function sendToQueue(env: Env, messageBody: string): Promise<Response> {
  if (!env.SQS_QUEUE_URL) throw new Error("SQS_QUEUE_URL is not configured");
  const endpoint = new URL(env.SQS_QUEUE_URL).origin + "/";
  const client =
    env.ENV === "local" && !env.AWS_ACCESS_KEY_ID
      ? awsClient({ ...env, AWS_ACCESS_KEY_ID: "local", AWS_SECRET_ACCESS_KEY: "local" }, "sqs")
      : awsClient(env, "sqs");
  return client.fetch(endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-amz-json-1.0", "x-amz-target": "AmazonSQS.SendMessage" },
    body: JSON.stringify({ QueueUrl: env.SQS_QUEUE_URL, MessageBody: messageBody }),
  });
}
