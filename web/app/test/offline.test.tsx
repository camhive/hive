import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { cloudReachable, reportCloud, resetCloudForTest } from "../src/lib/cloud";
import { clearOffline, readOffline, writeOffline, offlineFetchedAt } from "../src/lib/offline";
import { cacheSet } from "../src/lib/cache";
import { hiveSignalling } from "../src/lib/live";
import { createHiveClient } from "@camhive/api";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app" };
const grant = (offline: boolean) => ({ token: offline ? "off-tok" : "on-tok", session_id: offline ? "s_off" : "s_on", expires_at: new Date(Date.now() + (offline ? 7 * 86_400_000 : 900_000)).toISOString(), offline, role: "owner", cameras: ["*"], hive, quality: "full", ice_servers: [] });

class FakePC extends EventTarget {
  static instances: FakePC[] = [];
  localDescription: { type: string; sdp: string } | null = null;
  iceGatheringState = "complete";
  connectionState = "new";
  constructor(public config: RTCConfiguration) { super(); FakePC.instances.push(this); }
  addTransceiver() { return {}; }
  async createOffer() { return { type: "offer", sdp: "v=0 offer" }; }
  async setLocalDescription(d: { type: string; sdp: string }) { this.localDescription = d; }
  async setRemoteDescription() { queueMicrotask(() => { this.connectionState = "connected"; this.dispatchEvent(new Event("connectionstatechange")); }); }
  close() {}
}

/** Unmounts, lets requests still in flight reach the stubbed fetch, then removes the stubs (a late request hitting the real fetch would mark the cloud down for the next test). */
async function settle() {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
}

afterEach(async () => { await settle(); resetCloudForTest(); localStorage.clear();  });

describe("cloud reachability store", () => {
  it("flips on network failures and back on any answer", () => {
    expect(cloudReachable()).toBe(true);
    reportCloud(false);
    expect(cloudReachable()).toBe(false);
    reportCloud(true);
    expect(cloudReachable()).toBe(true);
  });
});

describe("offline grants", () => {
  it("stores, reads while valid, drops when expired", () => {
    const g = grant(true);
    writeOffline("hv_1", g as never, 1000);
    expect(readOffline("hv_1")?.token).toBe("off-tok");
    expect(offlineFetchedAt("hv_1")).toBe(1000);
    expect(readOffline("hv_1", Date.parse(g.expires_at) + 1)).toBeNull();
    clearOffline("hv_1");
    expect(readOffline("hv_1")).toBeNull();
  });
});

describe("hive signalling", () => {
  it("posts offer, candidates and close to the hive's local endpoints with the bearer token", async () => {
    const calls: string[] = [];
    const client = createHiveClient({ baseUrl: "https://hv", token: "off-tok", fetch: async (input, init) => {
      const req = new Request(input, init);
      calls.push(`${req.method} ${new URL(req.url).pathname} ${req.headers.get("Authorization")} ${await req.text()}`);
      if (req.url.endsWith("/live/offer")) return json(201, { sdp: "v=0 answer", session: "http://engine/cam/whep/s1" });
      return new Response(null, { status: 204 });
    } });
    const s = hiveSignalling(client);
    const a = await s.offer({ camera: "cam_1", sdp: "v=0 offer", stream: "sub" });
    expect(a).toEqual({ sdp: "v=0 answer", session: "http://engine/cam/whep/s1", ice_servers: [] });
    await s.candidates(a.session, "a=mid:0\r\n");
    await s.close(a.session);
    expect(calls[0]).toBe('POST /v0/cameras/cam_1/live/offer Bearer off-tok {"sdp":"v=0 offer","stream":"sub"}');
    expect(calls[1]).toContain("POST /v0/live/candidates Bearer off-tok");
    expect(calls[2]).toContain("POST /v0/live/close Bearer off-tok");
  });
});

describe("live view with the cloud down", () => {
  it("shows the banner, uses the stored offline grant and signals through the hive on the LAN", async () => {
    writeOffline("hv_1", grant(true) as never);
    const calls: string[] = [];
    vi.stubGlobal("RTCPeerConnection", FakePC);
    FakePC.instances = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const u = new URL(req.url);
      calls.push(`${req.method} ${u.host}${u.pathname}`);
      if (u.host === "hv_1.h.camhive.app:16017") {
        expect(req.headers.get("Authorization") ?? "none").toBe(u.pathname === "/healthz" ? "none" : "Bearer off-tok");
        switch (`${req.method} ${u.pathname}`) {
          case "GET /healthz": return json(200, {});
          case "GET /v0/cameras": return json(200, [{ id: "cam_1", name: "Door", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: {}, created_at: "", updated_at: "" }]);
          case "GET /v0/health/cameras": return json(200, [{ camera: "cam_1", enabled: true, online: true, streams: {} }]);
          case "POST /v0/cameras/cam_1/live/offer": return json(201, { sdp: "v=0 answer", session: "http://e/whep/1" });
          case "GET /v0/cameras/cam_1/ptz": return json(200, { supported: false });
          default: return json(404, { code: "not_found", message: "" });
        }
      }
      // The cloud is dead; the app runs on what it remembered from the last online visit.
      throw new TypeError("Failed to fetch");
    }) as typeof fetch);
    cacheSet("me", { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
    cacheSet("hives", { hives: [hive], shared: [] });
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/live"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    expect(await screen.findByTestId("cloud-down")).toHaveTextContent("cannot be reached");
    expect(await screen.findByTestId("local-mode")).toBeInTheDocument();
    const tile = await screen.findByTestId("tile");
    await waitFor(() => expect(tile.dataset.state).toBe("connected"));
    expect(within(tile).getByTestId("tile-status")).toHaveTextContent("Live");
    expect(calls).toContain("POST hv_1.h.camhive.app:16017/v0/cameras/cam_1/live/offer");
    expect(calls.some((c) => c.includes("/v1/hives/hv_1/signal/offer"))).toBe(false);
    expect(calls.some((c) => c.includes("/v1/hives/hv_1/access"))).toBe(false);
  }, 15_000);
});
