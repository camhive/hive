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
import { LiveTile } from "../src/components/LiveTile";
import type { Signalling } from "../src/lib/live";
import type { HiveCamera } from "@camhive/api";

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

let router: ReturnType<typeof makeRouter>;

function mount() {
  transport.invalidateAll();
  FakePC.instances = [];
  FakePC.audio = false;
  vi.stubGlobal("RTCPeerConnection", FakePC);
  localStorage.clear();
  router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/live"] }));
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

  it("opens a camera at its recordings from a tile or a list row; tapping the tile still opens it live (#84)", async () => {
    stub({ role: "viewer", quality: "full", evicted: false, offerFails: 0, lan: true });
    const user = mount();
    let door = (await within(await screen.findByTestId("grid")).findAllByTestId("tile")).find((t) => t.dataset.camera === "cam_1")!;
    await user.click(within(door).getByRole("button", { name: "Open Door" }));
    expect(within(await screen.findByTestId("camera-detail")).getByTestId("tile").dataset.camera).toBe("cam_1"); // live, as before
    expect(router.state.location.search).toEqual({ camera: "cam_1" });
    await user.click(screen.getByRole("button", { name: "Close camera" }));
    await waitFor(() => expect(screen.queryByTestId("camera-detail")).toBeNull());

    const grid = await screen.findByTestId("grid");
    door = (await within(grid).findAllByTestId("tile")).find((t) => t.dataset.camera === "cam_1")!;
    const recordings = within(door).getByRole("button", { name: "Recordings of Door" });
    expect(recordings).toHaveTextContent("Recordings"); // visible on touch, not a hover tooltip only
    await user.click(recordings);
    let detail = await screen.findByTestId("camera-detail");
    expect(within(detail).queryByTestId("tile")).toBeNull(); // not live
    expect(within(detail).getByRole("button", { name: "Back to live" })).toBeInTheDocument();
    expect(within(detail).getByText("Pick a moment on the timeline to play it.")).toBeInTheDocument();
    await waitFor(() => expect(within(detail).getByRole("slider", { name: "Timeline" })).toHaveFocus());
    expect(router.state.location.search).toEqual({ camera: "cam_1", view: "recordings" });

    await user.click(within(detail).getByRole("button", { name: "Back to live" }));
    expect(await within(detail).findByTestId("tile")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.search).toEqual({ camera: "cam_1" })); // a reload or shared link opens it live
    await user.click(within(detail).getByRole("button", { name: "Close camera" }));
    await waitFor(() => expect(screen.queryByTestId("camera-detail")).toBeNull());

    await user.click(screen.getByRole("button", { name: "List" }));
    const row = (await screen.findAllByTestId("camera")).find((r) => r.dataset.camera === "cam_2")!;
    await user.click(within(row).getByRole("button", { name: "Recordings of Garden" }));
    detail = await screen.findByTestId("camera-detail");
    expect(within(detail).getByRole("heading", { name: "Garden" })).toBeInTheDocument();
    expect(within(detail).queryByTestId("tile")).toBeNull();
  }, 20_000);

  it("offers sound only for a camera that sends it, muted by default, remembered per camera (#95)", async () => {
    stub({ role: "viewer", quality: "full", evicted: false, offerFails: 0, lan: true });
    // Records whether each play() ran muted; a refused one rejects like Chrome and Safari do.
    const plays: boolean[] = [];
    let refuse = false;
    const play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
      plays.push(this.muted);
      if (refuse && !this.muted) { refuse = false; return Promise.reject(new DOMException("blocked", "NotAllowedError")); }
      return Promise.resolve();
    });
    const user = mount();
    FakePC.audio = true;
    const door = (await within(await screen.findByTestId("grid")).findAllByTestId("tile")).find((t) => t.dataset.camera === "cam_1")!;
    const sound = await within(door).findByRole("button", { name: "Sound for Door" });
    const video = door.querySelector("video")!;
    expect(sound).toHaveAttribute("aria-pressed", "false");
    expect(video.muted).toBe(true);
    await user.click(sound);
    expect(sound).toHaveAttribute("aria-pressed", "true");
    expect(video.muted).toBe(false);
    expect(play).toHaveBeenCalled();
    expect(plays.at(-1)).toBe(false); // unmuted and played inside the tap
    expect(localStorage.getItem("camhive.live.sound.hv_1.cam_1")).toBe("on"); // camera ids are unique only within a hive

    // The opened camera remembers it; a browser that blocks sound without a tap keeps it muted and keeps the picture playing.
    refuse = true;
    plays.length = 0;
    await user.click(within(door).getByRole("button", { name: "Open Door" }));
    const big = within(await screen.findByTestId("camera-detail")).getByTestId("tile");
    const again = await within(big).findByRole("button", { name: "Sound for Door" });
    await waitFor(() => expect(again).toHaveAttribute("aria-pressed", "false"));
    expect(big.querySelector("video")!.muted).toBe(true);
    await waitFor(() => expect(plays.slice(-2)).toEqual([false, true])); // refused with sound, then played again muted
    await user.click(again);
    expect(again).toHaveAttribute("aria-pressed", "true");
    expect(big.querySelector("video")!.muted).toBe(false);
    await user.click(again);
    expect(big.querySelector("video")!.muted).toBe(true);
    expect(localStorage.getItem("camhive.live.sound.hv_1.cam_1")).toBeNull();
  });

  it("hides the sound control on a tile that stops streaming", async () => {
    FakePC.audio = true;
    const signal: Signalling = { offer: async () => ({ sdp: "v=0 answer", session: "s" }), candidates: async () => {}, close: async () => {} };
    const camera: HiveCamera = { ...cam("cam_1", "Door"), source: { kind: "rtsp", address: "rtsp://x" }, record_audio: true };
    vi.stubGlobal("RTCPeerConnection", FakePC);
    const props = { hiveId: "hv_1", camera, stream: "main" as const, signal };
    const { rerender } = render(<LiveTile {...props} />);
    expect(await screen.findByRole("button", { name: "Sound for Door" })).toBeInTheDocument();
    rerender(<LiveTile {...props} paused />);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Sound for Door" })).toBeNull());
  });

  it("shows no sound control when the camera's audio track never carries media", async () => {
    stub({ role: "viewer", quality: "full", evicted: false, offerFails: 0, lan: true });
    mount();
    const door = (await within(await screen.findByTestId("grid")).findAllByTestId("tile")).find((t) => t.dataset.camera === "cam_1")!;
    await waitFor(() => expect(door.dataset.state).toBe("connected"));
    expect(within(door).queryByRole("button", { name: "Sound for Door" })).toBeNull();
  });

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
