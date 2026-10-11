import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { decimalsFor, formatBaseUnits } from "./amount";
import { api, ApiError, fetchWallets } from "./api";

export function Wallets({ canAdmin }: { canAdmin: boolean }) {
  const q = useQuery({ queryKey: ["wallets"], queryFn: fetchWallets, refetchInterval: 30_000, retry: false });

  if (q.isPending) return <p data-testid="wallets-loading">Loading wallets…</p>;
  if (q.isError) {
    if (q.error instanceof ApiError && q.error.code === "bitgo_not_connected") {
      return canAdmin ? <ConnectBitGo /> : <p data-testid="wallets-not-connected">An admin has not connected BitGo yet.</p>;
    }
    return <p role="alert" data-testid="wallets-error">Could not load wallets ({q.error.message}).</p>;
  }
  const { wallets, stale } = q.data;
  if (wallets.length === 0) return <p data-testid="wallets-empty">No wallets visible to this BitGo token.</p>;

  return (
    <>
      {stale && <p className="muted" data-testid="wallets-stale">BitGo is unreachable; showing the last known balances.</p>}
      <table data-testid="wallets-table">
        <thead>
          <tr>
            <th>Wallet</th>
            <th>Coin</th>
            <th>Type</th>
            <th className="num">Balance</th>
            <th className="num">Spendable</th>
          </tr>
        </thead>
        <tbody>
          {wallets.map((w) => {
            const d = decimalsFor(w.coin);
            return (
              <tr key={w.id} data-testid={`wallet-${w.providerWalletId}`}>
                <td>{w.label || w.providerWalletId}</td>
                <td>{w.coin}</td>
                <td>{w.type}</td>
                <td className="num" data-testid="balance">{w.balance === null ? "—" : formatBaseUnits(w.balance, d)}</td>
                <td className="num">{w.spendableBalance === null ? "—" : formatBaseUnits(w.spendableBalance, d)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}

function ConnectBitGo() {
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const [enterpriseId, setEnterpriseId] = useState("");
  const m = useMutation({
    mutationFn: () => api<{ walletsVisible: number }>("PUT", "/api/v1/org/bitgo-connection", { token, enterpriseId }),
    onSuccess: () => {
      setToken("");
      void qc.invalidateQueries({ queryKey: ["wallets"] });
    },
  });
  return (
    <form
      className="card"
      data-testid="connect-bitgo"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <p>
        Connect a BitGo <strong>read-only</strong> access token for this organization. It is encrypted at rest and never
        shown again. VaultOps cannot send funds.
      </p>
      <label>
        Access token
        <input type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} data-testid="bitgo-token" required />
      </label>
      <label>
        Enterprise ID (optional)
        <input value={enterpriseId} onChange={(e) => setEnterpriseId(e.target.value)} />
      </label>
      <button type="submit" disabled={m.isPending} data-testid="connect-submit">Connect</button>
      {m.isError && <p role="alert">BitGo rejected this token.</p>}
    </form>
  );
}
