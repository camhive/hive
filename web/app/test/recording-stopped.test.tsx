import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";
import i18n from "../src/i18n";
import { recordingStop } from "../src/lib/recording";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const base = { id: "hv_1", name: "Home", status: "active" as const, online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.1.11",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app", capabilities: ["storage"] };
const since = "2026-11-24T10:30:00Z";
const camera = { id: "cam_1", name: "Door", enabled: true, source: { kind: "rtsp", address: "rtsp://x/m" }, streams: [] };

function stub(opts: { hive?: Record<string, unknown>; info?: Record<string, unknown>; infoDelay?: number }) {
  const hive = { ...base, ...opts.hive };
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    if (u.host === "hv_1.h.camhive.app:16017") {
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "GET /v0/cameras": return json(200, [camera]);
        case "GET /v0/hive":
          if (opts.infoDelay) await new Promise((r) => setTimeout(r, opts.infoDelay));
          return json(200, { version: "0.1.11", capabilities: [], archive_encryption: "off", ...opts.info });
        default: return json(404, { code: "not_found", message: "" });
      }
    }
    switch (`${req.method} ${u.pathname}`) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access":
        if (!hive.online) return json(409, { error: { code: "hive_offline", message: "" } });
        return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "viewer", cameras: ["*"], hive, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
}

function mount(path: string) {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
}

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });
afterEach(async () => { cleanup(); await i18n.changeLanguage("en"); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const at = new Date(since).toLocaleString("en");

describe("a hive that stopped recording (#172)", () => {
  it("shows a red banner on the hive page from the hive's own status, with the time and the reason", async () => {
    stub({ info: { not_recording: { since, reason: "archive_missing" } } });
    mount("/hives/hv_1");
    const banner = await screen.findByTestId("recording-stopped");
    expect(banner).toHaveAttribute("role", "alert");
    expect(banner).toHaveClass("error");
    expect(banner).toHaveTextContent(`Recording stopped at ${at}: the folder the hive records into is gone`);
    expect(banner).toHaveAttribute("data-source", "hive");
  });

  it("shows it in the archive view of a camera too", async () => {
    stub({ info: { not_recording: { since, reason: "database" } } });
    mount("/hives/hv_1?camera=cam_1&view=recordings");
    expect(await screen.findByTestId("recording-stopped")).toHaveTextContent("its database reports disk errors");
  });

  it("falls back to the cloud's last word when the hive cannot be reached", async () => {
    stub({ hive: { online: false, recording_stopped: { since, reason: "archive_missing" } } });
    mount("/hives/hv_1");
    const banner = await screen.findByTestId("recording-stopped");
    expect(banner).toHaveAttribute("data-source", "cloud");
    expect(banner).toHaveTextContent("as the hive last told Camhive");
  });

  it("says it in German", async () => {
    await i18n.changeLanguage("de");
    stub({ info: { not_recording: { since, reason: "engine_stalled" } } });
    mount("/hives/hv_1");
    expect(await screen.findByTestId("recording-stopped")).toHaveTextContent(`Aufnahme gestoppt am ${new Date(since).toLocaleString("de")}: Kameras sind online, aber keine neue Aufnahme hat begonnen`);
  });

  it("shows nothing while the hive records, even when the cloud still remembers a stop", async () => {
    stub({ hive: { recording_stopped: { since, reason: "archive_missing" } } }); // the hive's own word is fresher
    mount("/hives/hv_1");
    await screen.findAllByText("Door");
    expect(screen.queryByTestId("recording-stopped")).toBeNull();
  });
});

describe("while the hive's answer loads (#172 review)", () => {
  it("does not flash the cloud's older word before the hive's own answer", async () => {
    stub({ hive: { recording_stopped: { since, reason: "archive_missing" } }, infoDelay: 300 });
    mount("/hives/hv_1");
    await screen.findAllByText("Door");
    expect(screen.queryByTestId("recording-stopped")).toBeNull();
    await new Promise((r) => setTimeout(r, 400));
    expect(screen.queryByTestId("recording-stopped")).toBeNull();
  });

  it("names a disk that is not mounted", async () => {
    stub({ info: { not_recording: { since, reason: "disk_not_mounted" } } });
    mount("/hives/hv_1");
    expect(await screen.findByTestId("recording-stopped")).toHaveTextContent("the disk the hive records to is not mounted");
  });
});

describe("recordingStop", () => {
  const stopped = { since, reason: "archive_missing" as const };
  it("prefers the hive's answer and falls back to the cloud's", () => {
    expect(recordingStop({ ...base }, undefined)).toBeUndefined();
    expect(recordingStop({ ...base, recording_stopped: stopped }, undefined)).toEqual({ ...stopped, source: "cloud" });
    expect(recordingStop({ ...base, recording_stopped: stopped }, { not_recording: undefined })).toBeUndefined();
    expect(recordingStop({ ...base }, { not_recording: { since, reason: "database" } })).toEqual({ since, reason: "database", source: "hive" });
  });
});
