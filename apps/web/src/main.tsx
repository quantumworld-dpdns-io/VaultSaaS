import { QueryClient, QueryClientProvider, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { accessToken, onAuthChange, signIn, signOut } from "./auth";
import { api, getMe, setCurrentOrg, type Me } from "./api";
import { Alerts, Members, Transfers } from "./OrgPanels";
import { linkInjectedWallet } from "./siwe";
import { Wallets } from "./Wallets";
import "./styles.css";

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1 } } });
const ORG_KEY = "vaultops.org";

function readLocal(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}
function writeLocal(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* storage unavailable: selection is per page load only */
  }
}

function App() {
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  useEffect(() => {
    const check = () => void accessToken().then((t) => setSignedIn(!!t));
    check();
    return onAuthChange(check);
  }, []);
  if (signedIn === null) return null;
  return (
    <main>
      <header>
        <h1>VaultOps</h1>
        <span className="tag">Treasury &amp; Reconciliation</span>
        {signedIn && (
          <button
            className="link"
            onClick={() => void signOut().then(() => {
              queryClient.clear();
              setSignedIn(false);
            })}
            data-testid="sign-out"
          >
            Sign out
          </button>
        )}
      </header>
      {signedIn ? <Dashboard /> : <Login onSignedIn={() => setSignedIn(true)} />}
    </main>
  );
}

function Login({ onSignedIn }: { onSignedIn: () => void }) {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      className="card"
      data-testid="login"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        signIn(email).then((r) => (r === "signed_in" ? onSignedIn() : setSent(true)), () => setError("Sign-in failed."));
      }}
    >
      <h2>Sign in</h2>
      <p className="muted">We email you a one-time link. Wallets can be linked after signing in; they are not a way to sign in.</p>
      <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@company.com" data-testid="login-email" required />
      <button type="submit" data-testid="login-submit">Continue</button>
      {sent && <p>Check your inbox for the sign-in link.</p>}
      {error && <p role="alert">{error}</p>}
    </form>
  );
}

function Dashboard() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: getMe });
  const [org, setOrg] = useState<string | null>(readLocal(ORG_KEY));
  const [inviteError, setInviteError] = useState(false);

  // Accept ?invite=<token> once after sign-in.
  useEffect(() => {
    const token = new URLSearchParams(window.location.search).get("invite");
    if (!token) return;
    api<{ organizationId: string }>("POST", "/api/v1/invitations/accept", { token })
      .then((r) => chooseOrg(r.organizationId))
      .catch(() => setInviteError(true))
      .finally(() => {
        window.history.replaceState(null, "", window.location.pathname);
        void qc.invalidateQueries({ queryKey: ["me"] });
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function chooseOrg(id: string) {
    writeLocal(ORG_KEY, id);
    setCurrentOrg(id);
    setOrg(id);
    qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
  }

  if (me.isPending) return <p>Loading…</p>;
  if (me.isError) return <p role="alert">Could not load your account.</p>;
  const orgs = me.data.organizations;
  const active = orgs.find((o) => o.id === org) ?? orgs[0];
  setCurrentOrg(active?.id ?? null);

  return (
    <>
      {inviteError && <p role="alert" data-testid="invite-error">This invitation is invalid, expired, or for another email.</p>}
      <section className="row" data-testid="org-bar">
        {orgs.length > 0 && (
          <select value={active?.id} onChange={(e) => chooseOrg(e.target.value)} data-testid="org-switcher">
            {orgs.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name} ({o.role})
              </option>
            ))}
          </select>
        )}
        <CreateOrg onCreated={chooseOrg} />
      </section>
      {active ? (
        <OrgView key={active.id} canAdmin={active.role === "owner" || active.role === "admin"} />
      ) : (
        <p data-testid="no-orgs">Create an organization to get started.</p>
      )}
      <LinkedWallets me={me.data} />
    </>
  );
}

function CreateOrg({ onCreated }: { onCreated: (id: string) => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [token, setToken] = useState<string | null>(null);
  const m = useMutation({
    mutationFn: () => api<{ id: string; webhookToken: string }>("POST", "/api/v1/orgs", { name }),
    onSuccess: (r) => {
      setToken(r.webhookToken);
      setName("");
      void qc.invalidateQueries({ queryKey: ["me"] });
      onCreated(r.id);
    },
  });
  return (
    <>
      <form
        className="row"
        onSubmit={(e) => {
          e.preventDefault();
          m.mutate();
        }}
      >
        <input placeholder="New organization" value={name} onChange={(e) => setName(e.target.value)} data-testid="org-name" required />
        <button type="submit" data-testid="org-create">Create</button>
      </form>
      {token && (
        <p data-testid="webhook-url">
          BitGo webhook URL (shown once): <code>{`${import.meta.env.VITE_API_BASE ?? ""}/webhooks/bitgo/${token}`}</code>
        </p>
      )}
    </>
  );
}

function OrgView({ canAdmin }: { canAdmin: boolean }) {
  return (
    <>
      <section>
        <h2>Wallets</h2>
        <Wallets canAdmin={canAdmin} />
      </section>
      <section>
        <h2>Transfers</h2>
        <Transfers />
      </section>
      <section>
        <h2>Security alerts</h2>
        <Alerts />
      </section>
      <section>
        <h2>Members</h2>
        <Members canAdmin={canAdmin} />
      </section>
    </>
  );
}

function LinkedWallets({ me }: { me: Me }) {
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  return (
    <section>
      <h2>Linked wallets</h2>
      <p className="muted">Used to confirm sensitive actions. Linking a wallet never signs you in and cannot move funds.</p>
      <ul data-testid="linked-wallets">
        {me.linkedWallets.map((w) => (
          <li key={w.address}>
            <code>{w.address}</code>
          </li>
        ))}
      </ul>
      <button
        onClick={() => {
          setError(null);
          linkInjectedWallet().then(() => qc.invalidateQueries({ queryKey: ["me"] }), (e: Error) => setError(e.message));
        }}
        data-testid="link-wallet"
      >
        Link a wallet
      </button>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
