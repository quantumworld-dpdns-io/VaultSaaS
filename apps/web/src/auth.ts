// Sign-in. Production uses Supabase Auth (email one-time link); local
// development and the Robot suites use services/core/cmd/mockauth, which
// issues Supabase-shaped tokens. Wallets are never a sign-in method (D4).
import { createClient, type SupabaseClient } from "@supabase/supabase-js";

const MODE: "supabase" | "mock" = import.meta.env.VITE_AUTH_MODE === "mock" ? "mock" : "supabase";
const MOCK_URL: string = import.meta.env.VITE_MOCK_AUTH_URL ?? "http://127.0.0.1:8095";
const MOCK_KEY = "vaultops.mockToken";

// The anon key is public by design; never a service key (core principle 5).
let supabase: SupabaseClient | null = null;
function sb(): SupabaseClient {
  if (!supabase) supabase = createClient(import.meta.env.VITE_SUPABASE_URL ?? "", import.meta.env.VITE_SUPABASE_ANON_KEY ?? "");
  return supabase;
}

function store(): Storage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

export async function accessToken(): Promise<string | null> {
  if (MODE === "mock") return store()?.getItem(MOCK_KEY) ?? null;
  const { data } = await sb().auth.getSession();
  return data.session?.access_token ?? null;
}

/** Starts sign-in. Returns "signed_in" (mock) or "link_sent" (Supabase email link). */
export async function signIn(email: string): Promise<"signed_in" | "link_sent"> {
  if (MODE === "mock") {
    const res = await fetch(MOCK_URL + "/__mock/token", { method: "POST", body: JSON.stringify({ email }) });
    if (!res.ok) throw new Error("sign-in failed");
    const { access_token } = (await res.json()) as { access_token: string };
    store()?.setItem(MOCK_KEY, access_token);
    return "signed_in";
  }
  const { error } = await sb().auth.signInWithOtp({ email, options: { emailRedirectTo: window.location.href } });
  if (error) throw error;
  return "link_sent";
}

export async function signOut(): Promise<void> {
  if (MODE === "mock") store()?.removeItem(MOCK_KEY);
  else await sb().auth.signOut();
}

export function onAuthChange(cb: () => void): () => void {
  if (MODE === "mock") return () => {};
  const { data } = sb().auth.onAuthStateChange(() => cb());
  return () => data.subscription.unsubscribe();
}
