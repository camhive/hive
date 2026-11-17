import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { CamhiveError } from "@camhive/api";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { lanBase, transport } from "../src/lib/transport";
import { resetCloudForTest } from "../src/lib/cloud";
import { cacheSet } from "../src/lib/cache";
import { clearLocal, readLocal, signInLocally, writeLocal } from "../src/lib/localLogin";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active" as const, online: true, claimed_at: "2026-11-09T00:00:00Z", lan: { host: "hv-1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" as const }, relay_host: "hv_1.relay.camhive.app" };
const LAN = "hv-1.h.camhive.app:16017";
const ADDRESS = "192.168.1.20:16017";
const session = (ms = 3_600_000) => ({ token: "v4.local.loc-tok", session_id: "local_1", expires_at: new Date(Date.now() + ms).toISOString(), role: "owner" as const, cameras: ["*"], local: true as const });
const me = { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" };

class FakePC extends EventTarget {
  localDescription: { type: string; sdp: string } | null = null;
  iceGatheringState = "complete";
  connectionState = "new";
  constructor(public config: RTCConfiguration) { super(); }
  addTransceiver() { return {}; }
  async createOffer() { return { type: "offer", sdp: "v=0 offer" }; }
  async setLocalDescription(d: { type: string; sdp: string }) { this.localDescription = d; }
  async setRemoteDescription() { queueMicrotask(() => { this.connectionState = "connected"; this.dispatchEvent(new Event("connectionstatechange")); }); }
  close() {}
}

async function settle() {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
}

afterEach(async () => { await settle(); resetCloudForTest(); localStorage.clear(); sessionStorage.clear(); });

describe("local sessions", () => {
  it("are kept for this tab only while valid", () => {
    writeLocal("hv_1", session());
    expect(readLocal("hv_1")?.token).toBe("v4.local.loc-tok");
    expect(localStorage.getItem("camhive.local.hv_1")).toBeNull();
    expect(sessionStorage.getItem("camhive.local.hv_1")).not.toBeNull();
    expect(readLocal("hv_1", Date.now() + 3_600_001)).toBeNull();
    clearLocal("hv_1");
    expect(readLocal("hv_1")).toBeNull();
  });

  it("sign in on the hive's LAN name only, never through the relay", async () => {
    const calls: string[] = [];
    const f = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      calls.push(`${req.method} ${req.url} ${await req.text()}`);
      return json(201, session());
    }) as typeof fetch;
    const s = await signInLocally(lanBase(hive), "anna", "correct horse battery", f);
    expect(s.token).toBe("v4.local.loc-tok");
    expect(calls).toEqual([`POST https://${LAN}/v0/local-login/sessions {"username":"anna","password":"correct horse battery"}`]);

    const refused = (async () => new Response(JSON.stringify({ code: "local_login_rate_limited", message: "" }), { status: 429, headers: { "Retry-After": "540" } })) as typeof fetch;
    await expect(signInLocally(lanBase(hive), "anna", "x", refused)).rejects.toMatchObject({ code: "local_login_rate_limited", status: 429, retryAfterS: 540 });
    const hidden = (async () => new Response(JSON.stringify({ code: "local_login_rate_limited", message: "" }), { status: 429 })) as typeof fetch; // a hive up to v0.1.7: CORS hides Retry-After
    await expect(signInLocally(lanBase(hive), "anna", "x", hidden)).rejects.toMatchObject({ status: 429, retryAfterS: null });
    await expect(signInLocally(null, "anna", "x", f)).rejects.toBeInstanceOf(CamhiveError);
  });
});

