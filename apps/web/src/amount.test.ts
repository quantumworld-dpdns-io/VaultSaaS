import { describe, expect, it } from "vitest";
import { decimalsFor, formatBaseUnits } from "./amount";

describe("formatBaseUnits", () => {
  it("formats without float rounding", () => {
    expect(formatBaseUnits("150000000", 8)).toBe("1.5");
    expect(formatBaseUnits("2500000000000000000", 18)).toBe("2.5");
    expect(formatBaseUnits("123456789012345678901234567890", 18, 18)).toBe("123,456,789,012.34567890123456789");
    expect(formatBaseUnits("1", 18, 18)).toBe("0.000000000000000001");
    expect(formatBaseUnits("-100", 2)).toBe("-1");
  });
  it("knows testnet coins", () => {
    expect(decimalsFor("tbtc4")).toBe(8);
    expect(decimalsFor("hteth")).toBe(18);
    expect(decimalsFor("hteth:tusdc")).toBe(6);
  });
});
