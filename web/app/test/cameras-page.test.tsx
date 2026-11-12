import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetCloudForTest } from "../src/lib/cloud";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app" };
type Cam = { id: string; name: string; group: string; enabled: boolean; source: { kind: string; address: string }; streams: Record<string, { URL: string; Codec?: string; Width?: number; Height?: number }>; created_at: string; updated_at: string; credentials?: { username: string } };

function stub(role: "owner" | "viewer") {
  const cams: Cam[] = [{ id: "cam_1", name: "Door", group: "Outside", enabled: true, source: { kind: "rtsp", address: "rtsp://10.0.0.9/s" }, streams: { main: { URL: "rtsp://10.0.0.9/s", Codec: "H264", Width: 1920, Height: 1080 } }, created_at: "", updated_at: "", credentials: { username: "admin" } }];
  const bodies: Record<string, unknown[]> = {};
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const key = `${req.method} ${u.pathname}`;
    if (u.host === "hv_1.h.camhive.app:16017") {
      const body = req.method === "GET" || req.method === "DELETE" ? undefined : await req.json();
      (bodies[key] ??= []).push(body);
      switch (key) {
        case "GET /healthz": return json(200, {});
        case "GET /v0/cameras": return json(200, cams);
        case "GET /v0/health/cameras": return json(200, cams.map((c) => ({ camera: c.id, enabled: c.enabled, online: c.id === "cam_1", streams: {} })));
        case "GET /v0/discovery": return json(200, [{ xaddr: "http://10.0.0.5/onvif/device_service", name: "Garden", scopes: [] }]);
        case "POST /v0/discovery/probe": {
          const b = body as { credentials?: { password: string } };
          if (b.credentials?.password !== "pw") return json(502, { code: "camera_auth", message: "" });
          return json(200, { device: { Manufacturer: "Camsim", Model: "Sim-1", FirmwareVersion: "1.0" }, profiles: [
            { token: "p-sub", name: "sub", encoding: "H264", width: 640, height: 360, fps: 15, url: "rtsp://10.0.0.5/sub" },
            { token: "p-main", name: "main", encoding: "H265", width: 2560, height: 1440, fps: 25, url: "rtsp://10.0.0.5/main" }] });
        }
        case "POST /v0/cameras": {
          const b = body as { name: string; group: string; source: { kind: string; address: string }; streams?: Record<string, { url: string; codec: string; width: number; height: number }> };
          if (!b.name) return json(422, { code: "invalid", message: "name is required" });
          const streams: Cam["streams"] = {};
          for (const [k, v] of Object.entries(b.streams ?? {})) streams[k] = { URL: v.url, Codec: v.codec, Width: v.width, Height: v.height };
          if (b.source.kind === "rtsp") streams.main = { URL: b.source.address };
          const c: Cam = { id: `cam_${cams.length + 1}`, name: b.name, group: b.group, enabled: true, source: b.source, streams, created_at: "", updated_at: "" };
          cams.push(c);
          return json(201, c);
        }
        case "PATCH /v0/cameras/cam_1": {
          const b = body as { enabled?: boolean; name?: string; group?: string };
          Object.assign(cams[0]!, b);
          return json(200, cams[0]);
        }
        case "DELETE /v0/cameras/cam_1": cams.splice(0, 1); return new Response(null, { status: 204 });
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (key) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role, cameras: ["*"], hive, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return { cams, bodies };
}

function mount() {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/cameras"] }));
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

describe("camera management on the hive page", () => {
  it("lists cameras with health, adds one by discovery with probed main/sub streams, adds one by RTSP, edits, disables and removes", async () => {
    const { cams, bodies } = stub("owner");
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const row = await screen.findByTestId("camera");
    expect(row).toHaveTextContent("Door");
    expect(row).toHaveTextContent("Outside");
    await waitFor(() => expect(within(row).getByTestId("camera-status")).toHaveTextContent("Online"));

    // Discovery → probe (wrong password first) → pick streams → create.
    await user.click(screen.getByRole("button", { name: "Add camera" }));
    await user.click(screen.getByRole("button", { name: "Scan network" }));
    await user.click(await screen.findByRole("button", { name: "Garden" }));
    expect(screen.getByLabelText("ONVIF address")).toHaveValue("http://10.0.0.5/onvif/device_service");
    await user.type(screen.getByLabelText("Camera user"), "admin");
    await user.type(screen.getByLabelText("Camera password"), "nope");
    await user.click(screen.getByRole("button", { name: "Read profiles" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The camera rejected the user or password.");
    await user.clear(screen.getByLabelText("Camera password"));
    await user.type(screen.getByLabelText("Camera password"), "pw");
    await user.click(screen.getByRole("button", { name: "Read profiles" }));
    const probed = await screen.findByTestId("probe-result");
    expect(probed).toHaveTextContent("Camsim · Sim-1 · 1.0");
    expect(within(probed).getByLabelText("Name")).toHaveValue("Garden");
    expect(within(probed).getByLabelText(/Main stream/)).toHaveValue("p-main"); // largest picked by default
    expect(within(probed).getByLabelText(/Sub stream/)).toHaveValue("p-sub");
    await user.type(within(probed).getByLabelText("Group"), "Outside");
    await user.click(within(probed).getByRole("button", { name: "Add camera" }));
    await waitFor(() => expect(cams).toHaveLength(2));
    const created = bodies["POST /v0/cameras"]![0] as { source: { kind: string }; credentials: { username: string; password: string }; streams: Record<string, { url: string; codec: string }> };
    expect(created.source.kind).toBe("onvif");
    expect(created.credentials).toEqual({ username: "admin", password: "pw" });
    expect(created.streams.main).toMatchObject({ url: "rtsp://10.0.0.5/main", codec: "H265" });
    expect(created.streams.sub).toMatchObject({ url: "rtsp://10.0.0.5/sub" });
    expect(await screen.findAllByTestId("camera")).toHaveLength(2);

    // Manual RTSP; the hive's validation message is shown when the name is missing.
    await user.click(screen.getByRole("button", { name: "Add camera" }));
    await user.click(screen.getByRole("tab", { name: "RTSP address" }));
    const form = screen.getByRole("form", { name: "RTSP address" });
    await user.type(within(form).getByLabelText("RTSP URL"), "rtsp://10.0.0.7/live");
    within(form).getByLabelText("Name").removeAttribute("required");
    await user.click(within(form).getByRole("button", { name: "Add camera" }));
    expect(await within(form).findByRole("alert")).toHaveTextContent("The hive refused the camera: name is required");
    await user.type(within(form).getByLabelText("Name"), "Cellar");
    await user.click(within(form).getByRole("button", { name: "Add camera" }));
    await waitFor(() => expect(cams).toHaveLength(3));
    expect(await screen.findAllByTestId("camera")).toHaveLength(3);

    // Edit name and group; disable; remove with confirmation.
    const door = screen.getAllByTestId("camera").find((r) => r.dataset.camera === "cam_1")!;
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    const edit = within(door).getByRole("form", { name: "Edit Door" });
    expect(within(edit).getByLabelText("Camera user")).toHaveValue("admin");
    await user.clear(within(edit).getByLabelText("Name"));
    await user.type(within(edit).getByLabelText("Name"), "Front door");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]![0]).toEqual({ name: "Front door", group: "Outside" }));
    await user.click(within(door).getByRole("checkbox", { name: /Enabled: / }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]!.at(-1)).toEqual({ enabled: false }));
    vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await user.click(within(door).getByRole("button", { name: "Remove" }));
    expect(cams).toHaveLength(3);
    await user.click(within(door).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(cams).toHaveLength(2));
  }, 20_000);

  it("hides management from members", async () => {
    stub("viewer");
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const row = await screen.findByTestId("camera");
    expect(screen.queryByRole("button", { name: "Add camera" })).toBeNull();
    expect(within(row).queryByRole("button", { name: "Remove" })).toBeNull();
  });
});
