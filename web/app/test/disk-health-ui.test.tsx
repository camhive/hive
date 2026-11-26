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
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.1.10",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app", capabilities: ["disk.health", "storage", "updates"] };
const failing = {
  state: "failing", checked_at: "2026-11-22T11:30:00Z", device: "/dev/sdb",
  evidence: [{ signal: "smart", detail: "/dev/sdb: pending sectors", values: { pending_sectors: 8 } }],
};
const storage = (health: unknown) => ({
  cameras: [], disk: { total_bytes: 1e12, archive_bytes: 0, export_bytes: 0, free_bytes: 5e11, health },
  settings: { retention_days: 30, min_free_gb: 10, export_max_gb: 10 },
  forecast: { bytes_per_day: 0, capacity_bytes: 0, disk_days: 0, retention_met: true, deleting_for_space: false },
});
const files = [
  { name: "hive.key", path: "/var/lib/camhive/hive.key" }, { name: "cloud.json", path: "/var/lib/camhive/cloud.json" },
  { name: "hive.yaml", path: "/etc/camhive/hive.yaml" }, { name: "archive-key", path: "/var/lib/camhive/archive-key/" },
  { name: "hive.db", path: "/var/lib/camhive/hive.db" },
];
const idle = { state: "none", supported: true, data_dir: "/var/lib/camhive", archive_dir: "/mnt/cams/archive", files, copied_bytes: 0, planned_bytes: 0, skipped_segments: 0, skipped_bytes: 0 };