describe("the hive page with the cloud gone and no offline grant", () => {
  it("offers a local sign-in when the LAN name answers, then shows live video with a local-session banner", async () => {
    const calls: string[] = [];
    let password = "";
    vi.stubGlobal("RTCPeerConnection", FakePC);
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const u = new URL(req.url);
      calls.push(`${req.method} ${u.host}${u.pathname} ${req.headers.get("Authorization") ?? ""}`);
      if (u.host !== LAN) throw new TypeError("Failed to fetch"); // the cloud is dead
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "POST /v0/local-login/sessions":
          password = (await req.json()).password;
          if (password === "locked out") return new Response(JSON.stringify({ code: "local_login_rate_limited", message: "" }), { status: 429, headers: { "Retry-After": "540" } });
          return password === "correct horse battery" ? json(201, session()) : json(401, { code: "invalid_credentials", message: "" });
        case "GET /v0/cameras": return json(200, [{ id: "cam_1", name: "Door", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: {}, created_at: "", updated_at: "" }]);
        case "GET /v0/health/cameras": return json(200, [{ camera: "cam_1", enabled: true, online: true, streams: {} }]);
        case "POST /v0/cameras/cam_1/live/offer": return json(201, { sdp: "v=0 answer", session: "http://e/whep/1" });
        default: return json(404, { code: "not_found", message: "" });
      }
    }) as typeof fetch);
    cacheSet("me", me);
    cacheSet("hives", { hives: [hive], shared: [] });
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();

    const form = await screen.findByTestId("local-sign-in", {}, { timeout: 5000 }); // after the cloud call fails and the LAN probe answers
    await user.type(within(form).getByLabelText("Local username"), "anna");
    await user.type(within(form).getByLabelText("Local password"), "wrong password!");
    await user.click(within(form).getByRole("button", { name: "Sign in locally" }));
    expect(await within(form).findByRole("alert")).toHaveTextContent("Wrong username or password");
    await user.clear(within(form).getByLabelText("Local password"));
    await user.type(within(form).getByLabelText("Local password"), "locked out");
    await user.click(within(form).getByRole("button", { name: "Sign in locally" }));
    await waitFor(() => expect(within(form).getByRole("alert")).toHaveTextContent("Try again in 9 minutes"));

    await user.clear(within(form).getByLabelText("Local password"));
    await user.type(within(form).getByLabelText("Local password"), "correct horse battery");
    await user.click(within(form).getByRole("button", { name: "Sign in locally" }));

    expect(await screen.findByTestId("local-session")).toHaveTextContent("Signed in locally");
    expect(screen.queryByTestId("local-sign-in")).toBeNull();
    const tile = await screen.findByTestId("tile");
    await waitFor(() => expect(tile.dataset.state).toBe("connected"));
    expect(calls).toContain(`GET ${LAN}/v0/cameras Bearer v4.local.loc-tok`);
    expect(calls).toContain(`POST ${LAN}/v0/cameras/cam_1/live/offer Bearer v4.local.loc-tok`);
    expect(calls.some((c) => c.includes("relay"))).toBe(false);

    await user.click(screen.getByRole("button", { name: "Sign out locally" }));
    expect(await screen.findByTestId("local-sign-in")).toBeInTheDocument();
    expect(readLocal("hv_1")).toBeNull();
  }, 15_000);

  it("does not offer the local sign-in when the LAN name does not answer", async () => {
    vi.stubGlobal("fetch", (async () => { throw new TypeError("Failed to fetch"); }) as typeof fetch);
    cacheSet("me", me);
    cacheSet("hives", { hives: [hive], shared: [] });
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    expect(await screen.findByTestId("cloud-down")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByTestId("local-sign-in")).toBeNull();
  });
});

