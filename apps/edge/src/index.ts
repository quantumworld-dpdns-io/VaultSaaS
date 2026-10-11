import { Hono, type Context } from "hono";
import { bodyLimit } from "hono/body-limit";
import { cors } from "hono/cors";
import { secureHeaders } from "hono/secure-headers";
import { requireUser, identityHeaders, type AuthVars } from "./auth";
import { callCore } from "./core";
import type { Env } from "./env";
import { webhooks } from "./webhooks";

export type AppEnv = { Bindings: Env; Variables: AuthVars };

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

  app.get("/api/healthz", (c) => proxy(c.env, c.req.raw, "/healthz", {}));

  // Everything else under /api/v1 needs a verified Supabase user.
  app.use("/api/v1/*", requireUser);
  const forward = async (c: Context<AppEnv>): Promise<Response> => {
    const extra = identityHeaders(c.get("identity"), c.req.header("x-org-id"));
    if (!extra) return Response.json({ error: "invalid_org_id" }, { status: 400 });
    const path = c.req.path.slice("/api".length);
    return proxy(c.env, c.req.raw, path, extra);
  };
  app.get("/api/v1/*", forward);
  app.post("/api/v1/*", forward);
  app.put("/api/v1/*", forward);

  app.notFound((c) => c.json({ error: "not_found" }, 404));
  app.onError((err, c) => {
    console.error(JSON.stringify({ msg: "edge error", err: String(err) }));
    return c.json({ error: "internal" }, 500);
  });
  return app;
}

async function proxy(env: Env, req: Request, path: string, extraHeaders: Record<string, string>): Promise<Response> {
  const url = new URL(req.url);
  const res = await callCore(env, path + url.search, {
    method: req.method,
    headers: req.headers,
    body: req.method === "GET" ? null : await req.arrayBuffer(),
    extraHeaders,
  });
  // Re-wrap so only safe upstream headers reach the browser.
  return new Response(res.body, {
    status: res.status,
    headers: { "content-type": res.headers.get("content-type") ?? "application/json", "cache-control": "no-store" },
  });
}

export default createApp();
