import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" }, relay_host: "hv_1.relay.camhive.app" };
const camera = (id: string, name: string) => ({ id, name, group: "", enabled: true, source: { kind: "rtsp", address: "rtsp://x" }, streams: {}, created_at: "", updated_at: "" });

/** The hive's action log, newest first: the three of the issue, a camera change, then old filler. */
function log(total: number) {
  const at = (min: number) => new Date(Date.UTC(2026, 10, 16, 12, 0) - min * 60_000).toISOString();
  const head = [
    { id: 1000, at: at(1), actor: "updater", action: "update.applied", target: "hive", details: { version: "0.1.7" } },
    { id: 999, at: at(2), actor: "acc_1", action: "export.done", target: "exp_1", details: { camera: "cam_1", stream: "main" } },
    { id: 998, at: at(3), actor: "acc_1", action: "archive.delete", target: "cam_1", details: { files: 3 } },
    { id: 997, at: at(4), actor: "cloud", action: "camera.update", target: "cam_1", details: { name: "Door", group: null, enabled: null } },
    { id: 996, at: at(5), actor: "hive", action: "something.new", target: "", details: {} },
    { id: 995, at: at(6), actor: "acc_1", action: "archive.delete", target: "*", details: {} },
    { id: 994, at: at(7), actor: "acc_1", action: "archive.delete", target: "cam_1,cam_2", details: {} },
    { id: 993, at: at(8), actor: "acc_1", action: "archive.encryption.unlock_failed", target: "", details: {} },
    { id: 992, at: at(9), actor: "cloud", action: "signal.offer", target: "cam_2", details: {} },
    { id: 991, at: at(10), actor: "local", action: "local_login.failure", target: "", details: {} },
    { id: 990, at: at(11), actor: "cloud", action: "tunnel.open", target: "", details: {} },
  ];
  const rest = Array.from({ length: total - head.length }, (_, i) => ({ id: 989 - i, at: at(12 + i), actor: "acc_1", action: "camera.add", target: "cam_1", details: {} }));
  return [...head, ...rest];
}

function stub(opts: { own: boolean; total: number; only?: unknown[] }) {
  const limits: number[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, { status: "ok" });
        case "GET /v0/cameras": return json(200, [camera("cam_1", "Door"), camera("cam_2", "Garden")]);
        case "GET /v0/actions": {
          if (!opts.own) return json(403, { code: "forbidden", message: "" });
          const limit = Number(u.searchParams.get("limit") ?? 100);
          limits.push(limit);
          return json(200, (opts.only ?? log(opts.total)).slice(0, limit));
        }
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, opts.own ? { hives: [hive], shared: [] } : { hives: [], shared: [hive] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: opts.own ? "owner" : "viewer", cameras: ["*"], hive, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return limits;
}

function mount(path: string) {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return Object.assign(userEvent.setup(), { router });
}

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });
afterEach(async () => {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
});

