// Links an injected EVM wallet (EIP-1193) to the signed-in user with an
// EIP-4361 message. Linking only — it never signs anyone in (D4).
import { api } from "./api";

interface Eip1193 {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>;
}

export function buildMessage(domain: string, address: string, statement: string, nonce: string, issuedAt: Date): string {
  return [
    `${domain} wants you to sign in with your Ethereum account:`,
    address,
    "",
    statement,
    "",
    `URI: https://${domain}`,
    "Version: 1",
    "Chain ID: 1",
    `Nonce: ${nonce}`,
    `Issued At: ${issuedAt.toISOString().replace(/\.\d{3}Z$/, "Z")}`,
  ].join("\n");
}

export async function linkInjectedWallet(): Promise<string> {
  const eth = (window as unknown as { ethereum?: Eip1193 }).ethereum;
  if (!eth) throw new Error("No browser wallet found.");
  const [address] = (await eth.request({ method: "eth_requestAccounts" })) as string[];
  if (!address) throw new Error("No account selected.");
  const n = await api<{ nonce: string; domain: string; statement: string }>("POST", "/api/v1/me/wallets/nonce");
  // Wallets show the checksummed form; the server verifies EIP-55.
  const msg = buildMessage(n.domain, address, n.statement, n.nonce, new Date());
  const signature = (await eth.request({ method: "personal_sign", params: [toHex(msg), address] })) as string;
  const res = await api<{ address: string }>("POST", "/api/v1/me/wallets", { message: msg, signature });
  return res.address;
}

function toHex(s: string): string {
  return "0x" + Array.from(new TextEncoder().encode(s), (b) => b.toString(16).padStart(2, "0")).join("");
}