describe("the hive page with no DNS and no cloud (ADR-0023)", () => {
  /** The hive at its address; the browser refuses it until the user proceeded through the certificate warning. */
  function hiveAtAddress(calls: string[], accepted: () => boolean) {
    return (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const u = new URL(req.url);
      calls.push(`${req.method} ${u.host}${u.pathname} ${req.headers.get("Authorization") ?? ""}`);
      if (u.host !== ADDRESS || !accepted()) throw new TypeError("Failed to fetch"); // no DNS for the cloud or the LAN name; the address needs the exception
      switch (`${req.method} ${u.pathname}`) {
        case "GET /healthz": return json(200, {});
        case "POST /v0/local-login/sessions": return json(201, session());
        case "GET /v0/cameras": return json(200, [{ id: "cam_1", name: "Door", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: {}, created_at: "", updated_at: "" }]);
        case "GET /v0/health/cameras": return json(200, [{ camera: "cam_1", enabled: true, online: true, streams: {} }]);
        case "POST /v0/cameras/cam_1/live/offer": return json(201, { sdp: "v=0 answer", session: "http://e/whep/1" });
        default: return json(404, { code: "not_found", message: "" });
      }
    }) as typeof fetch;
  }

  function renderHive(h: typeof hive = hive) {
    cacheSet("me", me);
    cacheSet("hives", { hives: [h], shared: [] });
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  }

  it("sends the browser to the hive's last address with the check, then signs in and plays live there", async () => {
    const calls: string[] = [];
    let accepted = false;
    vi.stubGlobal("RTCPeerConnection", FakePC);
    vi.stubGlobal("fetch", hiveAtAddress(calls, () => accepted));
    renderHive();
    const user = userEvent.setup();

    const step = await screen.findByTestId("lan-address", {}, { timeout: 5000 });
    expect(within(step).getByRole("link", { name: /192\.168\.1\.20/ })).toHaveAttribute("href", `https://${ADDRESS}/`);
    const checks = within(step).getByTestId("lan-address-checks");
    expect(checks).toHaveTextContent("NET::ERR_CERT_COMMON_NAME_INVALID");
    expect(checks).toHaveTextContent("This server could not prove that it is 192.168.1.20; its security certificate is from hv-1.h.camhive.app.");
    expect(checks).toHaveTextContent("Proceed to 192.168.1.20 (unsafe)");
    expect(checks).toHaveTextContent("SSL_ERROR_BAD_CERT_DOMAIN");
    expect(checks).toHaveTextContent("issued to hv-1.h.camhive.app by Let’s Encrypt");
    expect(step).toHaveTextContent("Any other warning (“not trusted”, “self-signed”, an unknown issuer or any other name) means stop");
    expect(screen.queryByTestId("local-sign-in")).toBeNull();

    accepted = true; // the user opened the link, proceeded through the warning and comes back to this tab
    document.dispatchEvent(new Event("visibilitychange"));
    const form = await screen.findByTestId("local-sign-in");
    expect(screen.queryByTestId("lan-address")).toBeNull();
    await user.type(within(form).getByLabelText("Local username"), "anna");
    await user.type(within(form).getByLabelText("Local password"), "correct horse battery");
    await user.click(within(form).getByRole("button", { name: "Sign in locally" }));

    const tile = await screen.findByTestId("tile");
    await waitFor(() => expect(tile.dataset.state).toBe("connected"));
    expect(calls).toContain(`POST ${ADDRESS}/v0/local-login/sessions `);
    expect(calls).toContain(`POST ${ADDRESS}/v0/cameras/cam_1/live/offer Bearer v4.local.loc-tok`);
  }, 15_000);

  it("takes a new address the owner typed after a DHCP change", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", hiveAtAddress(calls, () => true));
    renderHive({ ...hive, lan: { ...hive.lan, addrs: ["192.168.1.99"] } }); // the hive moved during the outage
    const user = userEvent.setup();
    const step = await screen.findByTestId("lan-address", {}, { timeout: 5000 });
    const field = within(step).getByLabelText("Hive address");
    await user.clear(field);
    await user.type(field, "8.8.8.8");
    await user.click(within(step).getByRole("button", { name: "Try again" }));
    expect(await within(step).findByRole("alert")).toHaveTextContent("192.168.1.23");
    await user.clear(field);
    await user.type(field, "192.168.1.20");
    expect(within(step).getByRole("link", { name: /192\.168\.1\.20/ })).toHaveAttribute("href", `https://${ADDRESS}/`);
    await user.click(within(step).getByRole("button", { name: "Try again" }));
    expect(await screen.findByTestId("local-sign-in")).toBeInTheDocument();
    expect(calls.some((c) => c.startsWith("GET 8.8.8.8"))).toBe(false);
  }, 15_000);

  it("is never a shortcut: with the cloud reaching the hive the card stays away although the name fails", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const u = new URL(req.url);
      const route = `${req.method} ${u.pathname}`;
      calls.push(`${req.method} ${u.host}${u.pathname}`);
      if (u.host === LAN || u.host === ADDRESS) throw new TypeError("Failed to fetch"); // router rebinding protection; no exception for the address
      if (u.host === "hv_1.relay.camhive.app") return json(200, []);
      switch (route) {
        case "GET /v1/me": return json(200, me);
        case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
        case "POST /v1/hives/hv_1/access": return json(201, { token: "on-tok", session_id: "s_on", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "owner", cameras: ["*"], hive, quality: "full", ice_servers: [] });
        default: return json(200, []);
      }
    }) as typeof fetch);
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    await waitFor(() => expect(calls).toContain("GET hv_1.relay.camhive.app/v0/cameras"), { timeout: 5000 });
    expect(calls).toContain(`GET ${ADDRESS}/healthz`); // probed after the name failed, silently
    expect(screen.queryByTestId("lan-address")).toBeNull();
    expect(screen.queryByTestId("local-sign-in")).toBeNull();
  }, 15_000);

  it("offers no address for a hive whose certificate is self-signed", async () => {
    vi.stubGlobal("fetch", hiveAtAddress([], () => true));
    renderHive({ ...hive, lan: { ...hive.lan, cert_issuer: "self-signed" as unknown as "letsencrypt" } });
    expect(await screen.findByTestId("cloud-down")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 100));
    expect(screen.queryByTestId("lan-address")).toBeNull();
    expect(screen.queryByTestId("local-sign-in")).toBeNull();
  });
});

