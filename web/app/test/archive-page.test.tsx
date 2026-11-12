import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetCloudForTest } from "../src/lib/cloud";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { dayStart } from "../src/lib/archive";

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app" };
const day = dayStart("2026-11-09");
const at = (h: number, m = 0) => new Date(day.getTime() + (h * 60 + m) * 60_000).toISOString();
/** A day button's accessible name: the full date. */
const dayName = (key: string) => dayStart(key).toLocaleDateString("en", { weekday: "long", day: "numeric", month: "long", year: "numeric" });

function stub(opts: { exportMax: number }) {
  const calls: string[] = [];
  const jobs: unknown[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    calls.push(`${req.method} ${u.pathname}${u.search}`);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "GET /v0/cameras": return json(200, [{ id: "cam_1", name: "Door", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: { main: { Codec: "H264" } }, created_at: "", updated_at: "" }]);
        case "GET /v0/cameras/cam_1/archive/calendar": return json(200, [{ day: "2026-11-08" }, { day: "2026-11-09" }]);
        case "GET /v0/cameras/cam_1/archive/timeline": return json(200, [{ start: at(8), duration_ms: 3_600_000, bytes: 1, complete: true, activity: { step_s: 10, video_kbps: [100, 900, 300], sound_db: [-90, -20, -40] } }, { start: at(20), duration_ms: 600_000, bytes: 1, complete: false }]);
        case "GET /v0/cameras/cam_1/events": return json(200, [{ id: 1, kind: "motion", at: at(8, 15), camera: "cam_1", data: {} }]);
        case "GET /v0/cameras/cam_1/archive/audio": return json(404, { code: "no_audio", message: "" });
        case "GET /v0/cameras/cam_1/archive/play": return new Response(new Uint8Array([0, 0, 0, 1]), { status: 200, headers: { "Content-Type": "video/mp4" } });
        case "POST /v0/cameras/cam_1/exports": {
          const b = (await req.json()) as { duration_seconds: number; start: string };
          if (b.duration_seconds > opts.exportMax * 60) return json(403, { code: "plan_limit", message: "" });
          const job = { id: "exp_1", camera: "cam_1", stream: "main", start: b.start, duration_seconds: b.duration_seconds, status: "done", bytes: 10, file_name: "door.mp4", created_at: "" };
          jobs.push(job);
          return json(202, job);
        }
        case "GET /v0/exports": return json(200, jobs);
        case "GET /v0/exports/exp_1/file": return new Response(new Uint8Array([1]), { status: 200, headers: { "Content-Type": "video/mp4" } });
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "owner", cameras: ["*"], hive, quality: "full", export_max_minutes: opts.exportMax, ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount(path: string) {
  transport.invalidateAll();
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:x", revokeObjectURL: () => undefined }));
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return { user: userEvent.setup(), router };
}

/** Unmounts, lets requests still in flight reach the stubbed fetch, then removes the stubs (a late request hitting the real fetch would mark the cloud down for the next test). */
async function settle() {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
}

afterEach(settle);

describe("archive on the hive page", () => {
  it("shows days and footage, seeks into a recording and plays from there, refuses empty spots, exports within the cap and shows plan_limit over it", async () => {
    const calls = stub({ exportMax: 10 });
    const { user } = mount("/hives/hv_1/archive?day=2026-11-09");
    // The old archive link redirects to the hive page with that camera opened on that day.
    expect(await screen.findByRole("button", { name: dayName("2026-11-09") }, { timeout: 5000 })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: dayName("2026-11-08") })).toHaveAttribute("aria-pressed", "false");
    // The strip runs on to today; a day without footage cannot be picked.
    expect(screen.getByRole("button", { name: dayName("2026-11-10") })).toBeDisabled();
    const timeline = await screen.findByTestId("timeline");
    await waitFor(() => expect(within(timeline).getAllByTestId("segment")).toHaveLength(2));
    expect(within(timeline).getAllByTestId("event")).toHaveLength(1);
    // ADR-0015: the profiled segment draws an activity wave and a sound line; the other has none.
    expect(within(timeline).getAllByTestId("activity-video")).toHaveLength(1);
    expect(within(timeline).getAllByTestId("activity-sound")).toHaveLength(1);
    expect(screen.getByTestId("activity-legend")).toHaveTextContent("activitysound level");

    // Clicking at 08:30 (fraction 8.5/24) plays from that minute; the window stops at the segment end.
    vi.spyOn(timeline, "getBoundingClientRect").mockReturnValue({ left: 0, width: 240, top: 0, height: 32, right: 240, bottom: 32, x: 0, y: 0, toJSON: () => ({}) });
    fireEvent.click(timeline, { clientX: 85 });
    await waitFor(() => expect(calls.find((c) => c.includes("/archive/play"))).toBeDefined());
    const play = new URL("https://x" + calls.find((c) => c.includes("/archive/play"))!.split(" ")[1]!);
    expect(play.searchParams.get("start")).toBe(at(8, 30));
    expect(play.searchParams.get("duration")).toBe("600");
    expect(screen.getByTestId("position")).toHaveTextContent(new Date(at(8, 30)).toLocaleString());
    expect(screen.getByTestId("readout")).toHaveTextContent("08:30:00");

    // The ruler labels the day every two hours and the playhead shows its time;
    // zooming to 1 h re-centres the window on the playhead and the ruler to 10-minute steps.
    expect(within(timeline).getByText("06:00")).toBeInTheDocument();
    expect(within(timeline).getByTestId("playhead")).toHaveTextContent("08:30");
    await user.click(screen.getByRole("button", { name: "1h" }));
    expect(screen.getByTestId("window")).toHaveTextContent("08:00 – 09:00");
    expect(within(timeline).getByText("08:10")).toBeInTheDocument();
    expect(within(timeline).getAllByTestId("segment")).toHaveLength(1); // the 20:00 segment is out of view
    await user.click(screen.getByRole("button", { name: "24h" }));
    expect(screen.getByTestId("window")).toHaveTextContent("00:00 – 24:00");

    // 12:00 has no footage and nothing later until 20:00 → jumps there.
    fireEvent.click(timeline, { clientX: 120 });
    await waitFor(() => expect(screen.getByTestId("position")).toHaveTextContent(new Date(at(20)).toLocaleString()));
    // ADR-0015: sound is fetched only on request; a camera without it says so and the button goes back off.
    expect(calls.some((c) => c.includes("/archive/audio"))).toBe(false);
    await user.click(screen.getByRole("button", { name: "Sound off" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("No sound was recorded here");
    expect(calls.find((c) => c.startsWith("GET /v0/cameras/cam_1/archive/audio?"))).toContain("duration=60");
    expect(screen.getByRole("button", { name: "Sound off" })).toHaveAttribute("aria-pressed", "false");
    // 23:00: nothing recorded afterwards.
    fireEvent.click(timeline, { clientX: 230 });
    expect(await screen.findByRole("alert")).toHaveTextContent("Nothing recorded at this time");

    // Export: cap from the grant; over it the hive's plan_limit is explained.
    fireEvent.click(timeline, { clientX: 85 });
    await user.click(screen.getByRole("button", { name: "Export from here" }));
    const minutes = screen.getByLabelText("Length (minutes)");
    expect(screen.getByText("Your plan allows up to 10 minutes per export.")).toBeInTheDocument();
    await user.clear(minutes);
    await user.type(minutes, "30");
    minutes.removeAttribute("max"); // browser validation would block the submit; the API answer is what we test
    await user.click(screen.getByRole("button", { name: "Start export" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Over the plan limit: exports may be up to 10 minutes.");
    await user.clear(minutes);
    await user.type(minutes, "5");
    await user.click(screen.getByRole("button", { name: "Start export" }));
    const job = await screen.findByTestId("export");
    expect(job).toHaveAttribute("data-status", "done");
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    await user.click(within(job).getByRole("button", { name: "Download" }));
    await waitFor(() => expect(calls).toContain("GET /v0/exports/exp_1/file"));
    expect(click).toHaveBeenCalled();

    // An old archive link opened the camera on the hive page; going back to live leaves the recording.
    expect(screen.getByTestId("camera-detail")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Back to live" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Back to live" })).toBeNull());
    expect(within(screen.getByTestId("camera-detail")).getByTestId("tile").dataset.camera).toBe("cam_1");
  }, 20_000);

  it("drags and wheels the timeline, follows playback, plays on into the next window, changes speed and saves a snapshot", async () => {
    const calls = stub({ exportMax: 10 });
    const { user } = mount("/hives/hv_1/archive?day=2026-11-09");
    const timeline = await screen.findByTestId("timeline", {}, { timeout: 5000 });
    await waitFor(() => expect(within(timeline).getAllByTestId("segment")).toHaveLength(2));
    vi.spyOn(timeline, "getBoundingClientRect").mockReturnValue({ left: 0, width: 240, top: 0, height: 32, right: 240, bottom: 32, x: 0, y: 0, toJSON: () => ({}) });
    const plays = () => calls.filter((c) => c.includes("/archive/play")).map((c) => new URL("https://x" + c.split(" ")[1]!).searchParams.get("start"));
    fireEvent.click(timeline, { clientX: 85 });
    await waitFor(() => expect(plays()).toEqual([at(8, 30)]));

    // Zoomed to 1 h, dragging a quarter of the bar to the left moves 15 minutes on; the playhead stays centred.
    await user.click(screen.getByRole("button", { name: "1h" }));
    fireEvent.pointerDown(timeline, { pointerId: 1, clientX: 120, button: 0, pointerType: "mouse" });
    fireEvent.pointerMove(timeline, { pointerId: 1, clientX: 60, pointerType: "mouse" });
    expect(screen.getByTestId("readout")).toHaveTextContent("08:45:00");
    fireEvent.pointerUp(timeline, { pointerId: 1, clientX: 60, pointerType: "mouse" });
    await waitFor(() => expect(plays()).toEqual([at(8, 30), at(8, 45)]));
    expect(screen.getByTestId("window")).toHaveTextContent("08:15 – 09:15");

    // The wheel zooms: far out is the whole day, in again leaves it.
    fireEvent.wheel(timeline, { deltaY: 5000, clientX: 120 });
    expect(screen.getByTestId("window")).toHaveTextContent("00:00 – 24:00");
    expect(screen.getByRole("button", { name: "24h" })).toHaveAttribute("aria-pressed", "true");
    fireEvent.wheel(timeline, { deltaY: -1000, clientX: 85 });
    expect(screen.getByTestId("window")).not.toHaveTextContent("00:00 – 24:00");

    // Playback moves the playhead and the readout.
    const video = screen.getByTestId("player") as HTMLVideoElement;
    Object.defineProperty(video, "currentTime", { configurable: true, value: 65 });
    fireEvent(video, new Event("timeupdate"));
    expect(screen.getByTestId("readout")).toHaveTextContent("08:46:05");

    // Speed applies to this and later windows.
    await user.selectOptions(screen.getByLabelText("Speed"), "4");
    expect(video.playbackRate).toBe(4);
    expect(video.defaultPlaybackRate).toBe(4);

    // A snapshot is the frame shown, named after the camera and that moment.
    Object.defineProperty(video, "videoWidth", { configurable: true, value: 640 });
    Object.defineProperty(video, "videoHeight", { configurable: true, value: 360 });
    const drawImage = vi.fn();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((cb) => cb(new Blob(["png"])));
    const saved: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) { saved.push(this.download); });
    await user.click(screen.getByRole("button", { name: "Snapshot" }));
    expect(drawImage).toHaveBeenCalledWith(video, 0, 0, 640, 360);
    expect(saved).toEqual(["Door_2026-11-09_08-46-05.png"]);

    // At the end of a window the next one plays on its own (08:45 + 10 min).
    fireEvent(video, new Event("ended"));
    await waitFor(() => expect(plays()).toEqual([at(8, 30), at(8, 45), at(8, 55)]));

    // ADR-0016: the owner can delete the day shown, starting from this camera and day.
    await user.click(screen.getByRole("button", { name: "Delete this day…" }));
    const form = screen.getByRole("region", { name: "Delete recordings" });
    expect(within(form).getByRole("checkbox", { name: "Door" })).toBeChecked();
    expect(within(form).getByLabelText("From")).toHaveValue("2026-11-09T00:00");
    expect(within(form).getByLabelText("To")).toHaveValue("2026-11-10T00:00");
  }, 20_000);
});
