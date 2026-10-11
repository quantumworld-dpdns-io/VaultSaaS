import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { SignJWT, exportJWK, generateKeyPair, type CryptoKey } from "jose";
import { createApp } from "../src/index";
import type { Env } from "../src/env";

const ISSUER = "https://proj.supabase.test/auth/v1";
const JWKS_URL = ISSUER + "/.well-known/jwks.json";
const USER = "11111111-2222-4333-8444-555555555555";
const ORG = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";

const env: Env = {
  ENV: "local",
  CORE_URL: "http://core.test",
  SQS_QUEUE_URL: "https://sqs.ap-northeast-1.amazonaws.com/123456789012/vaultops-dev-webhook-events",
  AWS_REGION: "ap-northeast-1",
  ALLOWED_ORIGINS: "http://localhost:5173",
  SUPABASE_ISSUER: ISSUER,
  SUPABASE_JWKS_URL: JWKS_URL,
};

let priv: CryptoKey;
let other: CryptoKey;
let jwks: unknown;

beforeAll(async () => {
  const kp = await generateKeyPair("ES256");
  priv = kp.privateKey;
  other = (await generateKeyPair("ES256")).privateKey;
  jwks = { keys: [{ ...(await exportJWK(kp.publicKey)), kid: "k1", alg: "ES256", use: "sig" }] };
});

afterEach(() => vi.unstubAllGlobals());

function token(claims: Record<string, unknown> = {}, key: CryptoKey = priv, iss = ISSUER) {
  return new SignJWT({ role: "authenticated", email: "alice@example.com", ...claims })
    .setProtectedHeader({ alg: "ES256", kid: "k1" })
    .setSubject((claims.sub as string) ?? USER)
    .setIssuer(iss)
    .setAudience("authenticated")
    .setIssuedAt()
    .setExpirationTime("10m")
    .sign(key);
}

function stubFetch(core: (req: Request) => Response | Promise<Response>) {
  const f = vi.fn(async (input: Request | string | URL, init?: RequestInit) => {
    const req = input instanceof Request ? input : new Request(String(input), init);
    if (req.url === JWKS_URL) return Response.json(jwks);
    return core(req);
  });
  vi.stubGlobal("fetch", f);
  return f;
}

describe("edge", () => {
  it("serves its own health check", async () => {
    const res = await createApp().request("/healthz", {}, env);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ status: "ok", service: "edge", env: "local" });
    expect(res.headers.get("x-content-type-options")).toBe("nosniff");
  });

  it("forwards the verified identity and org, never the browser's credentials", async () => {
    const core = vi.fn((req: Request) => {
      expect(req.url).toBe("http://core.test/v1/wallets");
      expect(req.headers.get("x-vaultops-user-id")).toBe(USER);
      expect(req.headers.get("x-vaultops-user-email")).toBe("alice@example.com");
      expect(req.headers.get("x-vaultops-org-id")).toBe(ORG);
      expect(req.headers.get("authorization")).toBeNull();
      expect(req.headers.get("cookie")).toBeNull();
      return Response.json({ wallets: [] }, { headers: { "x-internal": "secret" } });
    });
    stubFetch(core);
    const res = await createApp().request(
      "/api/v1/wallets",
      { headers: { authorization: `Bearer ${await token()}`, "x-org-id": ORG, cookie: "sb=1" } },
      env,
    );
    expect(res.status).toBe(200);
    expect(res.headers.get("x-internal")).toBeNull();
    expect(core).toHaveBeenCalledOnce();
  });

  it("ignores identity headers supplied by the client", async () => {
    stubFetch((req) => {
      expect(req.headers.get("x-vaultops-user-id")).toBe(USER);
      return Response.json({});
    });
    await createApp().request(
      "/api/v1/me",
      { headers: { authorization: `Bearer ${await token()}`, "x-vaultops-user-id": "99999999-9999-4999-8999-999999999999" } },
      env,
    );
  });

  it("rejects missing, forged, foreign-issuer and expired tokens", async () => {
    const core = vi.fn();
    stubFetch(core);
    const expired = await new SignJWT({ role: "authenticated" })
      .setProtectedHeader({ alg: "ES256", kid: "k1" }).setSubject(USER).setIssuer(ISSUER)
      .setAudience("authenticated").setIssuedAt(0).setExpirationTime(60).sign(priv);
    const bad = [
      undefined,
      "Bearer not-a-jwt",
      `Bearer ${await token({}, other)}`,
      `Bearer ${await token({}, priv, "https://evil.test/auth/v1")}`,
      `Bearer ${await token({ role: "anon" })}`,
      `Bearer ${await token({ sub: "not-a-uuid" })}`,
      `Bearer ${expired}`,
    ];
    for (const auth of bad) {
      const res = await createApp().request("/api/v1/wallets", { headers: auth ? { authorization: auth } : {} }, env);
      expect(res.status).toBe(401);
    }
    expect(core).not.toHaveBeenCalled();
  });

  it("rejects a malformed org id", async () => {
    stubFetch(() => Response.json({}));
    const res = await createApp().request(
      "/api/v1/wallets",
      { headers: { authorization: `Bearer ${await token()}`, "x-org-id": "../../etc" } },
      env,
    );
    expect(res.status).toBe(400);
  });

  it("signs core requests with SigV4 outside local", async () => {
    const { callCore } = await import("../src/core");
    const f = vi.fn(async (req: Request) => {
      expect(req.headers.get("authorization")).toMatch(/^AWS4-HMAC-SHA256 Credential=AKIATEST\/\d{8}\/ap-northeast-1\/lambda\/aws4_request/);
      return new Response("{}");
    });
    vi.stubGlobal("fetch", f);
    await callCore({ ...env, ENV: "dev", AWS_ACCESS_KEY_ID: "AKIATEST", AWS_SECRET_ACCESS_KEY: "secret" }, "/healthz", { method: "GET" });
    expect(f).toHaveBeenCalledOnce();
  });

  it("rejects oversized API bodies", async () => {
    const res = await createApp().request(
      "/api/v1/orgs",
      { method: "POST", body: "x".repeat(300 * 1024), headers: { "content-length": String(300 * 1024) } },
      env,
    );
    expect(res.status).toBe(413);
  });
});
