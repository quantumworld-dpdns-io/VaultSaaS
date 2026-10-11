import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Wallets } from "./Wallets";
import "./styles.css";

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1 } } });

function App() {
  return (
    <main>
      <header>
        <h1>VaultOps</h1>
        <span className="tag">Treasury &amp; Reconciliation</span>
      </header>
      <section>
        <h2>Wallets</h2>
        <Wallets />
      </section>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
