import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";
import { cacheSet } from "../src/lib/cache";
import { writeLocal } from "../src/lib/localLogin";
import { dayStart } from "../src/lib/archive";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const LAN = "hv_1.h.camhive.app:16017";
const LOCAL_TOKEN = "v4.local.loc-tok";
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.1.7", capabilities: ["archive.encryption", "updates"],
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app" };
const me = { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" };
const camera = { id: "cam_1", name: "Door", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: { main: { Codec: "H264" } }, created_at: "", updated_at: "" };

type State = { state: "off" | "locked" | "unlocked"; sealing: boolean };

/**
 * A hive with archive encryption as PR #110 built it: owner requests, a
 * passphrase, five-strikes rate limit simplified to one magic passphrase,
 * and the cloud-only requests refused to a local session.
 */
function fakeHive(start: State, opts: { role?: string; cloud?: boolean; h?: Record<string, unknown>; lockOnRead?: boolean } = {}) {
  const h = opts.h ?? hive;
  let enc = { ...start };
  let failReads = false;
  /** Footage reads: 423 while locked; `lockOnRead` locks first, as a restart the app has not seen yet. */
  const footage = (ok: () => Response) => {
    if (opts.lockOnRead) enc = { ...enc, state: "locked" };
    return enc.state === "locked" ? json(423, { code: "archive_locked", message: "" }) : ok();
  };
  let passphrase = "correct horse battery";
  const calls: string[] = [];
  const cloudOnly = new Set(["POST /v0/archive/encryption", "POST /v0/archive/encryption/passphrase", "GET /v0/archive/encryption/key-file"]);
  async function onHive(req: Request, route: string): Promise<Response> {
    if (cloudOnly.has(route) && req.headers.get("Authorization") === `Bearer ${LOCAL_TOKEN}`) return json(403, { code: "forbidden", message: "" });
    const body = req.method === "POST" ? ((await req.json().catch(() => ({}))) as { passphrase?: string; new_passphrase?: string }) : {};
    switch (route) {
      case "GET /healthz": return json(200, {});
      case "GET /v0/archive/encryption": return failReads ? json(502, { code: "engine", message: "" }) : json(200, enc);
      case "POST /v0/archive/encryption":
        if (enc.state !== "off") return json(409, { code: "encryption_on", message: "" });
        passphrase = body.passphrase!;
        enc = { state: "unlocked", sealing: true };
        return json(200, enc);
      case "POST /v0/archive/encryption/unlock":
        // As the browser sees it cross-origin: hives up to v0.1.7 do not expose Retry-After, later ones do.
        if (body.passphrase === "too many wrong ones") return json(429, { code: "unlock_rate_limited", message: "" });
        if (body.passphrase === "too many, newer hive") return new Response(JSON.stringify({ code: "unlock_rate_limited", message: "" }), { status: 429, headers: { "Retry-After": "600" } });
        if (body.passphrase !== passphrase) return json(403, { code: "wrong_passphrase", message: "" });
        enc = { state: "unlocked", sealing: true };
        return json(200, enc);
      case "POST /v0/archive/encryption/lock":
        enc = { ...enc, state: "locked" };
        return json(200, enc);
      case "POST /v0/archive/encryption/passphrase":
        if (body.passphrase !== passphrase) return json(403, { code: "wrong_passphrase", message: "" });
        passphrase = body.new_passphrase!;
        return new Response(null, { status: 204 });
      case "GET /v0/archive/encryption/key-file": return new Response(new Uint8Array([1, 2, 3]), { status: 200, headers: { "Content-Type": "application/octet-stream" } });
      case "GET /v0/cameras": return json(200, [camera]);
      case "GET /v0/health/cameras": return json(200, [{ camera: "cam_1", enabled: true, online: true, streams: {} }]);
      case "GET /v0/cameras/cam_1/archive/calendar": return json(200, [{ day: "2026-11-09" }]);
      case "GET /v0/cameras/cam_1/archive/timeline": return json(200, [{ start: new Date(dayStart("2026-11-09").getTime() + 8 * 3_600_000).toISOString(), duration_ms: 3_600_000, bytes: 1, complete: true }]);
      case "GET /v0/cameras/cam_1/archive/play": return footage(() => new Response(new Uint8Array([0, 0, 0, 1]), { status: 200, headers: { "Content-Type": "video/mp4" } }));
      case "GET /v0/exports/exp_1/file": return footage(() => new Response(new Uint8Array([1]), { status: 200, headers: { "Content-Type": "video/mp4" } }));
      case "GET /v0/cameras/cam_1/events": return json(200, []);
      case "GET /v0/exports": return json(200, [{ id: "exp_1", camera: "cam_1", stream: "main", start: new Date(dayStart("2026-11-09").getTime() + 8 * 3_600_000).toISOString(), duration_seconds: 600, status: "done", bytes: 10, file_name: "door.mp4", created_at: "" }]);
      default: return json(404, { code: "not_found", message: "" });
    }
  }
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const route = `${req.method} ${u.pathname}`;
    if (u.host === LAN) {
      calls.push(`${route} ${req.headers.get("Authorization") ?? ""}`.trim());
      return onHive(req, route);
    }
    if (opts.cloud === false) throw new TypeError("Failed to fetch");
    switch (route) {
      case "GET /v1/me": return json(200, me);
      case "GET /v1/hives": return json(200, opts.role ? { hives: [], shared: [h] } : { hives: [h], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: opts.role ?? "owner", cameras: ["*"], hive: h, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return { calls, state: () => enc, set: (e: State) => { enc = { ...e }; }, failReads: (on: boolean) => { failReads = on; } };
}

const downloads: string[] = [];
let qc: QueryClient;
/** What the 60 s poll or a window refocus does: ask the hive again. */
const refetchState = () => act(() => qc.invalidateQueries({ queryKey: ["hive", "hv_1", "encryption"] }));
/** Clicks the timeline at 08:30, where the fake hive has footage. */
function seekTo0830() {
  const timeline = screen.getByTestId("timeline");
  vi.spyOn(timeline, "getBoundingClientRect").mockReturnValue({ left: 0, width: 240, top: 0, height: 32, right: 240, bottom: 32, x: 0, y: 0, toJSON: () => ({}) });
  fireEvent.click(timeline, { clientX: 85 });
}

function mount(path: string) {
  transport.invalidateAll();
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:x", revokeObjectURL: () => undefined }));
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) { downloads.push(this.download); });
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<App router={router} queryClient={qc} />);
  return userEvent.setup();
}

async function openSettings(user: ReturnType<typeof userEvent.setup>) {
  const section = await screen.findByRole("region", { name: "Encrypted recordings" }, { timeout: 5000 });
  await user.click(within(section).getByRole("button", { name: "Manage encryption…" }));
  await within(section).findByTestId("encryption-state");
  return section;
}

beforeEach(() => { resetCloudForTest(); localStorage.clear(); sessionStorage.clear(); downloads.length = 0; });
afterEach(async () => { cleanup(); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("switching archive encryption on (ADR-0019 §4)", () => {
  it("warns, wants the passphrase twice, and holds the owner on the key-file download before it is done", async () => {
    const hv = fakeHive({ state: "off", sealing: false });
    const user = mount("/hives/hv_1/settings");
    const section = await openSettings(user);
    expect(within(section).getByTestId("encryption-state")).toHaveTextContent("Off");
    expect(within(section).getByRole("note")).toHaveTextContent("nobody can play the encrypted recordings, Camhive included");
    await user.click(within(section).getByRole("button", { name: "I understand, choose a passphrase" }));

    await user.type(within(section).getByLabelText("Passphrase"), "too short");
    await user.type(within(section).getByLabelText("Passphrase again"), "too short");
    await user.click(within(section).getByRole("button", { name: "Switch encryption on" }));
    expect(within(section).getByRole("alert")).toHaveTextContent("10 to 256 characters");
    await user.clear(within(section).getByLabelText("Passphrase"));
    await user.type(within(section).getByLabelText("Passphrase"), "a long passphrase");
    await user.clear(within(section).getByLabelText("Passphrase again"));
    await user.type(within(section).getByLabelText("Passphrase again"), "a long passphrasE");
    await user.click(within(section).getByRole("button", { name: "Switch encryption on" }));
    expect(within(section).getByRole("alert")).toHaveTextContent("The two passphrases differ");
    expect(hv.calls.some((c) => c.startsWith("POST /v0/archive/encryption "))).toBe(false);

    await user.clear(within(section).getByLabelText("Passphrase again"));
    await user.type(within(section).getByLabelText("Passphrase again"), "a long passphrase");
    await user.click(within(section).getByRole("button", { name: "Switch encryption on" }));
    const backup = await within(section).findByTestId("encryption-backup");
    expect(hv.state().state).toBe("unlocked");
    await waitFor(() => expect(document.activeElement).toHaveTextContent("Encryption is on. Download the key file now."));
    expect(within(backup).getByRole("button", { name: "Done" })).toBeDisabled();
    // A LAN hiccup on a background refetch (the Save dialog refocuses the window) keeps the owner on the key file.
    hv.failReads(true);
    await refetchState();
    expect(await within(section).findByRole("alert")).toHaveTextContent("video engine reported an error");
    expect(within(section).getByTestId("encryption-backup")).toBeInTheDocument();
    hv.failReads(false);
    await user.click(within(backup).getByRole("button", { name: "Download key file" }));
    await waitFor(() => expect(downloads).toEqual(["camhive-archive-key-hv_1.age"]));
    await user.click(within(backup).getByRole("button", { name: "Done" }));
    expect(within(section).getByTestId("encryption-state")).toHaveTextContent("On and unlocked");
  }, 15_000);
});

describe("switching on a hive that is already on", () => {
  it("offers the key file instead of leaving the owner on the passphrase form", async () => {
    const hv = fakeHive({ state: "off", sealing: false });
    const user = mount("/hives/hv_1/settings");
    const section = await openSettings(user);
    await user.click(within(section).getByRole("button", { name: "I understand, choose a passphrase" }));
    hv.set({ state: "unlocked", sealing: true }); // switched on from another device meanwhile
    await user.type(within(section).getByLabelText("Passphrase"), "a long passphrase");
    await user.type(within(section).getByLabelText("Passphrase again"), "a long passphrase");
    await user.click(within(section).getByRole("button", { name: "Switch encryption on" }));
    const backup = await within(section).findByTestId("encryption-backup");
    expect(within(backup).getByRole("button", { name: "Download key file" })).toBeInTheDocument();
  }, 15_000);
});

describe("unlocking, locking and the passphrase", () => {
  it("locks, refuses a wrong passphrase, says how long a rate limit lasts, unlocks, and offers a fresh key file after a change", async () => {
    const hv = fakeHive({ state: "unlocked", sealing: true });
    const user = mount("/hives/hv_1/settings");
    const section = await openSettings(user);
    await user.click(within(section).getByRole("button", { name: "Lock recordings" }));
    await waitFor(() => expect(within(section).getByTestId("encryption-state")).toHaveTextContent("On and locked"));

    await user.type(within(section).getByLabelText("Passphrase"), "not the passphrase");
    await user.click(within(section).getByRole("button", { name: "Unlock" }));
    expect(await within(section).findByRole("alert")).toHaveTextContent("Wrong passphrase");
    await user.clear(within(section).getByLabelText("Passphrase"));
    await user.type(within(section).getByLabelText("Passphrase"), "too many wrong ones");
    await user.click(within(section).getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(within(section).getByRole("alert")).toHaveTextContent("blocks unlocking for up to 15 minutes"));
    await user.clear(within(section).getByLabelText("Passphrase"));
    await user.type(within(section).getByLabelText("Passphrase"), "too many, newer hive");
    await user.click(within(section).getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(within(section).getByRole("alert")).toHaveTextContent("Try again in 10 minutes"));
    await user.clear(within(section).getByLabelText("Passphrase"));
    await user.type(within(section).getByLabelText("Passphrase"), "correct horse battery");
    await user.click(within(section).getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(within(section).getByTestId("encryption-state")).toHaveTextContent("On and unlocked"));

    await user.click(within(section).getByRole("button", { name: "Change passphrase…" }));
    await user.type(within(section).getByLabelText("Current passphrase"), "correct horse battery");
    await user.type(within(section).getByLabelText("New passphrase"), "another long one");
    await user.type(within(section).getByLabelText("New passphrase again"), "another long one");
    await user.click(within(section).getByRole("button", { name: "Change passphrase" }));
    const changed = await within(section).findByTestId("passphrase-changed");
    expect(changed).toHaveTextContent("opens only with the old passphrase");
    await user.click(within(changed).getByRole("button", { name: "Download the new key file" }));
    await waitFor(() => expect(downloads).toEqual(["camhive-archive-key-hv_1.age"]));
    expect(hv.calls).toContain("POST /v0/archive/encryption/passphrase Bearer tok");
  }, 15_000);

  it("warns the owner when the hive records in clear because it cannot read the archive key", async () => {
    fakeHive({ state: "locked", sealing: false });
    const user = mount("/hives/hv_1/settings");
    const section = await openSettings(user);
    expect(within(section).getByTestId("not-sealing")).toHaveTextContent("new recordings are stored unencrypted");
  });
});

describe("passphrase fields and the browser's saved passwords", () => {
  it("name a credential per hive, so a password manager neither fills nor overwrites the account password", async () => {
    fakeHive({ state: "unlocked", sealing: true });
    const user = mount("/hives/hv_1/settings");
    const section = await openSettings(user);
    await user.click(within(section).getByRole("button", { name: "Change passphrase…" }));
    await user.click(within(section).getByRole("button", { name: "Lock recordings" }));
    await waitFor(() => expect(within(section).getByTestId("encryption-state")).toHaveTextContent("On and locked"));
    const forms = within(section).getAllByRole("form");
    expect(forms.map((f) => f.getAttribute("aria-label"))).toEqual(["Unlock recordings", "Change passphrase…"]);
    for (const f of forms) {
      const account = f.querySelector<HTMLInputElement>('input[autocomplete="username"]');
      expect(account?.value).toBe("camhive-archive-hv_1");
    }
  }, 15_000);
});

describe("a local emergency session (#57)", () => {
  function signedInLocally(start: State) {
    const hv = fakeHive(start, { cloud: false });
    cacheSet("me", me);
    cacheSet("hives", { hives: [{ ...hive, online: false }], shared: [] }); // the emergency: the cloud cannot reach the hive
    writeLocal("hv_1", { token: LOCAL_TOKEN, session_id: "local_1", expires_at: new Date(Date.now() + 3_600_000).toISOString(), role: "owner", cameras: ["*"], local: true });
    return hv;
  }

  it("unlocks and locks, but offers no key file and no passphrase change, which need Camhive", async () => {
    const hv = signedInLocally({ state: "locked", sealing: true });
    const user = mount("/hives/hv_1/settings");
    const section = await openSettings(user);
    expect(within(section).getByText(/need a sign-in through Camhive/)).toBeInTheDocument();
    expect(within(section).queryByRole("button", { name: "Download key file" })).toBeNull();
    expect(within(section).queryByRole("button", { name: "Change passphrase…" })).toBeNull();
    await user.type(within(section).getByLabelText("Passphrase"), "correct horse battery");
    await user.click(within(section).getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(within(section).getByTestId("encryption-state")).toHaveTextContent("On and unlocked"));
    expect(hv.calls).toContain(`POST /v0/archive/encryption/unlock Bearer ${LOCAL_TOKEN}`);
    await user.click(within(section).getByRole("button", { name: "Lock recordings" }));
    await waitFor(() => expect(hv.state().state).toBe("locked"));
  });

  it("does not offer to switch encryption on", async () => {
    signedInLocally({ state: "off", sealing: false });
    const user = mount("/hives/hv_1/settings");
    const section = await screen.findByRole("region", { name: "Encrypted recordings" }, { timeout: 5000 });
    await user.click(within(section).getByRole("button", { name: "Manage encryption…" }));
    expect(await within(section).findByText(/need a sign-in through Camhive/)).toBeInTheDocument();
    expect(within(section).queryByRole("button", { name: "I understand, choose a passphrase" })).toBeNull();
  });
});

describe("a locked archive on the hive page", () => {
  it("gives the owner a one-tap unlock, and the notice goes once unlocked", async () => {
    const hv = fakeHive({ state: "locked", sealing: true });
    const user = mount("/hives/hv_1");
    const notice = await screen.findByTestId("archive-locked", {}, { timeout: 5000 });
    expect(notice).toHaveTextContent("The recordings on this hive are locked: the hive restarted or you locked them.");
    await user.click(within(notice).getByRole("button", { name: "Unlock" }));
    await user.type(within(notice).getByLabelText("Passphrase"), "correct horse battery");
    await user.click(within(notice).getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(screen.queryByTestId("archive-locked")).toBeNull());
    expect(hv.state().state).toBe("unlocked");
  });

  it("shows the lock once, where the footage would be, keeps export and sound from asking, and plays on after the unlock", async () => {
    const hv = fakeHive({ state: "locked", sealing: true });
    const user = mount("/hives/hv_1?camera=cam_1&day=2026-11-09");
    const detail = await screen.findByTestId("camera-detail", {}, { timeout: 5000 });
    const notice = await within(detail).findByTestId("archive-locked");
    expect(screen.getAllByTestId("archive-locked")).toHaveLength(1); // not again at the top of the hive page
    expect(within(detail).getByTestId("notices").children).toHaveLength(1); // the lock only: no "pick a moment" under it (#126)
    expect(within(detail).getByRole("button", { name: "Export from here" })).toBeDisabled();
    expect(within(detail).getByRole("button", { name: "Sound off" })).toBeDisabled();
    expect(await within(detail).findByRole("button", { name: "Download" })).toBeDisabled();

    await waitFor(() => expect(within(screen.getByTestId("timeline")).getAllByTestId("segment")).toHaveLength(1));
    seekTo0830();
    await waitFor(() => expect(hv.calls.filter((c) => c.startsWith("GET /v0/cameras/cam_1/archive/play"))).toHaveLength(1));
    await user.click(within(notice).getByRole("button", { name: "Unlock" }));
    await user.type(within(notice).getByLabelText("Passphrase"), "correct horse battery");
    await user.click(within(notice).getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(hv.calls.filter((c) => c.startsWith("GET /v0/cameras/cam_1/archive/play"))).toHaveLength(2));
    expect(screen.queryByTestId("archive-locked")).toBeNull();
  }, 15_000);

  it("closes an open export form when the archive is locked meanwhile", async () => {
    const hv = fakeHive({ state: "unlocked", sealing: true });
    const user = mount("/hives/hv_1?camera=cam_1&day=2026-11-09");
    await waitFor(() => expect(within(screen.getByTestId("timeline")).getAllByTestId("segment")).toHaveLength(1), { timeout: 5000 });
    seekTo0830();
    await user.click(await screen.findByRole("button", { name: "Export from here" }));
    expect(screen.getByRole("form", { name: "Export clip" })).toBeInTheDocument();
    hv.set({ state: "locked", sealing: true });
    await refetchState();
    await waitFor(() => expect(screen.queryByRole("form", { name: "Export clip" })).toBeNull());
  }, 15_000);

  it("shows the lock when an export download finds the archive locked since the app last asked", async () => {
    fakeHive({ state: "unlocked", sealing: true }, { lockOnRead: true });
    const user = mount("/hives/hv_1?camera=cam_1&day=2026-11-09");
    const detail = await screen.findByTestId("camera-detail", {}, { timeout: 5000 });
    await user.click(await within(detail).findByRole("button", { name: "Download" }));
    expect(await within(detail).findByTestId("archive-locked")).toBeInTheDocument();
    expect(downloads).toEqual([]);
  });

  it("shows the lock, not an error, when playback finds the archive locked since the app last asked", async () => {
    fakeHive({ state: "unlocked", sealing: true }, { lockOnRead: true });
    mount("/hives/hv_1?camera=cam_1&day=2026-11-09");
    const timeline = await screen.findByTestId("timeline", {}, { timeout: 5000 });
    await waitFor(() => expect(within(timeline).getAllByTestId("segment")).toHaveLength(1));
    vi.spyOn(timeline, "getBoundingClientRect").mockReturnValue({ left: 0, width: 240, top: 0, height: 32, right: 240, bottom: 32, x: 0, y: 0, toJSON: () => ({}) });
    fireEvent.click(timeline, { clientX: 85 }); // 08:30
    const detail = screen.getByTestId("camera-detail");
    expect(await within(detail).findByTestId("archive-locked")).toBeInTheDocument();
    expect(within(detail).queryByRole("alert")).toBeNull();
  });

  it("tells a member the owner locked it, without an unlock", async () => {
    fakeHive({ state: "locked", sealing: false }, { role: "viewer" });
    mount("/hives/hv_1");
    const notice = await screen.findByTestId("archive-locked", {}, { timeout: 5000 });
    expect(notice).toHaveTextContent("locked by its owner");
    expect(within(notice).queryByRole("button")).toBeNull();
    expect(within(notice).queryByTestId("not-sealing")).toBeNull();
  });

  it("asks nothing of a hive without encryption", async () => {
    const old = { ...hive, version: "0.1.6", capabilities: undefined };
    const hv = fakeHive({ state: "off", sealing: false }, { h: old });
    mount("/hives/hv_1");
    await waitFor(() => expect(hv.calls).toContain("GET /v0/cameras Bearer tok"));
    expect(hv.calls.some((c) => c.includes("/v0/archive/encryption"))).toBe(false);
    expect(screen.queryByTestId("archive-locked")).toBeNull();
  });
});
