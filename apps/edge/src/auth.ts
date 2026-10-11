import type { MiddlewareHandler } from "hono";
import { createRemoteJWKSet, jwtVerify, type JWTVerifyGetKey } from "jose";
import type { Env } from "./env";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface Identity {
  userId: string;
  email: string;
}

export type AuthVars = { identity: Identity };

// One JWKS cache per isolate; jose refreshes it on key rotation.
const jwksCache = new Map<string, JWTVerifyGetKey>();
function jwks(url: string): JWTVerifyGetKey {
  let k = jwksCache.get(url);
  if (!k) {
    k = createRemoteJWKSet(new URL(url), { cooldownDuration: 30_000 });
    jwksCache.set(url, k);
  }
  return k;
}

/**
 * Verifies the Supabase access token (asymmetric signing keys via JWKS) and
 * exposes the user to downstream handlers. Only email/OAuth users exist in
 * Supabase Auth: Web3 sign-in is disabled (D4), so a wallet can never produce
 * a token that passes here.
 */
export const requireUser: MiddlewareHandler<{ Bindings: Env; Variables: AuthVars }> = async (c, next) => {
  const header = c.req.header("authorization") ?? "";
  const token = header.startsWith("Bearer ") ? header.slice(7) : "";
  if (!token) return c.json({ error: "unauthenticated" }, 401);
  if (!c.env.SUPABASE_JWKS_URL || !c.env.SUPABASE_ISSUER) return c.json({ error: "auth_not_configured" }, 503);
  try {
    const { payload } = await jwtVerify(token, jwks(c.env.SUPABASE_JWKS_URL), {
      issuer: c.env.SUPABASE_ISSUER,
      audience: "authenticated",
      algorithms: ["ES256", "RS256"],
      clockTolerance: 30,
    });
    const sub = typeof payload.sub === "string" ? payload.sub : "";
    if (!UUID.test(sub) || payload.role !== "authenticated") return c.json({ error: "unauthenticated" }, 401);
    c.set("identity", { userId: sub, email: typeof payload.email === "string" ? payload.email : "" });
  } catch {
    return c.json({ error: "unauthenticated" }, 401);
  }
  await next();
};

/** Headers the core trusts (it is reachable only through the edge, via AWS_IAM). */
export function identityHeaders(id: Identity, orgHeader: string | undefined): Record<string, string> | null {
  const h: Record<string, string> = { "x-vaultops-user-id": id.userId, "x-vaultops-user-email": id.email };
  if (orgHeader) {
    if (!UUID.test(orgHeader)) return null;
    h["x-vaultops-org-id"] = orgHeader;
  }
  return h;
}