describe("the hive's own activity (#94)", () => {
  it("lists the hive's actions with actor, time, kind and target, and loads more as the API allows", async () => {
    const limits = stub({ own: true, total: 52 });
    const user = mount("/hives/hv_1");
    await user.click(await screen.findByRole("link", { name: "Activity" }));
    const list = await screen.findByRole("list", { name: "Activity on this hive" });
    await user.click(await screen.findByRole("checkbox", { name: /Show cloud sessions/ }));
    const rows = await within(list).findAllByTestId("hive-action");
    expect(rows).toHaveLength(50);
    expect(rows[0]).toHaveTextContent("Update installed");
    expect(rows[0]).toHaveTextContent("Updater");
    expect(rows[0]).toHaveTextContent("0.1.7");
    expect(rows[1]).toHaveTextContent("Export finished");
    expect(rows[1]).toHaveTextContent("you");
    expect(rows[1]).toHaveTextContent("· Door"); // the export's camera, not its job id
    expect(rows[1]).not.toHaveTextContent("exp_1");
    expect(rows[2]).toHaveTextContent("Recordings deleted");
    expect(rows[2]).toHaveTextContent("Door"); // the target camera by name
    expect(rows[3]).toHaveTextContent("Camera changed");
    expect(rows[3]).toHaveTextContent("Camhive cloud");
    expect(rows[3]).toHaveTextContent('{"name":"Door"}'); // no nulls
    expect(rows[5]).toHaveTextContent("Recordings deleted · all cameras");
    expect(rows[6]).toHaveTextContent("Recordings deleted · Door, Garden");
    expect(rows[7]).toHaveTextContent("Wrong passphrase when unlocking");
    expect(rows[8]).toHaveTextContent("Live view started · Garden");
    expect(rows[4]).toHaveTextContent("something.new"); // a kind this app does not know yet
    expect(rows[0]).toHaveTextContent(new Date(Date.UTC(2026, 10, 16, 11, 59)).toLocaleString());

    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(within(list).getAllByTestId("hive-action")).toHaveLength(52));
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull(); // fewer than asked: that was all
    expect(limits).toEqual([50, 100]);
  }, 20_000);

  it("filters by kind and text, hides cloud sessions by default, keeps the filter in the URL and through Load more (#125)", async () => {
    stub({ own: true, total: 52 });
    const user = mount("/hives/hv_1/activity");
    const list = await screen.findByRole("list", { name: "Activity on this hive" });
    await waitFor(() => expect(within(list).getAllByTestId("hive-action")).toHaveLength(48)); // 50 loaded, 2 from the cloud hidden
    const kinds = () => within(list).getAllByTestId("hive-action").map((r) => r.dataset.action);
    expect(kinds()).not.toContain("signal.offer");
    expect(kinds()).not.toContain("tunnel.open");
    const cloud = screen.getByRole("checkbox", { name: "Show cloud sessions (2)" });
    expect(cloud).not.toBeChecked();

    // Only updates and sign-ins.
    await user.click(screen.getByRole("button", { name: "Updates" }));
    await user.click(screen.getByRole("button", { name: "Sign-ins and access" }));
    expect(screen.getByRole("button", { name: "Updates" })).toHaveAttribute("aria-pressed", "true");
    await waitFor(() => expect(kinds()).toEqual(["update.applied", "local_login.failure"]));
    expect(user.router.state.location.search).toEqual({ kind: "updates,access" });

    // Free text over label, target and actor.
    await user.click(screen.getByRole("button", { name: "Updates" }));
    await user.click(screen.getByRole("button", { name: "Sign-ins and access" }));
    await user.type(screen.getByRole("searchbox", { name: "Search activity" }), "garden");
    await waitFor(() => expect(kinds()).toEqual(["archive.delete"])); // "Door, Garden"
    expect(screen.getByRole("checkbox", { name: "Show cloud sessions (1)" })).toBeInTheDocument(); // the live view of Garden
    await user.click(screen.getByRole("checkbox", { name: "Show cloud sessions (1)" }));
    await waitFor(() => expect(kinds()).toEqual(["archive.delete", "signal.offer"]));
    expect(user.router.state.location.search).toEqual({ q: "garden", cloud: true });

    await user.clear(screen.getByRole("searchbox", { name: "Search activity" }));
    await user.click(screen.getByRole("checkbox", { name: "Show cloud sessions (2)" }));
    await user.click(screen.getByRole("button", { name: "Cameras" }));
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(kinds()).toHaveLength(42)); // 41 camera.add of filler plus the camera change
    expect(kinds().every((k) => k?.startsWith("camera."))).toBe(true);
  }, 20_000);

  it("searches the words the list shows, not the raw kind", async () => {
    stub({ own: true, total: 20 });
    const user = mount("/hives/hv_1/activity");
    const list = await screen.findByRole("list", { name: "Activity on this hive" });
    await user.type(screen.getByRole("searchbox", { name: "Search activity" }), "installed");
    await waitFor(() => expect(within(list).getAllByTestId("hive-action").map((r) => r.dataset.action)).toEqual(["update.applied"]));
  });

  it("says when only cloud sessions are hidden, and offers more only when there is more", async () => {
    stub({ own: true, total: 0, only: [{ id: 2, at: "2026-11-16T12:00:00Z", actor: "cloud", action: "license.set", target: "", details: {} }, { id: 1, at: "2026-11-16T11:00:00Z", actor: "cloud", action: "tunnel.open", target: "", details: {} }] });
    const user = mount("/hives/hv_1/activity");
    expect(await screen.findByText("Only cloud sessions so far. Tick “Show cloud sessions” to see them.")).toBeInTheDocument();
    expect(screen.queryByText(/Load more/)).toBeNull();
    await user.type(screen.getByRole("searchbox", { name: "Search activity" }), "door");
    expect(await screen.findByText("Nothing matches this filter.")).toBeInTheDocument();
    expect(screen.queryByText(/Load more/)).toBeNull(); // two rows, all there is
  });

  it("opens with the filter from the URL", async () => {
    stub({ own: true, total: 20 });
    mount("/hives/hv_1/activity?kind=updates");
    const list = await screen.findByRole("list", { name: "Activity on this hive" });
    await waitFor(() => expect(within(list).getAllByTestId("hive-action").map((r) => r.dataset.action)).toEqual(["update.applied"]));
    expect(screen.getByRole("button", { name: "Updates" })).toHaveAttribute("aria-pressed", "true");
  });

  it("shows no Activity tab to members and says why on the page", async () => {
    stub({ own: false, total: 5 });
    mount("/hives/hv_1/activity");
    expect(await screen.findByText("Only the hive's owner sees its activity.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Activity" })).toBeNull();
  });
});
