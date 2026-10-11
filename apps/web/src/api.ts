// All data goes through the edge Worker (core principle 4); the browser never
// talks to Postgres or holds a service key.
import { accessToken } from "./auth";

export const API_BASE: string = import.meta.env.VITE_API_BASE ?? "";

export interface Wallet {
  id: string;
  providerWalletId: string;
  coin: string;
  label: string;
  type: string;
  balance: string | null;
  confirmedBalance: string | null;
  spendableBalance: string | null;
}

export interface Membership {
  id: string;
  name: string;
  role: "owner" | "admin" | "approver" | "viewer";
}

export interface Me {
  userId: string;
  email: string;
  organizations: Membership[];
  linkedWallets: { chain: string; address: string; verifiedAt: string }[];
}

export interface Transfer {
  id: string;
  providerTransferId: string;
  coin: string;
  direction: string;
  amount: string;
  state: string;
  occurredAt: string;
}

export interface Alert {
  id: string;
  kind: string;
  severity: string;
  message: string;
  createdAt: string;
}

export interface Member {
  userId: string;
  email: string;
  role: string;
}

export class ApiError extends Error {
  constructor(public status: number, public code: string) {
    super(`${status} ${code}`);
  }
}

let currentOrg: string | null = null;
export function setCurrentOrg(id: string | null) {
  currentOrg = id;
}

async function authHeaders(): Promise<Record<string, string>> {
  const headers: Record<string, string> = {};
  const token = await accessToken();
  if (token) headers.authorization = `Bearer ${token}`;
  if (currentOrg) headers["x-org-id"] = currentOrg;
  return headers;
}

/** Raw request (CSV upload / download); throws ApiError on non-2xx. */
export async function apiRaw(method: string, path: string, body?: string, contentType?: string): Promise<Response> {
  const headers = await authHeaders();
  if (contentType) headers["content-type"] = contentType;
  const res = await fetch(API_BASE + path, { method, headers, body });
  if (!res.ok) {
    const b = (await res.json().catch(() => ({}))) as { error?: string };
    throw new ApiError(res.status, b.error ?? "request_failed");
  }
  return res;
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { accept: "application/json", ...(await authHeaders()) };
  if (body !== undefined) headers["content-type"] = "application/json";
  const res = await fetch(API_BASE + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!res.ok) {
    const b = (await res.json().catch(() => ({}))) as { error?: string };
    throw new ApiError(res.status, b.error ?? "request_failed");
  }
  return (await res.json()) as T;
}

export const getMe = () => api<Me>("GET", "/api/v1/me");
export const fetchWallets = () => api<{ wallets: Wallet[]; stale: boolean }>("GET", "/api/v1/wallets");
export const fetchTransfers = () => api<{ transfers: Transfer[] }>("GET", "/api/v1/transfers").then((r) => r.transfers);
export const fetchAlerts = () => api<{ alerts: Alert[] }>("GET", "/api/v1/alerts").then((r) => r.alerts);
export const fetchMembers = () => api<{ members: Member[] }>("GET", "/api/v1/org/members").then((r) => r.members);

export interface ExpectedPayment {
  id: string;
  reference: string;
  coin: string;
  address: string;
  amount: string;
  expectedBy: string | null;
  status: string;
}

export interface Run {
  id: string;
  trigger: string;
  status: string;
  startedAt: string;
  stats: Record<string, number>;
}

export interface Finding {
  id: string;
  kind: string;
  providerTransferId: string | null;
  reference: string | null;
  details: Record<string, unknown>;
}

export const fetchExpected = () =>
  api<{ expectedPayments: ExpectedPayment[] }>("GET", "/api/v1/expected-payments").then((r) => r.expectedPayments);
export const fetchRuns = () => api<{ runs: Run[] }>("GET", "/api/v1/reconciliation/runs").then((r) => r.runs);
export const fetchFindings = (run: string) =>
  api<{ items: Finding[] }>("GET", `/api/v1/reconciliation/runs/${run}/items`).then((r) => r.items);
