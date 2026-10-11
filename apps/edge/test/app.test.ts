import { afterEach, describe, expect, it, vi } from "vitest";
import { createApp } from "../src/index";
import type { Env } from "../src/env";

const localEnv: Env = {
  ENV: "local",
  CORE_URL: "http://core.test",
  SQS_QUEUE_URL: "https://sqs.ap-northeast-1.amazonaws.com/123456789012/vaultops-dev-webhook-events",
  AWS_REGION: "ap-northeast-1",
  ALLOWED_ORIGINS: "http://localhost:5173",
};

afterEach(() => vi.unstubAllGlobals());

describe("edge", () => {
  it("serves its own health check", async () => {
    const res = await createApp().request("/healthz", {}, localEnv);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ status: "ok", service: "edge", env: "local" });
    expect(res.headers.get("x-content-type-options")).toBe("nosniff");
  });

  it("proxies wallets to core and strips browser credentials", async () => {
    const fetchMock = vi.fn(async (req: Request) => {
      expect(req.url).toBe("http://core.test/v1/wallets");
      expect(req.headers.get("cookie")).toBeNull();
      expect(req.headers.get("authorization")).toBeNull();
      return new Response(JSON.stringify({ wallets: [] }), {
        headers: { "content-type": "application/json", "x-internal": "secret" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const res = await createApp().request(
      "/api/v1/wallets",
      { headers: { cookie: "sb=1", authorization: "Bearer x" } },
      localEnv,
    );
    expect(res.status).toBe(200);
    expect(res.headers.get("x-internal")).toBeNull();
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("signs core requests with SigV4 outside local", async () => {
    // M1 fails closed before auth exists; exercise the signer directly.
    const { callCore } = await import("../src/core");
    const fetchMock = vi.fn(async (req: Request) => {
      expect(req.headers.get("authorization")).toMatch(/^AWS4-HMAC-SHA256 Credential=AKIATEST\/\d{8}\/ap-northeast-1\/lambda\/aws4_request/);
      return new Response("{}");
    });
    vi.stubGlobal("fetch", fetchMock);
    await callCore(
      { ...localEnv, ENV: "dev", AWS_ACCESS_KEY_ID: "AKIATEST", AWS_SECRET_ACCESS_KEY: "secret" },
      "/healthz",
      { method: "GET" },
    );
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("fails closed in deployed environments until auth exists", async () => {
    const res = await createApp().request("/api/v1/wallets", {}, { ...localEnv, ENV: "prod" });
    expect(res.status).toBe(503);
  });

  it("rejects oversized bodies", async () => {
    const res = await createApp().request(
      "/api/v1/wallets",
      { method: "POST", body: "x".repeat(300 * 1024), headers: { "content-length": String(300 * 1024) } },
      localEnv,
    );
    expect(res.status).toBe(413);
  });
});
