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
type Cam = { id: string; name: string; group: string; enabled: boolean; source: { kind: string; address: string }; streams: Record<string, { URL: string; Codec?: string; Width?: number; Height?: number }>; created_at: string; updated_at: string; credentials?: { username: string };
  identity?: Record<string, string>; network?: string; retention_days?: number; sensitivity?: string; motion_calmed_since?: string };

function stub(role: "owner" | "viewer", hiveExtra: Record<string, unknown> = {}) {
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
          const { host, replace_identity, sub_stream, retention_days, ...b } = body as { enabled?: boolean; name?: string; group?: string; host?: string; replace_identity?: boolean; sub_stream?: string; retention_days?: number | null };
          // ADR-0017: another device answers at 10.0.0.50.
          if (host === "10.0.0.50" && !replace_identity) return json(409, { code: "camera_mismatch", message: "" });
          if (host) cams[0]!.source.address = `rtsp://${host}/s`;
          if (sub_stream) cams[0]!.streams.sub = { URL: sub_stream };
          if (sub_stream === "") delete cams[0]!.streams.sub;
          if (typeof retention_days === "number" && (!Number.isInteger(retention_days) || retention_days < 0 || retention_days > 3650)) return json(422, { code: "invalid", message: "" });
          if (typeof retention_days === "number") cams[0]!.retention_days = retention_days;
          if (retention_days === null) delete cams[0]!.retention_days; // the hive omits it on the hive-wide value
          Object.assign(cams[0]!, b);
          return json(200, cams[0]);
        }
        case "DELETE /v0/cameras/cam_1": cams.splice(0, 1); return new Response(null, { status: 204 });
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (key) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [{ ...hive, ...hiveExtra }], shared: [] });
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
    // ADR-0015: sound is off until the owner switches it on, with the legal note beside it.
    const sound = within(edit).getByRole("checkbox", { name: /^Record sound/ });
    expect(sound).not.toBeChecked();
    expect(within(edit).getByText(/§ 201 StGB/)).toBeInTheDocument();
    await user.click(sound);
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]![0]).toEqual({ name: "Front door", group: "Outside", record_audio: true }));
    await user.click(within(door).getByRole("checkbox", { name: /Enabled: / }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]!.at(-1)).toEqual({ enabled: false }));
    vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await user.click(within(door).getByRole("button", { name: "Remove" }));
    expect(cams).toHaveLength(3);
    await user.click(within(door).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(cams).toHaveLength(2));
  }, 20_000);

  it("moves a camera to another address, asking before it takes a different device, and explains what it knows of the camera", async () => {
    const { cams, bodies } = stub("owner");
    cams[0]!.identity = { mac: "aa:bb:cc:00:11:22", serial: "S1", manufacturer: "Acme", model: "X1" };
    cams[0]!.network = "local";
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const door = await screen.findByTestId("camera");
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    const edit = within(door).getByRole("form", { name: "Edit Door" });
    expect(within(edit).getByTestId("camera-identity")).toHaveTextContent("Acme X1 · serial S1 · MAC aa:bb:cc:00:11:22");
    expect(within(edit).getByTestId("camera-network")).toHaveTextContent("finds it again");
    const address = within(edit).getByLabelText("Address (IP or host name)");
    expect(address).toHaveValue("10.0.0.9");
    await user.clear(address);
    await user.type(address, "10.0.0.50");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    expect(await within(door).findByRole("alert")).toHaveTextContent("Another device answers at that address");
    await user.click(within(door).getByRole("button", { name: "It is a new camera, use it" }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]!.at(-1)).toEqual({ name: "Door", group: "Outside", record_audio: false, host: "10.0.0.50", replace_identity: true }));
    await waitFor(() => expect(cams[0]!.source.address).toBe("rtsp://10.0.0.50/s"));
  });

  it("takes an optional sub stream URL for RTSP cameras when adding and editing (#63)", async () => {
    const { cams, bodies } = stub("owner", { version: "0.1.8", capabilities: ["camera.sub_stream"] });
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    await screen.findByTestId("camera");

    await user.click(screen.getByRole("button", { name: "Add camera" }));
    await user.click(screen.getByRole("tab", { name: "RTSP address" }));
    const form = screen.getByRole("form", { name: "RTSP address" });
    await user.type(within(form).getByLabelText("Name"), "Cellar");
    await user.type(within(form).getByLabelText("RTSP URL"), "rtsp://10.0.0.7/ch0");
    for (const bad of ["http://10.0.0.7/ch1", "rtsp://admin:pw@10.0.0.7/ch1", "10.0.0.7/ch1"]) {
      await user.clear(within(form).getByLabelText("Sub stream URL (optional)"));
      await user.type(within(form).getByLabelText("Sub stream URL (optional)"), bad);
      await user.click(within(form).getByRole("button", { name: "Add camera" }));
      expect(await within(form).findByRole("alert")).toHaveTextContent("rtsp:// or rtsps://");
      expect(within(form).getByLabelText("Sub stream URL (optional)")).toHaveAccessibleDescription(/second, smaller stream.*rtsp:\/\/ or rtsps:\/\//);
    }
    expect(bodies["POST /v0/cameras"]).toBeUndefined();
    await user.clear(within(form).getByLabelText("Sub stream URL (optional)"));
    await user.type(within(form).getByLabelText("Sub stream URL (optional)"), "rtsps://10.0.0.7/ch1");
    await user.click(within(form).getByRole("button", { name: "Add camera" }));
    await waitFor(() => expect(cams).toHaveLength(2));
    expect(bodies["POST /v0/cameras"]![0]).toMatchObject({ source: { kind: "rtsp", address: "rtsp://10.0.0.7/ch0" }, streams: { sub: { url: "rtsps://10.0.0.7/ch1" } } });

    // Without a sub stream the create body names none.
    await user.click(screen.getByRole("button", { name: "Add camera" }));
    await user.click(screen.getByRole("tab", { name: "RTSP address" }));
    const again = screen.getByRole("form", { name: "RTSP address" });
    await user.type(within(again).getByLabelText("Name"), "Attic");
    await user.type(within(again).getByLabelText("RTSP URL"), "rtsp://10.0.0.8/ch0");
    await user.click(within(again).getByRole("button", { name: "Add camera" }));
    await waitFor(() => expect(cams).toHaveLength(3));
    expect(bodies["POST /v0/cameras"]![1]).not.toHaveProperty("streams");

    // Edit: set a sub stream, then remove it again.
    const door = screen.getAllByTestId("camera").find((r) => r.dataset.camera === "cam_1")!;
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    let edit = within(door).getByRole("form", { name: "Edit Door" });
    const sub = within(edit).getByLabelText("Sub stream URL (optional)");
    expect(sub).toHaveValue("");
    await user.type(sub, "ftp://10.0.0.9/ch1");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    expect(await within(edit).findByRole("alert")).toHaveTextContent("rtsp:// or rtsps://");
    expect(bodies["PATCH /v0/cameras/cam_1"]).toBeUndefined();
    await user.clear(sub);
    await user.type(sub, "rtsp://10.0.0.9/ch1");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]![0]).toEqual({ name: "Door", group: "Outside", record_audio: false, sub_stream: "rtsp://10.0.0.9/ch1" }));
    await waitFor(() => expect(cams[0]!.streams.sub).toEqual({ URL: "rtsp://10.0.0.9/ch1" }));

    await user.click(within(door).getByRole("button", { name: "Edit" }));
    edit = within(door).getByRole("form", { name: "Edit Door" });
    expect(within(edit).getByLabelText("Sub stream URL (optional)")).toHaveValue("rtsp://10.0.0.9/ch1");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]![1]).toEqual({ name: "Door", group: "Outside", record_audio: false })); // unchanged: not sent
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    edit = within(door).getByRole("form", { name: "Edit Door" });
    await user.clear(within(edit).getByLabelText("Sub stream URL (optional)"));
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(bodies["PATCH /v0/cameras/cam_1"]![2]).toEqual({ name: "Door", group: "Outside", record_audio: false, sub_stream: "" }));
  }, 20_000);

  it("sets a camera's own retention: hive default, days, zero, and back to the default (#124)", async () => {
    const { cams, bodies } = stub("owner", { version: "0.1.9", capabilities: ["camera.retention"] });
    const patches = () => bodies["PATCH /v0/cameras/cam_1"] ?? [];
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const door = await screen.findByTestId("camera");
    const open = async () => {
      await user.click(within(door).getByRole("button", { name: "Edit" }));
      return within(door).getByRole("form", { name: "Edit Door" });
    };
    const save = async (edit: HTMLElement, n: number) => {
      await user.click(within(edit).getByRole("button", { name: "Save" }));
      await waitFor(() => expect(patches()).toHaveLength(n));
      return patches()[n - 1];
    };

    // Default: the hive-wide value, nothing sent.
    let edit = await open();
    expect(within(edit).getByLabelText("Keep recordings")).toHaveDisplayValue("Hive default");
    expect(within(edit).queryByLabelText("Days")).toBeNull();
    expect(await save(edit, 1)).not.toHaveProperty("retention_days");

    // Days.
    edit = await open();
    await user.selectOptions(within(edit).getByLabelText("Keep recordings"), "Number of days");
    await user.type(within(edit).getByLabelText("Days"), "90");
    expect(await save(edit, 2)).toMatchObject({ retention_days: 90 });
    await waitFor(() => expect(cams[0]!.retention_days).toBe(90));

    // Unchanged days are not sent; out of range is refused before the hive.
    edit = await open();
    expect(within(edit).getByLabelText("Keep recordings")).toHaveDisplayValue("Number of days");
    expect(within(edit).getByLabelText("Days")).toHaveValue(90);
    expect(await save(edit, 3)).not.toHaveProperty("retention_days");
    edit = await open();
    const daysInput = within(edit).getByLabelText("Days");
    expect(daysInput).toHaveAttribute("min", "0");
    expect(daysInput).toHaveAttribute("max", "3650");
    expect(daysInput).toHaveAttribute("step", "1");
    // Flipping to the default and back keeps the days typed so far.
    await user.clear(daysInput);
    await user.type(daysInput, "45");
    await user.selectOptions(within(edit).getByLabelText("Keep recordings"), "Hive default");
    await user.selectOptions(within(edit).getByLabelText("Keep recordings"), "Number of days");
    expect(within(edit).getByLabelText("Days")).toHaveValue(45);
    await user.clear(within(edit).getByLabelText("Days"));
    await user.type(within(edit).getByLabelText("Days"), "3651");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    expect((within(edit).getByLabelText("Days") as HTMLInputElement).validity.rangeOverflow).toBe(true); // the browser stops it
    expect(patches()).toHaveLength(3);
    await user.clear(within(edit).getByLabelText("Days"));
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    expect(await within(edit).findByRole("alert")).toHaveTextContent("0 to 3650"); // no days at all: the app says so
    expect(patches()).toHaveLength(3);

    // Zero: until space runs out.
    await user.clear(within(edit).getByLabelText("Days"));
    await user.type(within(edit).getByLabelText("Days"), "0");
    expect(within(edit).getByLabelText("Days")).toHaveAccessibleDescription(/0 keeps them until the disk needs the space/);
    expect(await save(edit, 4)).toMatchObject({ retention_days: 0 });
    await waitFor(() => expect(cams[0]!.retention_days).toBe(0));

    // Reset to the hive default sends null.
    edit = await open();
    expect(within(edit).getByLabelText("Days")).toHaveValue(0);
    await user.selectOptions(within(edit).getByLabelText("Keep recordings"), "Hive default");
    expect(await save(edit, 5)).toMatchObject({ retention_days: null });
    await waitFor(() => expect(cams[0]!).not.toHaveProperty("retention_days"));
  }, 20_000);

  it("offers no retention field on a hive that does not announce it", async () => {
    stub("owner", { version: "0.1.8", capabilities: ["camera.sub_stream"] });
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const door = await screen.findByTestId("camera");
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    expect(within(door).getByRole("form", { name: "Edit Door" })).toBeInTheDocument();
    expect(within(door).queryByLabelText("Keep recordings")).toBeNull();
  });

  it("offers no sub stream edit on a hive that cannot take it, nor for ONVIF cameras", async () => {
    const { cams } = stub("owner", { version: "0.1.7", capabilities: ["archive.delete", "camera.identity"] }); // v0.1.7 shipped without it
    cams.push({ id: "cam_2", name: "Garden", group: "", enabled: true, source: { kind: "onvif", address: "http://10.0.0.5/onvif/device_service" }, streams: { main: { URL: "rtsp://10.0.0.5/main" } }, created_at: "", updated_at: "" });
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const [door, garden] = await screen.findAllByTestId("camera");
    await user.click(within(door!).getByRole("button", { name: "Edit" }));
    expect(within(door!).queryByLabelText("Sub stream URL (optional)")).toBeNull();
    await user.click(within(garden!).getByRole("button", { name: "Edit" }));
    expect(within(garden!).queryByLabelText("Sub stream URL (optional)")).toBeNull();
  });

  it("tells the owner to reserve the address of a camera behind a router", async () => {
    const { cams } = stub("owner");
    cams[0]!.network = "routed";
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const door = await screen.findByTestId("camera");
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    expect(within(door).getByTestId("camera-network")).toHaveTextContent("Reserve its address in the router");
  });

  it("says the cameras are loading instead of looking like an empty hive", async () => {
    stub("owner");
    const real = globalThis.fetch;
    let release: () => void = () => {};
    const held = new Promise<void>((r) => { release = r; });
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const href = input instanceof Request ? input.url : String(input); // not new Request(): it would use up the body
      if (new URL(href).pathname === "/v0/cameras") await held; // the hive answers slowly (relay, weak network)
      return real(input, init);
    }) as typeof fetch);
    const user = mount();
    expect(await screen.findByTestId("cameras-loading")).toHaveTextContent("Loading cameras");
    expect(screen.queryByRole("button", { name: "Add camera" })).toBeNull();
    release();
    await user.click(await screen.findByRole("button", { name: "List" }));
    expect(await screen.findByTestId("camera")).toHaveTextContent("Door");
    expect(screen.queryByTestId("cameras-loading")).toBeNull();
    expect(screen.getByRole("button", { name: "Add camera" })).toBeTruthy();
  });

  it("hides management from members", async () => {
    stub("viewer");
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const row = await screen.findByTestId("camera");
    expect(screen.queryByRole("button", { name: "Add camera" })).toBeNull();
    expect(within(row).queryByRole("button", { name: "Remove" })).toBeNull();
  });

  it("sets a camera's motion sensitivity and says since when the hive calmed it (#157)", async () => {
    const { cams, bodies } = stub("owner", { version: "0.1.10", capabilities: ["camera.sensitivity"] });
    cams[0]!.sensitivity = "normal";
    cams[0]!.motion_calmed_since = "2026-11-22T13:10:00Z";
    const patches = () => bodies["PATCH /v0/cameras/cam_1"] ?? [];
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const door = await screen.findByTestId("camera");
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    let edit = within(door).getByRole("form", { name: "Edit Door" });
    const local = new Date("2026-11-22T13:10:00Z").toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit" });
    expect(within(edit).getByText(`Calmed since ${local}`, { exact: false })).toBeInTheDocument();
    expect(within(edit).getByLabelText("Motion sensitivity")).toHaveDisplayValue("Normal");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]).not.toHaveProperty("sensitivity"); // unchanged: not sent

    await user.click(within(door).getByRole("button", { name: "Edit" }));
    edit = within(door).getByRole("form", { name: "Edit Door" });
    await user.selectOptions(within(edit).getByLabelText("Motion sensitivity"), "Low");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(2));
    expect(patches()[1]).toMatchObject({ sensitivity: "low" });
  }, 20_000);

  it("offers no sensitivity on a hive without camera.sensitivity", async () => {
    stub("owner", { version: "0.1.9", capabilities: ["camera.retention"] });
    const user = mount();
    await user.click(await screen.findByRole("button", { name: "List" }));
    const door = await screen.findByTestId("camera");
    await user.click(within(door).getByRole("button", { name: "Edit" }));
    expect(within(door).queryByLabelText("Motion sensitivity")).toBeNull();
  });
});
