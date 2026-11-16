import { describe, expect, it } from "vitest";
import { enableStep, passphraseProblem, START, type EnableFlow } from "../src/lib/archiveEncryption";
import { retryAfterSeconds } from "../src/lib/retryAfter";

const run = (...events: Parameters<typeof enableStep>[1][]): EnableFlow => events.reduce(enableStep, START);

describe("the enable flow (ADR-0019 §4)", () => {
  it("starts with the warning and asks for the passphrase only once it is understood", () => {
    expect(START).toEqual({ step: "warn" });
    expect(run("understood")).toEqual({ step: "choose" });
  });

  it("asks for the key-file download as soon as encryption is on, and finishes only after it", () => {
    expect(run("understood", "enabled")).toEqual({ step: "backup", saved: false });
    expect(run("understood", "enabled", "finish")).toEqual({ step: "backup", saved: false });
    expect(run("understood", "enabled", "saved")).toEqual({ step: "backup", saved: true });
    expect(run("understood", "enabled", "saved", "finish")).toEqual({ step: "done" });
  });

  it("ignores events out of order: no backup before encryption is on, no skipping the warning", () => {
    expect(run("enabled")).toEqual({ step: "warn" });
    expect(run("saved")).toEqual({ step: "warn" });
    expect(run("finish")).toEqual({ step: "warn" });
    expect(run("understood", "saved", "finish")).toEqual({ step: "choose" });
    expect(run("understood", "enabled", "understood")).toEqual({ step: "backup", saved: false });
  });
});

describe("the passphrase rule", () => {
  it("takes 10 to 256 characters, counted as the hive counts them", () => {
    expect(passphraseProblem("123456789", "123456789")).toBe("length");
    expect(passphraseProblem("1234567890", "1234567890")).toBeNull();
    expect(passphraseProblem("x".repeat(256), "x".repeat(256))).toBeNull();
    expect(passphraseProblem("x".repeat(257), "x".repeat(257))).toBe("length");
    const emoji = "🐝".repeat(10); // 20 UTF-16 units, 10 characters
    expect(passphraseProblem(emoji, emoji)).toBeNull();
    expect(passphraseProblem("🐝".repeat(9), "🐝".repeat(9))).toBe("length");
  });

  it("wants the same passphrase twice", () => {
    expect(passphraseProblem("correct horse", "correct horsE")).toBe("mismatch");
  });
});

describe("Retry-After as the browser sees it", () => {
  const h = (v?: string) => new Headers(v === undefined ? {} : { "Retry-After": v });
  it("is seconds when the hive exposes it, and unknown when CORS hides it (hives up to v0.1.7)", () => {
    expect(retryAfterSeconds(h("600"))).toBe(600);
    expect(retryAfterSeconds(h("0"))).toBe(0);
    expect(retryAfterSeconds(h())).toBeNull();
    expect(retryAfterSeconds(h("Wed, 21 Oct 2026 07:28:00 GMT"))).toBeNull();
  });
});
