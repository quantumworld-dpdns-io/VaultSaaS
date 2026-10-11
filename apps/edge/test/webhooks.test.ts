import { afterEach, describe, expect, it, vi } from "vitest";
import { createApp } from "../src/index";
import type { Env } from "../src/env";

const env: Env = {
  ENV: "dev",
  CORE_URL: "https://core.test",
  SQS_QUEUE_URL: "https://sqs.ap-northeast-1.amazonaws.com/123456789012/vaultops-dev-webhook-events",
  AWS_REGION: "ap-northeast-1",
  ALLOWED_ORIGINS: "",
  SUPABASE_ISSUER: "",
  SUPABASE_JWKS_URL: "",
  AWS_ACCESS_KEY_ID: "AKIATEST",
  AWS_SECRET_ACCESS_KEY: "secret",
};
const token = "A".repeat(43);
const body = JSON.stringify({ type: "transfer", wallet: "w1", transfer: "t1", coin: "tbtc4" });

afterEach(() => vi.unstubAllGlobals());

describe("webhook ingress", () => {
  it("enqueues the raw body to SQS with SigV4 and returns 202", async () => {
    const fetchMock = vi.fn(async (req: Request) => {
      expect(req.url).toBe("https://sqs.ap-northeast-1.amazonaws.com/");
      expect(req.headers.get("x-amz-target")).toBe("AmazonSQS.SendMessage");
      expect(req.headers.get("authorization")).toMatch(/Credential=AKIATEST\/\d{8}\/ap-northeast-1\/sqs\/aws4_request/);
      const sent = (await req.json()) as { QueueUrl: string; MessageBody: string };
      expect(sent.QueueUrl).toBe(env.SQS_QUEUE_URL);
      const envelope = JSON.parse(sent.MessageBody);
      expect(envelope).toMatchObject({ v: 1, provider: "bitgo", orgToken: token, body });
      return new Response("{}", { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const res = await createApp().request(`/webhooks/bitgo/${token}`, { method: "POST", body }, env);
    expect(res.status).toBe(202);
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("does not call core or the database", async () => {
    const fetchMock = vi.fn(async (req: Request) => {
      expect(req.url).not.toContain("core.test");
      return new Response("{}");
    });
    vi.stubGlobal("fetch", fetchMock);
    await createApp().request(`/webhooks/bitgo/${token}`, { method: "POST", body }, env);
  });

  it("rejects malformed tokens without enqueueing", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    for (const bad of ["short", "A".repeat(44), "A".repeat(42) + "!"]) {
      const res = await createApp().request(`/webhooks/bitgo/${encodeURIComponent(bad)}`, { method: "POST", body }, env);
      expect(res.status).toBe(404);
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects non-JSON and non-object bodies", async () => {
    vi.stubGlobal("fetch", vi.fn());
    for (const b of ["not json", "[1,2]", "42"]) {
      const res = await createApp().request(`/webhooks/bitgo/${token}`, { method: "POST", body: b }, env);
      expect(res.status).toBe(400);
    }
  });

  it("answers 503 when SQS is unavailable so BitGo retries", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("boom", { status: 500 })));
    const res = await createApp().request(`/webhooks/bitgo/${token}`, { method: "POST", body }, env);
    expect(res.status).toBe(503);
  });

  it("rejects oversized deliveries", async () => {
    vi.stubGlobal("fetch", vi.fn());
    const big = JSON.stringify({ pad: "x".repeat(210 * 1024) });
    const res = await createApp().request(`/webhooks/bitgo/${token}`, {
      method: "POST", body: big, headers: { "content-length": String(big.length) },
    }, env);
    expect(res.status).toBe(413);
  });

  it("only accepts POST", async () => {
    const res = await createApp().request(`/webhooks/bitgo/${token}`, { method: "GET" }, env);
    expect(res.status).toBe(404);
  });
});
