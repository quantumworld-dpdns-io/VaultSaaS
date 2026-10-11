import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { decimalsFor, formatBaseUnits } from "./amount";
import { api, fetchAlerts, fetchMembers, fetchTransfers } from "./api";

export function Transfers() {
  const q = useQuery({ queryKey: ["transfers"], queryFn: fetchTransfers, refetchInterval: 15_000 });
  if (q.isPending) return <p>Loading…</p>;
  if (q.isError) return <p role="alert">Could not load transfers.</p>;
  if (q.data.length === 0) return <p className="muted" data-testid="transfers-empty">No transfers yet.</p>;
  return (
    <table data-testid="transfers-table">
      <thead>
        <tr>
          <th>When</th>
          <th>Coin</th>
          <th>Direction</th>
          <th className="num">Amount</th>
          <th>State</th>
        </tr>
      </thead>
      <tbody>
        {q.data.map((t) => (
          <tr key={t.id} data-testid={`transfer-${t.providerTransferId}`}>
            <td>{new Date(t.occurredAt).toLocaleString()}</td>
            <td>{t.coin}</td>
            <td>{t.direction}</td>
            <td className="num">{formatBaseUnits(t.amount, decimalsFor(t.coin))}</td>
            <td data-testid="state">{t.state}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function Alerts() {
  const q = useQuery({ queryKey: ["alerts"], queryFn: fetchAlerts, refetchInterval: 15_000 });
  if (q.isPending) return <p>Loading…</p>;
  if (q.isError) return <p role="alert">Could not load alerts.</p>;
  if (q.data.length === 0) return <p className="muted" data-testid="alerts-empty">No alerts.</p>;
  return (
    <ul className="alerts" data-testid="alerts-list">
      {q.data.map((a) => (
        <li key={a.id} className={`sev-${a.severity}`} data-testid={`alert-${a.kind}`}>
          <strong>{a.severity}</strong> {a.kind} — {a.message}
        </li>
      ))}
    </ul>
  );
}

export function Members({ canAdmin }: { canAdmin: boolean }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["members"], queryFn: fetchMembers });
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("viewer");
  const [link, setLink] = useState<string | null>(null);
  const invite = useMutation({
    mutationFn: () => api<{ token: string }>("POST", "/api/v1/org/invitations", { email, role }),
    onSuccess: (r) => {
      setLink(`${window.location.origin}/?invite=${encodeURIComponent(r.token)}`);
      setEmail("");
      void qc.invalidateQueries({ queryKey: ["members"] });
    },
  });
  return (
    <>
      {q.data && (
        <ul data-testid="members-list">
          {q.data.map((m) => (
            <li key={m.userId}>
              {m.email} <span className="muted">({m.role})</span>
            </li>
          ))}
        </ul>
      )}
      {canAdmin && (
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault();
            invite.mutate();
          }}
        >
          <input type="email" placeholder="colleague@company.com" value={email} onChange={(e) => setEmail(e.target.value)} data-testid="invite-email" required />
          <select value={role} onChange={(e) => setRole(e.target.value)} data-testid="invite-role">
            <option value="viewer">viewer</option>
            <option value="approver">approver</option>
            <option value="admin">admin</option>
          </select>
          <button type="submit" data-testid="invite-submit">Invite</button>
        </form>
      )}
      {link && (
        <p data-testid="invite-link">
          Share this one-time link: <code>{link}</code>
        </p>
      )}
      {invite.isError && <p role="alert">Could not create the invitation.</p>}
    </>
  );
}
