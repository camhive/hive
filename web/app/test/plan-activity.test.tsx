import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetCloudForTest } from "../src/lib/cloud";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { formatValue } from "../src/pages/Plan";
import { freeMonths, price } from "../src/lib/plan";

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// The seed tiers as the cloud serves them (internal/entitlements/plans.go); the page must show PLAN.md §11.1.
const tiers = [
  { id: "free", name: "Free", currency: "EUR", price_month_cents: 0, price_year_cents: 0, entitlements: { "hives.max": 1, "members.max": 0, "shares.max": 0, "sessions.concurrent": 1, "remote.quality": "sub", "export.remote_per_day": 5, "export.max_minutes": 5, "export.api": false, "notify.channels": "basic", "notify.devices": 1, "analytics.level": "motion", "ptz.enabled": true, "updates.early_access": false } },
  { id: "plus", name: "Plus", currency: "EUR", price_month_cents: 500, price_year_cents: 5000, entitlements: { "hives.max": 3, "members.max": 4, "shares.max": 5, "sessions.concurrent": 4, "remote.quality": "full", "export.remote_per_day": -1, "export.max_minutes": 60, "export.api": false, "notify.channels": "rich", "notify.devices": 5, "analytics.level": "objects", "ptz.enabled": true, "updates.early_access": false } },
  { id: "pro", name: "Pro", currency: "EUR", price_month_cents: 1200, price_year_cents: 12000, entitlements: { "hives.max": 10, "members.max": 14, "shares.max": 15, "sessions.concurrent": 10, "remote.quality": "full", "export.remote_per_day": -1, "export.max_minutes": -1, "export.api": true, "notify.channels": "all", "notify.devices": -1, "analytics.level": "full", "ptz.enabled": true, "updates.early_access": true } },
];

function stub() {
  const state = { plan: "free", promos: [] as string[], calls: [] as string[] };
  const events = Array.from({ length: 60 }, (_, i) => ({ id: 100 - i, at: new Date(Date.now() - i * 60_000).toISOString(), actor_type: "account", actor_id: "acc_1", action: i === 0 ? "hive.claim" : "session.login", target_type: i === 0 ? "hive" : undefined, target_id: i === 0 ? "hv_1" : undefined, details: i === 0 ? { name: "Home" } : {} }));
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const key = `${req.method} ${u.pathname}`;
    state.calls.push(`${key}${u.search}`);
    switch (key) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [{ id: "hv_1", name: "Home", status: "active", online: false, claimed_at: "" }], shared: [] });
      case "GET /v1/plans": return json(200, { plans: tiers });
      case "GET /v1/me/plan": { const t = tiers.find((x) => x.id === state.plan)!; return json(200, { plan: { id: t.id, name: t.name }, addons: [], promos: state.promos, entitlements: t.entitlements }); }
      case "POST /v1/me/promo": {
        const b = (await req.json()) as { code: string };
        if (b.code !== "BETA-PLUS") return json(404, { error: { code: "promo_invalid", message: "" } });
        if (state.promos.includes(b.code)) return json(409, { error: { code: "promo_redeemed", message: "" } });
        state.promos.push(b.code); state.plan = "plus";
        return json(200, { status: "redeemed", entitlements: tiers[1]!.entitlements });
      }
      case "GET /v1/me/activity": {
        const before = Number(u.searchParams.get("before") ?? Infinity);
        const page = events.filter((e) => e.id < before).slice(0, 50);
        return json(200, { events: page, next_before: page.length === 50 ? page[page.length - 1]!.id : undefined });
      }
      case "GET /v1/hives/hv_1/activity": return json(200, { events: events.slice(0, 1) });
      case "GET /v1/me/export": return new Response(JSON.stringify({ account: { id: "acc_1" } }), { status: 200, headers: { "Content-Type": "application/json", "Content-Disposition": "attachment" } });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return state;
}
function mount(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return userEvent.setup();
}
/** Unmounts, lets requests still in flight reach the stubbed fetch, then removes the stubs (a late request hitting the real fetch would mark the cloud down for the next test). */
async function settle() {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
}

afterEach(settle);

