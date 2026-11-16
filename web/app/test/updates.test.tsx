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
const baseHive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.1.6",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app" };

type Status = Record<string, unknown>;

function stub(hive: Record<string, unknown>, status: Status) {
  const calls: string[] = [];
  let current = { ...status };
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const body = req.method === "PUT" ? await req.text() : "";
    calls.push(`${req.method} ${u.pathname}${body ? " " + body : ""}`);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "GET /v0/hive/updates": return json(200, current);
        case "PUT /v0/hive/updates": {
          const s = JSON.parse(body) as Status;
          if (s.update_window === "05:00-05:00") return json(400, { code: "invalid_update_window", message: "" });
          current = { ...current, ...s };
          return json(200, current);
        }
        case "POST /v0/hive/updates/install": return json(202, {});
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "owner", cameras: ["*"], hive, quality: "full", ice_servers: [] });
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
afterEach(async () => { cleanup(); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); });

const status: Status = { updates: "automatic", update_window: "02:00-05:00", update_channel: "stable", running: "0.1.6", supported: true,
  last: { action: "update.applied", from: "0.1.5", to: "0.1.6", at: "2026-11-15T02:10:00Z" } };

describe("updates in the hive settings (#82)", () => {
  it("lets the owner change mode, window and channel, refuses a bad window, and installs an offered update", async () => {
    const calls = stub({ ...baseHive, capabilities: ["updates", "archive.delete"] }, { ...status, available: "0.1.7", updates: "manual" });
    const user = mount();
    const section = await screen.findByRole("region", { name: "Updates" });
    await user.click(within(section).getByRole("button", { name: "Manage updates…" }));

    expect(await within(section).findByText("0.1.6")).toBeInTheDocument();
    expect(within(section).getByTestId("update-last")).toHaveTextContent("Updated from 0.1.5 to 0.1.6");
    expect(within(section).getByTestId("update-available")).toHaveTextContent("Version 0.1.7 is available.");

    await user.selectOptions(within(section).getByLabelText("Install updates"), "automatic");
    await user.clear(within(section).getByLabelText("From"));
    await user.type(within(section).getByLabelText("From"), "05:00");
    await user.click(within(section).getByRole("button", { name: "Save" }));
    expect(await within(section).findByRole("alert")).toHaveTextContent("The update window needs two different times");

    await user.clear(within(section).getByLabelText("From"));
    await user.type(within(section).getByLabelText("From"), "22:00");
    await user.clear(within(section).getByLabelText("To"));
    await user.type(within(section).getByLabelText("To"), "23:30");
    await user.selectOptions(within(section).getByLabelText("Channel"), "beta");
    await user.click(within(section).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls).toContain(`PUT /v0/hive/updates {"updates":"automatic","update_window":"22:00-23:30","update_channel":"beta"}`));
    await waitFor(() => expect(within(section).queryByRole("alert")).toBeNull());

    await user.click(within(section).getByRole("button", { name: "Update now" }));
    await waitFor(() => expect(calls).toContain("POST /v0/hive/updates/install"));
    expect(await within(section).findByText(/installs it within a minute/)).toBeInTheDocument();
  });

  it("says why a build never updates and when a change needs a restart", async () => {
    stub({ ...baseHive, capabilities: ["updates"] }, { ...status, supported: false, unsupported_reason: "dev_build", last: undefined });
    let user = mount();
    let section = await screen.findByRole("region", { name: "Updates" });
    await user.click(within(section).getByRole("button", { name: "Manage updates…" }));
    expect(await within(section).findByText(/development build/)).toBeInTheDocument();
    expect(within(section).queryByRole("button", { name: "Update now" })).toBeNull();
    cleanup();

    stub({ ...baseHive, capabilities: ["updates"] }, { ...status, settings_need_restart: true });
    user = mount();
    section = await screen.findByRole("region", { name: "Updates" });
    await user.click(within(section).getByRole("button", { name: "Manage updates…" }));
    expect(await within(section).findByText(/apply after the hive's service restarts/)).toBeInTheDocument();
  });

  it("says when a waiting update installs, when one needs a package, and offers Update now only for what can install", async () => {
    stub({ ...baseHive, capabilities: ["updates"] }, { ...status, available: "0.1.8", needs_package: "0.2.0" });
    let user = mount();
    let section = await screen.findByRole("region", { name: "Updates" });
    await user.click(within(section).getByRole("button", { name: "Manage updates…" }));
    expect(await within(section).findByTestId("update-available")).toHaveTextContent("Version 0.1.8 installs in the update window, 02:00 to 05:00.");
    expect(within(section).getByTestId("update-needs-package")).toHaveTextContent("Version 0.2.0 needs a newer hive-updater");
    expect(within(section).getByRole("button", { name: "Update now" })).toBeInTheDocument();
    cleanup();

    stub({ ...baseHive, capabilities: ["updates"] }, status);
    user = mount();
    section = await screen.findByRole("region", { name: "Updates" });
    await user.click(within(section).getByRole("button", { name: "Manage updates…" }));
    expect(await within(section).findByText("No update is waiting.")).toBeInTheDocument();
    expect(within(section).queryByRole("button", { name: "Update now" })).toBeNull();
  });

  it("asks an old hive to be updated by hand, and offers Update now where the hive can", async () => {
    stub({ ...baseHive, version: "0.1.5", capabilities: undefined }, status);
    mount();
    const old = await screen.findByRole("region", { name: "Updates" });
    expect(within(old).getByRole("note")).toHaveTextContent("This needs hive version 0.1.7 or later; this hive runs 0.1.5.");
    expect(within(old).queryByRole("button", { name: "Update now" })).toBeNull();
    cleanup();

    const calls = stub({ ...baseHive, capabilities: ["updates"] }, status); // no archive.delete
    const user = mount();
    const deletion = await screen.findByRole("region", { name: "Delete recordings" });
    await user.click(within(deletion).getByRole("button", { name: "Update now" }));
    await waitFor(() => expect(calls).toContain("POST /v0/hive/updates/install"));
  });
});
