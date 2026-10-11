// Base-unit amounts arrive as decimal strings. Formatting uses BigInt string
// math only — never float (plan §4).

const DECIMALS: Record<string, number> = {
  btc: 8, tbtc: 8, tbtc4: 8,
  eth: 18, hteth: 18, teth: 18,
  usdc: 6, tusdc: 6, usdt: 6,
};

export function decimalsFor(coin: string): number {
  const c = coin.toLowerCase();
  if (c in DECIMALS) return DECIMALS[c]!;
  // ERC-20 style "chain:token" ids fall back on the token part
  const token = c.split(":").pop() ?? c;
  return DECIMALS[token] ?? 0;
}

export function formatBaseUnits(value: string, decimals: number, maxFraction = 8): string {
  if (!/^-?\d+$/.test(value)) return value;
  const negative = value.startsWith("-");
  const v = BigInt(negative ? value.slice(1) : value);
  const base = 10n ** BigInt(decimals);
  const whole = (v / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  let frac = decimals > 0 ? (v % base).toString().padStart(decimals, "0").slice(0, maxFraction) : "";
  frac = frac.replace(/0+$/, "");
  return (negative ? "-" : "") + whole + (frac ? "." + frac : "");
}
