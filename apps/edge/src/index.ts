import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { cors } from "hono/cors";
import { secureHeaders } from "hono/secure-headers";
import { callCore } from "./core";
import type { Env } from "./env";
import { webhooks } from "./webhooks";

export type AppEnv = { Bindings: Env };

export function createApp() {
  const app = new Hono<AppEnv>();

  app.use("*", secureHeaders());
  app.use("/api/*", bodyLimit({ maxSize: 256 * 1024, onError: (c) => c.json({ error: "payload_too_large" }, 413) }));
  app.use("/api/*", async (c, next) => {
    const origins = c.env.ALLOWED_ORIGINS.split(",").map((o) => o.trim()).filter(Boolean);
    return cors({ origin: origins, allowHeaders: ["Authorization", "Content-Type", "X-Org-Id"], maxAge: 600 })(c, next);
  });

  app.get("/healthz", (c) => c.json({ status: "ok", service: "edge", env: c.env.ENV }));

  app.route("/webhooks", webhooks);

  // Fail closed: until user authentication exists (M3) the API is only
  // reachable in local development.
  app.use("/api/*", async (c, next) => {
    if (c.env.ENV !== "local") return c.json({ error: "auth_not_configured" }, 503);
    await next();
  });

  app.get("/api/healthz", (c) => proxy(c.env, c.req.raw, "/healthz"));
  app.get("/api/v1/wallets", (c) => proxy(c.env, c.req.raw, "/v1/wallets"));

  app.notFound((c) => c.json({ error: "not_found" }, 404));
  app.onError((err, c) => {
    console.error(JSON.stringify({ msg: "edge error", err: String(err) }));
    return c.json({ error: "internal" }, 500);
  });
  return app;
}

async function proxy(env: Env, req: Request, path: string): Promise<Response> {
  const url = new URL(req.url);
  const res = await callCore(env, path + url.search, {
    method: req.method,
    headers: req.headers,
    body: req.method === "GET" ? null : await req.arrayBuffer(),
  });
  // Re-wrap so only safe upstream headers reach the browser.
  return new Response(res.body, {
    status: res.status,
    headers: { "content-type": res.headers.get("content-type") ?? "application/json", "cache-control": "no-store" },
  });
}

export default createApp();
