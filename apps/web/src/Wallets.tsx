import { useQuery } from "@tanstack/react-query";
import { decimalsFor, formatBaseUnits } from "./amount";
import { fetchWallets } from "./api";

export function Wallets() {
  const q = useQuery({ queryKey: ["wallets"], queryFn: fetchWallets, refetchInterval: 30_000 });

  if (q.isPending) return <p data-testid="wallets-loading">Loading wallets…</p>;
  if (q.isError) return <p role="alert" data-testid="wallets-error">Could not load wallets ({q.error.message}).</p>;
  if (q.data.length === 0) return <p data-testid="wallets-empty">No wallets connected yet.</p>;

  return (
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
        {q.data.map((w) => {
          const d = decimalsFor(w.coin);
          return (
            <tr key={w.id} data-testid={`wallet-${w.id}`}>
              <td>{w.label || w.id}</td>
              <td>{w.coin}</td>
              <td>{w.type}</td>
              <td className="num" data-testid="balance">{formatBaseUnits(w.balance, d)}</td>
              <td className="num">{formatBaseUnits(w.spendableBalance, d)}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
