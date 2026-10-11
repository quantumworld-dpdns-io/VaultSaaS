import { describe, expect, it } from "vitest";
import { buildMessage } from "./siwe";

describe("SIWE message", () => {
  it("matches the EIP-4361 layout the core verifies", () => {
    const msg = buildMessage("app.vaultops.example", "0xAbC0000000000000000000000000000000000000", "Link it.", "0123456789abcdef", new Date("2026-10-11T08:00:00.123Z"));
    expect(msg).toBe(
      "app.vaultops.example wants you to sign in with your Ethereum account:\n" +
        "0xAbC0000000000000000000000000000000000000\n\nLink it.\n\n" +
        "URI: https://app.vaultops.example\nVersion: 1\nChain ID: 1\nNonce: 0123456789abcdef\nIssued At: 2026-10-11T08:00:00Z",
    );
  });
});