describe("plan page", () => {
  it("shows the current plan, the §11.1 table from the price list, and redeems a promo code", async () => {
    const state = stub();
    const user = mount("/plan");
    expect(await screen.findByTestId("current-plan")).toHaveTextContent("Free");
    const table = await screen.findByTestId("tiers");
    const cell = (key: string, plan: string) => within(table).getByText((_, el) => el?.getAttribute("data-key") === key && el.getAttribute("data-plan") === plan);
    // Rows of PLAN.md §11.1.
    expect(cell("hives.max", "free")).toHaveTextContent("1"); expect(cell("hives.max", "plus")).toHaveTextContent("3"); expect(cell("hives.max", "pro")).toHaveTextContent("10");
    expect(cell("members.max", "free")).toHaveTextContent("1"); expect(cell("members.max", "plus")).toHaveTextContent("5"); expect(cell("members.max", "pro")).toHaveTextContent("15");
    expect(cell("sessions.concurrent", "pro")).toHaveTextContent("10");
    expect(cell("remote.quality", "free")).toHaveTextContent("reduced quality (sub stream)"); expect(cell("remote.quality", "plus")).toHaveTextContent("full quality");
    expect(cell("shares.max", "free")).toHaveTextContent("no"); expect(cell("shares.max", "plus")).toHaveTextContent("5"); expect(cell("shares.max", "pro")).toHaveTextContent("15");
    expect(cell("export.remote_per_day", "free")).toHaveTextContent("5"); expect(cell("export.remote_per_day", "plus")).toHaveTextContent("unlimited");
    expect(cell("export.max_minutes", "free")).toHaveTextContent("5"); expect(cell("export.max_minutes", "plus")).toHaveTextContent("60"); expect(cell("export.max_minutes", "pro")).toHaveTextContent("unlimited");
    expect(cell("export.api", "pro")).toHaveTextContent("yes");
    expect(cell("notify.devices", "free")).toHaveTextContent("1"); expect(cell("notify.devices", "pro")).toHaveTextContent("unlimited");
    expect(cell("updates.early_access", "pro")).toHaveTextContent("yes");
    // The monthly price first; the yearly one reads as the alternative it is, not a second monthly price.
    const priceOf = (id: string) => within(table).getByTestId(`price-${id}`);
    expect(priceOf("free")).toHaveTextContent(/^free$/);
    expect(priceOf("plus")).toHaveTextContent(/^5€ \/ monthor 50€ a year — 2 months free$/);
    expect(priceOf("pro")).toHaveTextContent(/^12€ \/ monthor 120€ a year — 2 months free$/);

    await user.type(screen.getByLabelText("Code"), "NOPE");
    await user.click(screen.getByRole("button", { name: "Redeem" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("This promo code is not valid.");
    await user.clear(screen.getByLabelText("Code"));
    await user.type(screen.getByLabelText("Code"), "BETA-PLUS");
    await user.click(screen.getByRole("button", { name: "Redeem" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Code redeemed.");
    await waitFor(() => expect(screen.getByTestId("current-plan")).toHaveTextContent("Plus"));
    expect(state.promos).toEqual(["BETA-PLUS"]);
  });
  it("formats entitlement values", () => {
    const t = (k: string, o?: Record<string, unknown>) => (k === "plan.unlimited" ? "unlimited" : k === "plan.yes" ? "yes" : k === "plan.no" ? "no" : (o?.defaultValue as string) ?? k);
    expect(formatValue("hives.max", -1, t)).toBe("unlimited");
    expect(formatValue("members.max", 4, t)).toBe("5");
    expect(formatValue("shares.max", 0, t)).toBe("no");
    expect(formatValue("export.api", true, t)).toBe("yes");
    expect(formatValue("analytics.level", "custom", t)).toBe("custom");
    expect(formatValue("x", undefined, t)).toBe("—");
    expect(freeMonths(500, 5000)).toBe(2); expect(freeMonths(1200, 12000)).toBe(2); expect(freeMonths(500, 6000)).toBe(0); expect(freeMonths(500, 0)).toBe(0); expect(freeMonths(0, 0)).toBe(0);
    expect(price(0, "EUR")).toBe("0"); expect(price(500, "EUR")).toBe("5€"); expect(price(1250, "USD")).toBe("12.50$"); expect(price(700, "CHF")).toBe("7 CHF");
  });
});

describe("activity page", () => {
  it("lists events newest first with translated actions, pages by cursor, filters by hive, and downloads the export", async () => {
    const state = stub();
    const user = mount("/activity");
    const rows = await screen.findAllByTestId("event");
    expect(rows).toHaveLength(50);
    expect(rows[0]).toHaveTextContent("Hive claimed");
    expect(rows[0]).toHaveTextContent("hive hv_1");
    expect(rows[1]).toHaveTextContent("Signed in");
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.getAllByTestId("event")).toHaveLength(60));
    expect(state.calls.some((c) => c.startsWith("GET /v1/me/activity?before=51"))).toBe(true);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();

    await user.selectOptions(screen.getByLabelText("Show"), "hv_1");
    await waitFor(() => expect(screen.getAllByTestId("event")).toHaveLength(1));
    expect(state.calls.some((c) => c.startsWith("GET /v1/hives/hv_1/activity"))).toBe(true);

    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:x", revokeObjectURL: () => undefined }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    await user.click(screen.getByRole("button", { name: "Download my data (JSON)" }));
    await waitFor(() => expect(click).toHaveBeenCalled());
    expect(state.calls).toContain("GET /v1/me/export");
  });
});
