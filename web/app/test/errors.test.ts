import { describe, expect, it } from "vitest";
import { CamhiveError } from "@camhive/api";
import { errorKey, errorMessage } from "../src/lib/errors";
import en from "../src/i18n/en.json";
import de from "../src/i18n/de.json";

describe("error codes", () => {
  it("maps every known API code to a translation in both languages", () => {
    const codes = ["invalid_credentials", "locked", "plan_limit", "hive_offline", "promo_redeemed", "csrf"];
    for (const code of codes) {
      const key = errorKey(new CamhiveError(400, { code, message: "x" }));
      expect(key).toBe(`errors.${code}`);
      const leaf = code as keyof typeof en.errors;
      expect(en.errors[leaf]).toBeTruthy();
      expect(de.errors[leaf]).toBeTruthy();
    }
  });
  it("falls back for unknown codes and network failures", () => {
    expect(errorKey(new CamhiveError(500, { code: "mystery", message: "" }))).toBe("errors.unknown");
    expect(errorKey(new TypeError("fetch failed"))).toBe("errors.network");
  });
  it("translates hive codes and shows an unknown code verbatim", () => {
    const t = ((key: string, opts?: Record<string, unknown>) => (opts?.code ? `${key}:${String(opts.code)}` : key)) as unknown as Parameters<typeof errorMessage>[0];
    expect(errorMessage(t, new CamhiveError(401, { code: "clock_skew", message: "" }))).toBe("errors.clock_skew");
    expect(en.errors.clock_skew).toMatch(/clock/);
    expect(errorMessage(t, new CamhiveError(500, { code: "mystery", message: "" }))).toBe("errors.unexpected_code:mystery");
    expect(errorMessage(t, new Error("boom"))).toBe("errors.unknown");
    expect(errorMessage(t, new TypeError("fetch failed"))).toBe("errors.network");
  });
  it("de and en carry the same keys", () => {
    const flat = (o: Record<string, unknown>, p = ""): string[] =>
      Object.entries(o).flatMap(([k, v]) => (typeof v === "object" && v ? flat(v as Record<string, unknown>, `${p}${k}.`) : [`${p}${k}`]));
    expect(flat(de).sort()).toEqual(flat(en).sort());
  });
});