function stub(role: "owner" | "viewer", answers: { storage?: unknown; move?: () => unknown }) {
  const calls: string[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    calls.push(`${req.method} ${u.host === "hv_1.h.camhive.app:16017" ? "hive" : "cloud"} ${u.pathname}`);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "GET /v0/cameras": return json(200, []);
        case "GET /v0/storage": return json(200, answers.storage ?? storage({ state: "ok", evidence: [] }));
        case "GET /v0/hive/move": return json(200, answers.move ? answers.move() : idle);
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, role === "owner" ? { hives: [hive], shared: [] } : { hives: [], shared: [hive] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role, cameras: ["*"], hive, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount(path: string) {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return userEvent.setup();
}

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });
afterEach(async () => { cleanup(); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("a failing archive disk (#155)", () => {
  it("shows the owner a banner with the evidence, the exact files to save first and the command", async () => {
    stub("owner", { storage: storage(failing) });
    mount("/hives/hv_1");
    const banner = await screen.findByTestId("disk-health");
    expect(banner).toHaveTextContent("The disk /dev/sdb is failing: replace it soon");
    expect(within(banner).getByTestId("disk-evidence")).toHaveTextContent("SMART: /dev/sdb: pending sectors");
    await waitFor(() => expect(within(banner).getByTestId("files-to-save")).toHaveTextContent("/var/lib/camhive/hive.key"));
    const listed = within(banner).getByTestId("files-to-save").querySelectorAll("li");
    expect([...listed].map((f) => f.textContent)).toEqual(["/var/lib/camhive/hive.key", "/var/lib/camhive/cloud.json", "/etc/camhive/hive.yaml", "/var/lib/camhive/archive-key/", "/var/lib/camhive/hive.db"]);
    expect(within(banner).getByTestId("disk-command")).toHaveTextContent("sudo hive move-data /mnt/newdisk/camhive");
    expect(within(banner).getByRole("link", { name: "Move to a new disk step by step" })).toHaveAttribute("href", "/hives/hv_1/settings#settings-move-data");
  });

  it("names no command for a hive the command cannot move", async () => {
    stub("owner", { storage: storage(failing), move: () => ({ ...idle, supported: false }) });
    mount("/hives/hv_1");
    const banner = await screen.findByTestId("disk-health");
    await waitFor(() => expect(banner).toHaveTextContent("systemd service on Linux"));
    expect(within(banner).queryByTestId("disk-command")).toBeNull();
  });

  it("shows nothing for a healthy disk, and nothing to a member", async () => {
    stub("owner", {});
    mount("/hives/hv_1");
    await screen.findByRole("button", { name: /Add camera/ }).catch(() => undefined);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByTestId("disk-health")).toBeNull();
    cleanup();
    const calls = stub("viewer", { storage: storage(failing) });
    mount("/hives/hv_1");
    await new Promise((r) => setTimeout(r, 100));
    expect(screen.queryByTestId("disk-health")).toBeNull();
    expect(calls).not.toContain("GET hive /v0/storage");
  });
});

describe("the guided move in the settings' Recordings and disk group (#155)", () => {
  it("builds the command for the new directory and refuses one inside the data directory", async () => {
    stub("owner", {});
    const user = mount("/hives/hv_1/settings");
    const card = await screen.findByTestId("move-data");
    await user.click(within(card).getByRole("button", { name: "Move the data…" }));
    const target = await within(card).findByLabelText("New data directory on that disk");
    expect(within(card).getByTestId("move-command")).toHaveTextContent("sudo hive move-data /mnt/newdisk/camhive");
    await user.type(target, "/var/lib/camhive/new");
    expect(within(card).getByRole("alert")).toHaveTextContent("not inside or around /var/lib/camhive");
    expect(within(card).getByRole("button", { name: "Copy the command" })).toBeDisabled();
    await user.clear(target);
    await user.type(target, "/mnt/cams/archive/new");
    expect(within(card).getByRole("alert")).toHaveTextContent("not inside or around"); // inside the archive directory
    await user.clear(target);
    await user.type(target, "/mnt/big disk/camhive");
    expect(within(card).getByRole("alert")).toHaveTextContent("without spaces");
    expect(within(card).getByRole("button", { name: "Copy the command" })).toBeDisabled();
    await user.clear(target);
    await user.type(target, "/mnt/big/camhive");
    expect(within(card).getByTestId("move-command")).toHaveTextContent("sudo hive move-data /mnt/big/camhive");
    expect(within(card).getByRole("button", { name: "Copy the command" })).toBeEnabled();
  });

  it("follows a move the command runs, and says what did not fit", async () => {
    let note: Record<string, unknown> = { ...idle, state: "copying", from: "/var/lib/camhive", to: "/mnt/new", step: "archive", copied_bytes: 25e9, planned_bytes: 100e9 };
    stub("owner", { move: () => note });
    const user = mount("/hives/hv_1/settings");
    const card = await screen.findByTestId("move-data");
    await user.click(within(card).getByRole("button", { name: "Move the data…" }));
    const progress = await within(card).findByTestId("move-progress");
    expect(progress).toHaveTextContent("Moving to /mnt/new: archive");
    expect(progress).toHaveTextContent("(25 %)");
    expect(progress).toHaveTextContent("does not record until the move ends");
    note = { ...note, state: "done", copied_bytes: 100e9, skipped_segments: 3, skipped_bytes: 3e9, skipped_newest: "2026-11-20T08:20:00Z" };
    await waitFor(() => expect(within(card).getByTestId("move-progress")).toHaveAttribute("data-state", "done"), { timeout: 5000 });
    expect(within(card).getByTestId("move-skipped")).toHaveTextContent("3 segments");
    expect(within(card).getByLabelText("New data directory on that disk")).toBeInTheDocument(); // the steps stay reachable
  });

  it("lists the files to save for a hive the command cannot move", async () => {
    stub("owner", { move: () => ({ ...idle, supported: false, data_dir: "C:\\ProgramData\\Camhive" }) });
    const user = mount("/hives/hv_1/settings");
    const card = await screen.findByTestId("move-data");
    await user.click(within(card).getByRole("button", { name: "Move the data…" }));
    expect(await within(card).findByTestId("move-unsupported")).toHaveTextContent("systemd service on Linux");
  });
});
