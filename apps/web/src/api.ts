// All data goes through the edge Worker (core principle 4); the browser never
// talks to Postgres or holds a service key.
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? "";

export interface Wallet {
  id: string;
  coin: string;
  label: string;
  type: string;
  balance: string;
  confirmedBalance: string;
  spendableBalance: string;
}

export class ApiError extends Error {
  constructor(public status: number, public code: string) {
    super(`${status} ${code}`);
  }
}

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path, { headers: { accept: "application/json" } });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new ApiError(res.status, body.error ?? "request_failed");
  }
  return (await res.json()) as T;
}

export const fetchWallets = () => apiGet<{ wallets: Wallet[] }>("/api/v1/wallets").then((r) => r.wallets);
