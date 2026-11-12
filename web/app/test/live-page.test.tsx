import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";
import { backoffMs, streamFor } from "../src/lib/reconnect";

/** Minimal RTCPeerConnection: gathering completes at once, connects right after the answer is applied. */
class FakePC extends EventTarget {
  static instances: FakePC[] = [];
  static failNext = 0;
  localDescription: { type: string; sdp: string } | null = null;
  iceGatheringState = "complete";
  connectionState = "new";
  closed = false;
  constructor(public config: RTCConfiguration) {
    super();
    FakePC.instances.push(this);
  }
  addTransceiver() { return {}; }
  async createOffer() { return { type: "offer", sdp: "v=0 offer" }; }
  async setLocalDescription(d: { type: string; sdp: string }) { this.localDescription = d; }
  async setRemoteDescription() {
    queueMicrotask(() => {
      this.connectionState = "connected";
      this.dispatchEvent(new Event("connectionstatechange"));
      const ev = new Event("track") as Event & { streams: unknown[] };
      ev.streams = [{ id: "ms", getTracks: () => [] }];
      this.dispatchEvent(ev);
    });
  }
  close() { this.closed = true; }
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" }, relay_host: "hv_1.relay.camhive.app" };
const cam = (id: string, name: string, enabled = true) => ({ id, name, group: "", enabled, source: { kind: "rtsp", address: "rtsp://x" }, streams: {}, created_at: "2026-11-09T00:00:00Z", updated_at: "2026-11-09T00:00:00Z" });

type StubState = { role: "viewer" | "owner"; quality: "sub" | "full"; evicted: boolean; offerFails: number; lan: boolean; hiveStreams?: string[] };

/** The hive answers on its LAN name when `lan` is set (the viewer is at home); otherwise only the cloud is reachable. */
function stub(state: StubState) {
  const calls: string[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const key = `${req.method} ${u.host}${u.pathname}`;
    calls.push(key);
    if (u.host === "hv_1.h.camhive.app:16017" && !state.lan) throw new TypeError("Failed to fetch"); // not at home
    if (u.host === "hv_1.h.camhive.app:16017" || u.host === "hv_1.relay.camhive.app") {
      expect(req.headers.get("Authorization")).toBe(u.pathname === "/healthz" ? null : "Bearer v4.public.tok");
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, { status: "ok" });
        case "GET /v0/cameras": return json(200, [cam("cam_1", "Door"), cam("cam_2", "Garden"), cam("cam_3", "Cellar", false)]);
        case "POST /v0/cameras/cam_1/live/offer": {
          const body = (await req.json()) as { stream: string };
          (state.hiveStreams ??= []).push(body.stream);
          return json(201, { sdp: "v=0 answer", session: `https://hv_1.h.camhive.app:16017/whep/cam_1/${body.stream}/s1` });
        }
        case "POST /v0/live/candidates":
        case "POST /v0/live/close": return new Response(null, { status: 204 });
        case "GET /v0/health/cameras": return json(200, [
          { camera: "cam_1", enabled: true, online: true, streams: { main: { stream: "main", online: true, bitrate_kbps: 2500 }, sub: { stream: "sub", online: true, bitrate_kbps: 400 } } },
          { camera: "cam_2", enabled: true, online: false, streams: {} }]);
        case "GET /v0/cameras/cam_1/ptz": return json(200, { supported: true });
        case "GET /v0/cameras/cam_2/ptz": return json(200, { supported: false });
        case "GET /v0/cameras/cam_1/ptz/presets": return json(200, [{ token: "p1", name: "Gate" }]);
        case "POST /v0/cameras/cam_1/ptz/move": return new Response(null, { status: 204 });
        case "POST /v0/cameras/cam_1/ptz/stop": return new Response(null, { status: 204 });
        case "POST /v0/cameras/cam_1/ptz/presets/p1/goto": return new Response(null, { status: 204 });
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "v4.public.tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: state.role, cameras: ["*"], hive, quality: state.quality, ice_servers: [{ urls: ["stun:stun.camhive.app:3478"] }, { urls: ["turn:turn.camhive.app:3478"], username: "u", credential: "c" }] });
      case "GET /v1/hives/hv_1/sessions": return json(200, { sessions: state.evicted ? [] : [{ id: "s_1", account_id: "acc_1", kind: "online", client: "web", issued_at: "", expires_at: "", mine: true }], server_time: "" });
      case "POST /v1/hives/hv_1/signal/offer": {
        if (state.offerFails > 0) { state.offerFails--; return json(502, { error: { code: "hive_unreachable", message: "" } }); }
        const body = (await req.json()) as { camera: string; stream: string; sdp: string };
        return json(201, { sdp: "v=0 answer", session: `https://hive/whep/${body.camera}/${body.stream}`, ice_servers: [] });
      }
      case "POST /v1/hives/hv_1/signal/candidates": return new Response(null, { status: 204 });
      case "POST /v1/hives/hv_1/signal/close": return new Response(null, { status: 204 });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount() {
  transport.invalidateAll();
  FakePC.instances = [];
  vi.stubGlobal("RTCPeerConnection", FakePC);
  localStorage.clear();
  const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/live"] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return userEvent.setup();
}

// Queries of an unmounted page may still fire after the stubbed fetch is gone and mark the cloud down; each test starts clean.
beforeEach(() => { resetCloudForTest(); localStorage.clear(); });
/** Unmounts, lets requests still in flight reach the stubbed fetch, then removes the stubs (a late request hitting the real fetch would mark the cloud down for the next test). */
async function settle() {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
}

afterEach(settle);

describe("hive page", () => {
  it("shows every camera as a live tile, opens one with PTZ for owners, switches layout and remembers it", async () => {
    const calls = stub({ role: "owner", quality: "full", evicted: false, offerFails: 0, lan: true });
    const user = mount();
    const grid = await screen.findByTestId("grid");
    const tiles = await within(grid).findAllByTestId("tile");
    expect(tiles).toHaveLength(3); // the owner also sees the disabled camera, to switch it back on
    expect(screen.getByTestId("quality")).toHaveTextContent("Full quality");
    expect(screen.getByRole("button", { name: "Large tiles" })).toHaveAttribute("aria-pressed", "true");

    const door = tiles.find((t) => t.dataset.camera === "cam_1")!;
    await waitFor(() => expect(door.dataset.state).toBe("connected"));
    expect(door.dataset.stream).toBe("main");
    expect(within(door).getByTestId("tile-status")).toHaveTextContent("Live");
    expect(within(door).getByText("2500 kbit/s")).toBeInTheDocument();
    expect(FakePC.instances[0]!.config.iceServers).toHaveLength(2);
    // At home the offer goes straight to the hive; offline and disabled cameras are not offered.
    expect(calls.filter((c) => c.endsWith("/live/offer"))).toEqual(["POST hv_1.h.camhive.app:16017/v0/cameras/cam_1/live/offer"]);
    expect(calls.some((c) => c.includes("/signal/offer"))).toBe(false);

    const garden = tiles.find((t) => t.dataset.camera === "cam_2")!;
    await waitFor(() => expect(within(garden).getByTestId("tile-status")).toHaveTextContent("Camera offline"));
    const cellar = tiles.find((t) => t.dataset.camera === "cam_3")!;
    expect(within(cellar).getByTestId("tile-status")).toHaveTextContent("Disabled");
    expect(within(grid).queryByTestId("ptz")).toBeNull(); // PTZ lives on the opened camera only

    // Opening a camera puts it on top, live, and takes it out of the grid.
    await user.click(within(door).getByRole("button", { name: "Open Door" }));
    const detail = await screen.findByTestId("camera-detail");
    const big = within(detail).getByTestId("tile");
    expect(big.dataset.camera).toBe("cam_1");
    expect(big.dataset.stream).toBe("main");
    expect(within(screen.getByTestId("grid")).getAllByTestId("tile").map((t) => t.dataset.camera)).toEqual(["cam_3", "cam_2"]); // by group, then name
    expect(within(detail).queryByRole("button", { name: "Back to live" })).toBeNull();

    // PTZ: hold-to-move posts move then stop; presets go to the hive.
    const ptz = await within(detail).findByTestId("ptz");
    const up = within(ptz).getByRole("button", { name: "Tilt up" });
    await user.pointer([{ keys: "[MouseLeft>]", target: up }, { keys: "[/MouseLeft]" }]);
    await waitFor(() => expect(calls).toContain("POST hv_1.h.camhive.app:16017/v0/cameras/cam_1/ptz/move"));
    await waitFor(() => expect(calls).toContain("POST hv_1.h.camhive.app:16017/v0/cameras/cam_1/ptz/stop"));
    await user.selectOptions(within(ptz).getByRole("combobox", { name: "Presets" }), "p1");
    await waitFor(() => expect(calls).toContain("POST hv_1.h.camhive.app:16017/v0/cameras/cam_1/ptz/presets/p1/goto"));

    // Small tiles use the sub stream; the opened camera keeps the main one.
    await user.click(screen.getByRole("button", { name: "Small tiles" }));
    for (const t of within(screen.getByTestId("grid")).getAllByTestId("tile")) expect(t.dataset.stream).toBe("sub");
    expect(within(detail).getByTestId("tile").dataset.stream).toBe("main");

    // The list shows no video at all, only the other cameras; the choice is remembered.
    await user.click(screen.getByRole("button", { name: "List" }));
    expect(within(screen.getByTestId("grid")).queryAllByTestId("tile")).toHaveLength(0);
    expect(screen.getAllByTestId("camera").map((r) => r.dataset.camera)).toEqual(["cam_3", "cam_2"]);
    expect(localStorage.getItem("camhive.live.layout")).toBe("list");

    await user.click(within(detail).getByRole("button", { name: "Close camera" }));
    await waitFor(() => expect(screen.queryByTestId("camera-detail")).toBeNull());
    expect(screen.getAllByTestId("camera")).toHaveLength(3);
  }, 20_000);

  it("reconnects with backoff after a failed offer, hides disabled cameras and PTZ from viewers and labels reduced quality", async () => {
    stub({ role: "viewer", quality: "sub", evicted: false, offerFails: 1, lan: false });
    const user = mount();
    const tiles = await within(await screen.findByTestId("grid")).findAllByTestId("tile");
    expect(tiles).toHaveLength(2);
    const door = tiles.find((t) => t.dataset.camera === "cam_1")!;
    expect(door.dataset.stream).toBe("sub");
    await waitFor(() => expect(within(door).getByTestId("tile-status")).toHaveTextContent("Reconnecting…"));
    await waitFor(() => expect(door.dataset.state).toBe("connected"), { timeout: 5000 });
    expect(screen.getByTestId("quality")).toHaveTextContent("Reduced quality (plan)");
    expect(screen.queryByRole("button", { name: "Add camera" })).toBeNull();
    await user.click(within(door).getByRole("button", { name: "Open Door" }));
    const detail = await screen.findByTestId("camera-detail");
    expect(within(detail).getByTestId("tile").dataset.stream).toBe("sub");
    expect(within(detail).queryByTestId("ptz")).toBeNull();
    expect(within(detail).queryByRole("button", { name: "Edit camera" })).toBeNull();
  }, 15_000);

  it("at home goes straight to the hive at full quality, whatever the plan allows remotely", async () => {
    const state: StubState = { role: "viewer", quality: "sub", evicted: false, offerFails: 0, lan: true };
    const calls = stub(state);
    const user = mount();
    const door = (await within(await screen.findByTestId("grid")).findAllByTestId("tile")).find((t) => t.dataset.camera === "cam_1")!;
    await waitFor(() => expect(door.dataset.state).toBe("connected"));
    expect(door.dataset.stream).toBe("main");
    expect(state.hiveStreams).toEqual(["main"]);
    expect(calls.some((c) => c.includes("/signal/offer"))).toBe(false);
    expect(screen.getByTestId("quality")).toHaveTextContent("Full quality");
    await user.click(within(door).getByRole("button", { name: "Open Door" }));
    expect(within(await screen.findByTestId("camera-detail")).getByTestId("tile").dataset.stream).toBe("main");
  }, 15_000);

  it("shows a clear message and stops the tiles when the session was evicted", async () => {
    stub({ role: "owner", quality: "full", evicted: true, offerFails: 0, lan: false });
    mount();
    expect(await screen.findByTestId("evicted")).toHaveTextContent("Your session on this hive was ended");
    await waitFor(() => { for (const pc of FakePC.instances) expect(pc.closed).toBe(true); });
    for (const t of screen.getAllByTestId("tile")) expect(t.dataset.state).toBe("idle");
  });
});

describe("reconnect helpers", () => {
  it("backs off exponentially up to the cap and picks the stream from plan and tile size", () => {
    expect([0, 1, 2, 5, 9].map((a) => backoffMs(a))).toEqual([1000, 2000, 4000, 30_000, 30_000]);
    expect(streamFor("full", "detail")).toBe("main");
    expect(streamFor("full", "large")).toBe("main");
    expect(streamFor("full", "medium")).toBe("sub");
    expect(streamFor("full", "small")).toBe("sub");
    expect(streamFor("sub", "detail")).toBe("sub");
    expect(streamFor(undefined, "large")).toBe("main");
  });
});
