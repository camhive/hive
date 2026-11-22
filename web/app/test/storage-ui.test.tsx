import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";

const GB = 1e9;
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const baseHive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.1.10",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app", capabilities: ["storage", "updates"] };

const camera = (id: string, name: string, perDay: number) => ({ id, name, bytes: 8 * perDay * GB, oldest: "2026-11-14T12:30:00Z", newest: "2026-11-22T11:40:00Z",
  bytes_per_day_24h: perDay * GB, bytes_per_day_7d: perDay * GB, days_held: 7.965, oldest_age_days: 7.98, retention_days: 30, follows_hive: true, effective_days: 11 });

// The hive's answer for the issue's case: 48 GB a day, a disk that gives 11 days.
const overview = {
  cameras: [camera("cam_door", "Door", 30), camera("cam_yard", "Yard", 18),
    { ...camera("cam_old", "Old", 0), bytes: 5 * GB, retention_days: 0, follows_hive: false, effective_days: 0 }], // unlimited, stopped recording
  disk: { total_bytes: 1000 * GB, archive_bytes: 384 * GB, export_bytes: 0, free_bytes: 190 * GB },
  settings: { retention_days: 30, min_free_gb: 10, export_max_gb: 10 },
  forecast: { bytes_per_day: 48 * GB, capacity_bytes: 552 * GB, disk_days: 11, horizon_days: 11, needed_bytes: 1461 * GB, fills_in_days: 0, retention_met: false, deleting_for_space: true },
};

function stub(hive: Record<string, unknown>, role: "owner" | "viewer", answer: Record<string, unknown>) {
  const calls: string[] = [];
  let current = { ...answer };
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const body = req.method === "PATCH" ? await req.text() : "";
    calls.push(`${req.method} ${u.pathname}${body ? " " + body : ""}`);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "GET /v0/storage": return json(200, current);
        case "PATCH /v0/hive/storage": {
          const s = JSON.parse(body) as { retention_days: number; min_free_gb: number };
          current = { ...current, settings: { ...overview.settings, ...s }, forecast: { ...overview.forecast, horizon_days: undefined, fills_in_days: undefined, retention_met: true } };
          return json(200, current);
        }
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, role === "owner" ? { hives: [hive], shared: [] } : { hives: [], shared: [hive] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role, cameras: ["*"], hive, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount() {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/settings"] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return userEvent.setup();
}

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });
afterEach(async () => { cleanup(); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("storage on the hive page (#150)", () => {
  it("shows the owner the figures and the forecast, validates the settings, confirms a deleting change and saves it", async () => {
    const calls = stub(baseHive, "owner", overview);
    const user = mount();
    const section = await screen.findByRole("region", { name: "Storage" });
    await user.click(within(section).getByRole("button", { name: "Show storage…" }));

    expect(await within(section).findByTestId("storage-forecast")).toHaveTextContent(
      "At 48 GB per day this disk holds 11 days; retention is 30 days, so it will effectively be 11.");
    expect(within(section).getByText("1 TB")).toBeInTheDocument();
    expect(within(section).getByText("384 GB")).toBeInTheDocument();
    expect(within(section).getByTestId("storage-short")).toHaveTextContent("that needs 1.5 TB of space");
    expect(within(section).getByTestId("storage-short")).toHaveTextContent("deleting the oldest footage now to keep 10 GiB free");
    const cams = within(section).getAllByTestId("storage-camera");
    expect(cams[0]).toHaveTextContent("Door · 7.9 days held, 240 GB, 30 GB per day · keeps 11 of 30 days");
    expect(cams[2]).toHaveTextContent(/Old · .* · keeps until space runs out$/);

    const days = within(section).getByLabelText("Keep footage for (days)");
    await user.clear(days);
    await user.type(days, "1.5");
    await user.click(within(section).getByRole("button", { name: "Save" }));
    expect(within(section).getByRole("alert")).toHaveTextContent("Enter whole days from 0 to 3650.");
    const floor = within(section).getByLabelText("Keep free on the disk (GiB)");
    await user.clear(days);
    await user.type(days, "30");
    await user.clear(floor);
    await user.type(floor, "932");
    await user.click(within(section).getByRole("button", { name: "Save" }));
    expect(within(section).getByRole("alert")).toHaveTextContent("Enter a whole number of GiB from 1 to 931.");
    expect(calls.filter((c) => c.startsWith("PATCH"))).toHaveLength(0);

    // Five days deletes what the cameras hold beyond it: the app asks first.
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await user.clear(floor);
    await user.type(floor, "10");
    await user.clear(days);
    await user.type(days, "5");
    await user.click(within(section).getByRole("button", { name: "Save" }));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(calls.filter((c) => c.startsWith("PATCH"))).toHaveLength(0);
    await user.click(within(section).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls).toContain('PATCH /v0/hive/storage {"retention_days":5,"min_free_gb":10}'));
    expect(await within(section).findByRole("status")).toHaveTextContent("within 5 minutes");
    expect(within(section).getByTestId("storage-forecast")).toHaveTextContent("retention is 5 days, which fits.");
  });

  it("gives a member only their cameras' days held", async () => {
    stub(baseHive, "viewer", { cameras: [{ id: "cam_yard", name: "Yard", bytes: 144 * GB, days_held: 7.965 }] });
    const user = mount();
    const section = await screen.findByRole("region", { name: "Storage" });
    expect(within(section).getByText("How many days of footage the hive holds for your cameras.")).toBeInTheDocument();
    await user.click(within(section).getByRole("button", { name: "Show storage…" }));
    expect(await within(section).findByTestId("storage-member")).toHaveTextContent("Yard: 7.9 days of footage, 144 GB");
    expect(within(section).queryByLabelText("Keep footage for (days)")).toBeNull();
  });

  it("asks the owner to update a hive without the storage capability (ADR-0022)", async () => {
    const calls = stub({ ...baseHive, version: "0.1.9", capabilities: ["updates"] }, "owner", overview);
    mount();
    const section = await screen.findByRole("region", { name: "Storage" });
    expect(within(section).getByTestId("needs-update")).toHaveTextContent("0.1.10");
    expect(within(section).queryByRole("button", { name: "Show storage…" })).toBeNull();
    expect(calls).not.toContain("GET /v0/storage");
  });
});