describe("local login settings", () => {
  it("lets the owner switch the local login on with a password and off again", async () => {
    let state: { enabled: boolean; username?: string; updated_at?: string } = { enabled: false };
    const puts: unknown[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const u = new URL(req.url);
      const route = `${req.method} ${u.pathname}`;
      if (u.host === LAN) {
        switch (route) {
          case "GET /healthz": return json(200, {});
          case "GET /v0/local-login": return json(200, state);
          case "PUT /v0/local-login": {
            const body = await req.json();
            puts.push(body);
            if (body.password.length < 10) return json(422, { code: "weak_password", message: "" });
            state = { enabled: true, username: body.username, updated_at: "2026-11-16T10:00:00Z" };
            return json(200, state);
          }
          case "DELETE /v0/local-login": state = { enabled: false }; return new Response(null, { status: 204 });
          default: return json(404, { code: "not_found", message: "" });
        }
      }
      switch (route) {
        case "GET /v1/me": return json(200, me);
        case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
        case "POST /v1/hives/hv_1/access": return json(201, { token: "on-tok", session_id: "s_on", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "owner", cameras: ["*"], hive, quality: "full", ice_servers: [] });
        default: return json(404, { error: { code: "not_found", message: "" } });
      }
    }) as typeof fetch);
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/hives/hv_1/settings"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();

    const section = await screen.findByTestId("local-login-settings");
    await user.click(within(section).getByRole("button", { name: "Set up local sign-in" }));
    expect(await within(section).findByText("Off")).toBeInTheDocument();
    await user.type(within(section).getByLabelText("Local username"), "anna");
    await user.type(within(section).getByLabelText("Local password"), "short");
    await user.click(within(section).getByRole("button", { name: "Save local sign-in" }));
    expect(await within(section).findByRole("alert")).toHaveTextContent("10–256");

    await user.clear(within(section).getByLabelText("Local password"));
    await user.type(within(section).getByLabelText("Local password"), "correct horse battery");
    await user.click(within(section).getByRole("button", { name: "Save local sign-in" }));
    expect(await within(section).findByText(/On for anna/)).toBeInTheDocument();
    expect(puts.at(-1)).toEqual({ username: "anna", password: "correct horse battery" });

    await user.click(within(section).getByRole("button", { name: "Turn off local sign-in" }));
    expect(await within(section).findByText("Off")).toBeInTheDocument();
  }, 15_000);
});
