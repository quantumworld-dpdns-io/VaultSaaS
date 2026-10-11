import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { decimalsFor, formatBaseUnits } from "./amount";
import { api, apiRaw, ApiError, fetchExpected, fetchFindings, fetchRuns, type Run } from "./api";

const KIND_LABEL: Record<string, string> = {
  matched: "Matched",
  amount_mismatch: "Amount mismatch",
  missing_bitgo: "Overdue (not received)",
  missing_internal: "Unexpected inbound",
  status_mismatch: "Status corrected",
  missed_webhook: "Missed webhook (backfilled)",
};

export function Reconciliation({ canAdmin }: { canAdmin: boolean }) {
  const qc = useQueryClient();
  const runs = useQuery({ queryKey: ["runs"], queryFn: fetchRuns });
  const expected = useQuery({ queryKey: ["expected"], queryFn: fetchExpected });
  const [selected, setSelected] = useState<string | null>(null);
  const runId = selected ?? runs.data?.[0]?.id ?? null;

  const runNow = useMutation({
    mutationFn: () => api<{ runId: string }>("POST", "/api/v1/reconciliation/runs"),
    onSettled: (r) => {
      if (r) setSelected(r.runId);
      void qc.invalidateQueries({ queryKey: ["runs"] });
      void qc.invalidateQueries({ queryKey: ["expected"] });
      void qc.invalidateQueries({ queryKey: ["transfers"] });
      void qc.invalidateQueries({ queryKey: ["alerts"] });
    },
  });

  return (
    <>
      {canAdmin && (
        <div className="row">
          <ImportCsv />
          <button onClick={() => runNow.mutate()} disabled={runNow.isPending} data-testid="run-recon">
            {runNow.isPending ? "Reconciling…" : "Run reconciliation now"}
          </button>
          {runNow.isError && <span role="alert">Reconciliation failed; see alerts.</span>}
        </div>
      )}
      <ReportDownload />

      <h3>Runs</h3>
      {runs.data && runs.data.length > 0 ? (
        <select value={runId ?? ""} onChange={(e) => setSelected(e.target.value)} data-testid="run-select">
          {runs.data.map((r) => (
            <option key={r.id} value={r.id}>
              {new Date(r.startedAt).toLocaleString()} · {r.trigger} · {r.status} · {summary(r)}
            </option>
          ))}
        </select>
      ) : (
        <p className="muted" data-testid="runs-empty">No reconciliation runs yet.</p>
      )}
      {runId && <Findings run={runId} />}

      <h3>Expected payments</h3>
      {expected.data && expected.data.length > 0 ? (
        <table data-testid="expected-table">
          <thead>
            <tr>
              <th>Reference</th>
              <th>Coin</th>
              <th>Address</th>
              <th className="num">Amount</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {expected.data.map((p) => (
              <tr key={p.id} data-testid={`expected-${p.reference}`}>
                <td>{p.reference}</td>
                <td>{p.coin}</td>
                <td><code>{p.address}</code></td>
                <td className="num">{formatBaseUnits(p.amount, decimalsFor(p.coin))}</td>
                <td data-testid="status">{p.status}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="muted">No expected payments imported.</p>
      )}
    </>
  );
}

function summary(r: Run): string {
  const parts = Object.entries(r.stats ?? {})
    .filter(([k, v]) => k !== "transfers_checked" && v > 0)
    .map(([k, v]) => `${KIND_LABEL[k] ?? k}: ${v}`);
  return parts.length ? parts.join(", ") : "no findings";
}

function Findings({ run }: { run: string }) {
  const q = useQuery({ queryKey: ["findings", run], queryFn: () => fetchFindings(run) });
  if (q.isPending) return <p>Loading…</p>;
  if (q.isError) return <p role="alert">Could not load findings.</p>;
  if (q.data.length === 0) return <p className="muted" data-testid="findings-empty">This run found nothing to report.</p>;
  return (
    <table data-testid="findings-table">
      <thead>
        <tr>
          <th>Finding</th>
          <th>Transfer</th>
          <th>Reference</th>
        </tr>
      </thead>
      <tbody>
        {q.data.map((f) => (
          <tr key={f.id} data-testid={`finding-${f.kind}`} className={`kind-${f.kind}`}>
            <td>{KIND_LABEL[f.kind] ?? f.kind}</td>
            <td><code>{f.providerTransferId ?? "—"}</code></td>
            <td>{f.reference ?? "—"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function ImportCsv() {
  const qc = useQueryClient();
  const [msg, setMsg] = useState<string | null>(null);
  return (
    <label className="file">
      Import expected payments (CSV)
      <input
        type="file"
        accept=".csv,text/csv"
        data-testid="import-csv"
        onChange={async (e) => {
          const input = e.currentTarget;
          const file = input.files?.[0];
          if (!file) return;
          setMsg(null);
          try {
            // Read before clearing the input: clearing it first can leave the read pending.
            const text = await file.text();
            input.value = "";
            const res = await apiRaw("POST", "/api/v1/expected-payments/import", text, "text/csv");
            const r = (await res.json()) as { imported: number; skippedSettled: number };
            setMsg(`Imported ${r.imported} (${r.skippedSettled} already settled).`);
            void qc.invalidateQueries({ queryKey: ["expected"] });
          } catch (err) {
            setMsg(err instanceof ApiError && err.code === "invalid_rows" ? "Some rows are invalid; nothing was imported." : "Import failed.");
          }
        }}
      />
      {msg && <span data-testid="import-result">{msg}</span>}
    </label>
  );
}

function ReportDownload() {
  const yesterday = new Date(Date.now() - 86_400_000).toISOString().slice(0, 10);
  const [date, setDate] = useState(yesterday);
  const [error, setError] = useState(false);
  return (
    <div className="row">
      <input type="date" value={date} onChange={(e) => setDate(e.target.value)} data-testid="report-date" />
      <button
        data-testid="report-download"
        onClick={async () => {
          setError(false);
          try {
            const res = await apiRaw("GET", `/api/v1/reports/daily?date=${encodeURIComponent(date)}`);
            if ((res.headers.get("content-type") ?? "").includes("application/json")) {
              const { url } = (await res.json()) as { url: string };
              window.location.assign(url); // short-lived presigned R2 URL
              return;
            }
            const blob = await res.blob();
            const a = document.createElement("a");
            a.href = URL.createObjectURL(blob);
            a.download = `vaultops-${date}.csv`;
            a.click();
            URL.revokeObjectURL(a.href);
          } catch {
            setError(true);
          }
        }}
      >
        Download daily report
      </button>
      {error && <span role="alert">Could not build the report.</span>}
    </div>
  );
}
