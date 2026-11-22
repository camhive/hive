import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";

/** Minimal RTCPeerConnection: gathering completes at once, connects right after the answer is applied. */
class FakePC extends EventTarget {
  static instances: FakePC[] = [];
  static failNext = 0;
  /** The hive sends sound: the audio track unmutes after the answer (#95). */
  static audio = false;
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
      for (const kind of ["video", "audio"]) {
        const track = Object.assign(new EventTarget(), { kind, muted: true });
        const ev = new Event("track") as Event & { track: unknown; streams: unknown[] };
        ev.track = track;
        ev.streams = [{ id: "ms", getTracks: () => [] }];
        this.dispatchEvent(ev);
        if (kind === "video" || FakePC.audio) track.dispatchEvent(new Event("unmute"));
      }
    });
  }
  close() { this.closed = true; }
}


const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const baseHive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.1.10",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" }, relay_host: "hv_1.relay.camhive.app" };
const cam = (id: string, name: string) => ({ id, name, group: "", enabled: true, source: { kind: "rtsp", address: "rtsp://x" }, streams: {}, created_at: "2026-11-09T00:00:00Z", updated_at: "2026-11-09T00:00:00Z" });
const thresholds = { bitrate_drop: 0.6, bitrate_ok: 0.8, night_picture: 0.25, night_ok: 0.35, missing_s: 30, missing_ok_s: 5, reconnects: 4, reconnects_ok: 1,
  clock_drift_s: 30, clock_every_h: 6, pattern_days: 3, pattern_minutes: 5, stream_changed_h: 24, baseline_days: 7, baseline_min_days: 3, late_checks: 3 };
const care = { camera: "cam_1", checked_at: "2026-11-22T23:00:00Z", degraded: true, since: "2026-11-22T22:40:00Z",
  reasons: [{ reason: "bitrate_drop", suggestion: "" }, { reason: "reconnects", suggestion: "" }],
  signals: { streams: [{ stream: "main", last_hour_kbps: 1000, usual_kbps: 2000, day_kbps: 2000, night: false }], missing_s: 0, late_s: 0, reconnects_hour: 12, reconnects_24h: 14,
    clock_drift_s: null, clock_checked_at: null, offline_minutes: [], pattern_hours: [], stream_change: null }, thresholds };

function stub(capabilities: string[]) {
  const calls: string[] = [];
  const hive = { ...baseHive, capabilities };
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    calls.push(`${req.method} ${u.pathname}`);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, { status: "ok" });
        case "GET /v0/cameras": return json(200, [cam("cam_1", "Door"), cam("cam_2", "Garden")]);
        case "POST /v0/cameras/cam_1/live/offer":
        case "POST /v0/cameras/cam_2/live/offer": return json(201, { sdp: "v=0 answer", session: "https://hv_1.h.camhive.app:16017/whep/x/s1" });
        case "POST /v0/live/candidates":
        case "POST /v0/live/close": return new Response(null, { status: 204 });
        case "GET /v0/health/cameras": return json(200, [
          { camera: "cam_1", enabled: true, online: true, streams: {}, care: { degraded: true, reasons: ["bitrate_drop", "reconnects"] } },
          { camera: "cam_2", enabled: true, online: true, streams: {}, care: { degraded: false, reasons: [] } }]);
        case "GET /v0/cameras/cam_1/care": return json(200, care);
        case "GET /v0/cameras/cam_1/ptz": return json(200, { supported: false });
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "v4.public.tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "owner", cameras: ["*"], hive, quality: "full", ice_servers: [] });
      case "GET /v1/hives/hv_1/sessions": return json(200, { sessions: [{ id: "s_1", account_id: "acc_1", kind: "online", client: "web", issued_at: "", expires_at: "", mine: true }], server_time: "" });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount() {
  transport.invalidateAll();
  vi.stubGlobal("RTCPeerConnection", FakePC);
  const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/live"] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return userEvent.setup();
}

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });
afterEach(async () => { cleanup(); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); });

describe("camera care in the app (#163)", () => {
  it("badges a degraded camera on its tile and in the list, and its page says what the hive saw and what to do", async () => {
    stub(["camera.care"]);
    const user = mount();
    const tiles = await screen.findAllByTestId("tile");
    const door = tiles.find((t) => t.dataset.camera === "cam_1")!;
    const garden = tiles.find((t) => t.dataset.camera === "cam_2")!;
    expect(await within(door).findByTestId("care-badge")).toHaveAccessibleName("Needs attention: bitrate drop, reconnects");
    expect(within(garden).queryByTestId("care-badge")).toBeNull();

    await user.click(screen.getByRole("button", { name: "List" }));
    const rows = await screen.findAllByTestId("camera");
    const doorRow = rows.find((r) => r.dataset.camera === "cam_1")!;
    expect(within(doorRow).getByTestId("care-badge")).toHaveTextContent("Needs attention");
    expect(within(rows.find((r) => r.dataset.camera === "cam_2")!).queryByTestId("care-badge")).toBeNull();

    await user.click(within(doorRow).getByRole("button", { name: "Open Door" }));
    const line = await screen.findByTestId("camera-care");
    expect(line).toHaveTextContent("The main stream sent 50 % of its usual bitrate in the last hour. A quieter scene also lowers the bitrate; if the picture looks wrong, check the cable or the PoE budget.");
    expect(line).toHaveTextContent("Reconnected 14 times in the last 24 hours.");
  });

  it("asks a hive without camera care for nothing", async () => {
    const calls = stub([]);
    const user = mount();
    const tiles = await screen.findAllByTestId("tile");
    await user.click(within(tiles.find((t) => t.dataset.camera === "cam_1")!).getByRole("button", { name: "Open Door" }));
    await waitFor(() => expect(calls).toContain("GET /v0/cameras/cam_1/ptz"));
    expect(screen.queryByTestId("camera-care")).toBeNull();
    expect(calls).not.toContain("GET /v0/cameras/cam_1/care");
  });
});
