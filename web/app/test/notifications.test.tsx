import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { CameraAlerts } from "../src/components/CameraAlerts";
import { cameraPrefs, withCamera, type NotifySettings } from "../src/lib/notify";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const defaults: NotifySettings = {
  events: { offline: { email: true, push: true }, storage: { email: true, push: true }, motion: { email: false, push: true } },
  time_zone: "UTC",
  quiet_hours: null,
  cameras: [],
  channels: ["email"],
};

/** A cloud that keeps one settings document and records every PUT body. */
function stubCloud(start: NotifySettings = defaults) {
  let doc = structuredClone(start);
  const puts: NotifySettings[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const key = `${req.method} ${new URL(req.url).pathname}`;
    switch (key) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" });
      case "GET /v1/me/notifications": return json(200, doc);
      case "PUT /v1/me/notifications": {
        const body = (await req.json()) as NotifySettings;
        puts.push(body);
        doc = { ...body, channels: start.channels };
        return json(200, doc);
      }
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return puts;
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("notification settings", () => {
  it("shows the plan's channels, saves switches and quiet hours", async () => {
    const puts = stubCloud();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/notifications"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();

    const motionMail = await screen.findByRole("checkbox", { name: "Motion: e-mail" });
    expect(motionMail).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Camera or hive offline: e-mail" })).toBeChecked();
    expect(screen.queryByRole("checkbox", { name: "Motion: push" })).toBeNull(); // push is not a channel here yet

    await user.click(motionMail);
    await user.click(screen.getByRole("checkbox", { name: "Quiet hours" }));
    await user.clear(screen.getByLabelText("From"));
    await user.type(screen.getByLabelText("From"), "22:00");
    await user.clear(screen.getByLabelText("Until"));
    await user.type(screen.getByLabelText("Until"), "07:00");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Saved");
    expect(puts).toHaveLength(1);
    const [put] = puts as [NotifySettings];
    expect(put.events.motion).toEqual({ email: true, push: true });
    expect(put.quiet_hours).toEqual({ start: "22:00", end: "07:00" });
    expect(put.time_zone).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone); // every save carries the browser's zone
    expect(put.cameras).toEqual([]);
    expect("channels" in put).toBe(false);
  });

  it("chooses when each group notifies, shows the week's counts and keeps them out of the save (#157)", async () => {
    const puts = stubCloud({ ...defaults, events: { ...defaults.events, motion: { email: false, push: true, mode: "immediate" } }, counts: { episodes_week: 5600, pushes_week: 312 } });
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/notifications"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();

    expect(await screen.findByText("This week: 5,600 motion episodes, 312 push notifications.")).toBeInTheDocument();
    const motion = screen.getByRole("combobox", { name: "Motion: when" });
    expect(motion).toHaveDisplayValue("Each time");
    expect(screen.queryByRole("combobox", { name: "Storage low: when" })).toBeNull(); // offline and storage always come at once
    expect(screen.getByText(/stays silent until it has been quiet/)).toBeInTheDocument();
    await user.selectOptions(motion, "Hourly summary");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(puts).toHaveLength(1));
    const [put] = puts as [NotifySettings];
    expect(put.events.motion).toEqual({ email: false, push: true, mode: "hourly" });
    expect(put.events.storage).toEqual({ email: true, push: true });
    expect("counts" in put).toBe(false);
  });

  it("switches motion notifications for one camera and keeps the rest", async () => {
    const other = { hive_id: "hv_1", camera_id: "cam_2", motion: false, cooldown_minutes: 30 };
    const puts = stubCloud({ ...defaults, cameras: [other] });
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <CameraAlerts hiveId="hv_1" cameraId="cam_1" />
      </QueryClientProvider>,
    );
    const user = userEvent.setup();
    const toggle = await screen.findByRole("checkbox", { name: "Motion notifications for this camera" });
    expect(toggle).toBeChecked();
    await user.selectOptions(screen.getByLabelText("After a push, silent until quiet for"), "30");
    await waitFor(() => expect(puts).toHaveLength(1));
    await user.click(toggle);
    await waitFor(() => expect(puts).toHaveLength(2));
    expect(puts[1]?.time_zone).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);
    expect(puts[1]?.cameras).toEqual([other, { hive_id: "hv_1", camera_id: "cam_1", motion: false, cooldown_minutes: 30 }]);
  });
});

describe("camera preferences", () => {
  it("defaults to motion on every 10 minutes and replaces in place", () => {
    expect(cameraPrefs(defaults, "hv_1", "cam_1")).toEqual({ hive_id: "hv_1", camera_id: "cam_1", motion: true, cooldown_minutes: 10 });
    const once = withCamera(defaults, { hive_id: "hv_1", camera_id: "cam_1", motion: false, cooldown_minutes: 10 });
    const twice = withCamera(once, { hive_id: "hv_1", camera_id: "cam_1", motion: true, cooldown_minutes: 5 });
    expect(twice.cameras).toEqual([{ hive_id: "hv_1", camera_id: "cam_1", motion: true, cooldown_minutes: 5 }]);
    expect(defaults.cameras).toEqual([]);
  });
});
