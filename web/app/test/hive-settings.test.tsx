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
afterEach(async () => { cleanup(); await new Promise((r) => setTimeout(r, 30)); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const CHROME = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36";
const FIREFOX = "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0";
const GB = 1e9;

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.3.0",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" }, relay_host: "hv_1.relay.camhive.app" };
const current = { ...hive, version: "0.1.10", capabilities: ["archive.autolock", "archive.delete", "archive.encryption", "disk.health", "local_login", "storage", "updates"] };

/** What a v0.1.10 hive answers on the LAN. */
const hiveAnswers: Record<string, unknown> = {
  "/healthz": { status: "ok" },
  "/v0/storage": {
    cameras: [{ id: "cam_a", name: "Door", bytes: 110 * GB, days_held: 13.6 }],
    disk: { total_bytes: 1000 * GB, archive_bytes: 110 * GB, export_bytes: 0, free_bytes: 800 * GB, health: { state: "ok", evidence: [], device: "/dev/sdb" } },
    settings: { retention_days: 60, min_free_gb: 10, export_max_gb: 10 },
    forecast: { bytes_per_day: 8 * GB, capacity_bytes: 790 * GB, disk_days: 98, fills_in_days: 40, retention_met: false, deleting_for_space: false },
  },
  "/v0/archive/encryption": { state: "locked", sealing: true },
  "/v0/hive/updates": { updates: "automatic", update_window: "02:00-05:00", update_channel: "stable", running: "0.1.10", supported: true },
  "/v0/local-login": { enabled: false },
  "/v0/hive/move": { state: "none", supported: true, files: [] },
};

type Stub = { hive: Record<string, unknown>; role?: "owner" | "viewer"; lanUp?: () => boolean; extra?: (key: string) => Response | undefined; answers?: Record<string, unknown> };

function stub({ hive: h, role = "owner", lanUp = () => true, extra, answers = {} }: Stub) {
  const calls: string[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const url = new URL(req.url);
    const key = `${req.method} ${url.pathname}`;
    calls.push(url.host === "hv_1.h.camhive.app:16017" ? `hive ${key}` : key);
    if (url.host === "hv_1.h.camhive.app:16017") {
      if (!lanUp()) throw new TypeError("Failed to fetch");
      const body = { ...hiveAnswers, ...answers }[url.pathname];
      if (body instanceof Response) return body.clone();
      return body ? json(200, body) : json(404, { code: "not_found", message: "" });
    }
    const special = extra?.(key);
    if (special) return special;
    switch (key) {
      case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" });
      case "GET /v1/hives": return json(200, role === "owner" ? { hives: [h], shared: [] } : { hives: [], shared: [h] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role, cameras: ["*"], hive: h, quality: "full", ice_servers: [] });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount(path = "/hives/hv_1/settings", queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={queryClient} />);
  return { user: userEvent.setup(), router };
}

const userAgent = (ua: string) => vi.spyOn(navigator, "userAgent", "get").mockReturnValue(ua);

describe("the hive's Settings tab (#174)", () => {
  it("shows the relay with the router hint when the LAN name fails, then the direct path and Chrome's note after a re-probe", async () => {
    userAgent(CHROME);
    let lanUp = false;
    const calls = stub({ hive, lanUp: () => lanUp, extra: (k) => (k === "DELETE /v1/hives/hv_1" ? new Response(null, { status: 204 }) : undefined) });
    const { user, router } = mount("/");
    await user.click(await screen.findByRole("link", { name: "Settings for Home" }));

    expect(await screen.findByRole("heading", { name: "Home" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("aria-current", "page");
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("Through the Camhive relay"));
    expect(screen.getByTestId("path-row-state")).toHaveTextContent("The local name did not answer; using the relay.");
    expect(screen.getByTestId("rebind-hint")).toHaveTextContent("h.camhive.app");
    expect(screen.queryByTestId("lna-hint")).toBeNull(); // nothing to allow on the relay
    const details = screen.getByTestId("connection-details");
    expect(details).not.toHaveAttribute("open");
    expect(within(details).getByText("https://hv_1.relay.camhive.app")).toBeInTheDocument();
    expect(within(details).getByText("Let's Encrypt")).toBeInTheDocument();
    expect(screen.queryByText(/returned an object/)).toBeNull();

    lanUp = true;
    await user.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("Direct on your network"));
    expect(screen.queryByTestId("rebind-hint")).toBeNull();
    expect(screen.getByTestId("lna-hint")).toHaveTextContent(/Chrome asks once/);

    // The owner removes the hive after confirming; the list is refreshed and the app returns to the hives.
    const danger = screen.getByRole("region", { name: "Danger zone" });
    vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await user.click(within(danger).getByRole("button", { name: "Remove hive" }));
    expect(calls.filter((c) => c === "DELETE /v1/hives/hv_1")).toHaveLength(0);
    await user.click(within(danger).getByRole("button", { name: "Remove hive" }));
    await waitFor(() => expect(calls).toContain("DELETE /v1/hives/hv_1"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("shows no Chrome note in other browsers, and no router hint when the hive reports no addresses", async () => {
    userAgent(FIREFOX);
    stub({ hive: { ...hive, lan: { ...hive.lan, addrs: [] } }, lanUp: () => false });
    mount();
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("Through the Camhive relay"));
    expect(screen.queryByTestId("rebind-hint")).toBeNull();
    cleanup();

    stub({ hive });
    mount();
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("Direct on your network"));
    expect(screen.queryByTestId("lna-hint")).toBeNull();
  });

  it("puts the six states of a v0.1.10 hive in the strip, and every row's state before its action", async () => {
    stub({ hive: current });
    mount();
    const strip = await screen.findByTestId("settings-strip");
    await waitFor(() => expect(within(strip).getByTestId("strip-updates")).toHaveTextContent("Updatesautomatic"));
    expect(within(strip).getByTestId("strip-version")).toHaveTextContent("Version0.1.10");
    expect(within(strip).getByTestId("strip-path")).toHaveTextContent("Pathdirect");
    expect(within(strip).getByTestId("strip-disk")).toHaveTextContent("Diskhealthy");
    expect(within(strip).getByTestId("strip-footage")).toHaveTextContent("Footage110 GB over 13 days, full in about 40 days");
    expect(within(strip).getByTestId("strip-encryption")).toHaveTextContent("Encryptionon, locked");
    expect(within(strip).queryByRole("button")).toBeNull();
    expect(screen.getByRole("region", { name: "This hive at a glance" })).toContainElement(strip);

    expect(screen.getByTestId("storage-row-state")).toHaveTextContent("110 GB over 13 days, full in about 40 days");
    expect(screen.getByTestId("disk-health-row-state")).toHaveTextContent("Healthy · /dev/sdb");
    expect(screen.getByTestId("encryption-row-state")).toHaveTextContent("On, locked");
    expect(screen.getByTestId("move-data-row-state")).toHaveTextContent("Possible on this hive");
    expect(screen.getByTestId("local-login-row-state")).toHaveTextContent("Off");
    expect(screen.getByTestId("updates-row-state")).toHaveTextContent("0.1.10, automatic, window 02:00–05:00");
    // The id is shown as it is: no text and no style capitalises it (review of #174).
    const id = screen.getByTestId("hive-id-row-state");
    expect(id.textContent).toBe("hv_1");
    for (let el: HTMLElement | null = id; el && el.tagName !== "SECTION"; el = el.parentElement) expect(el.className).not.toMatch(/uppercase|capitalize|first-letter/);
  });

  it("says what an older hive does not report and which version brings it, without asking it", async () => {
    const calls = stub({ hive: { ...hive, version: "0.1.6", capabilities: undefined } });
    mount();
    const strip = await screen.findByTestId("settings-strip");
    expect(within(strip).getByTestId("strip-version")).toHaveTextContent("0.1.6");
    expect(within(strip).getByTestId("strip-disk")).toHaveTextContent("not reported by this hive (needs 0.1.10)");
    expect(within(strip).getByTestId("strip-footage")).toHaveTextContent("not reported by this hive (needs 0.1.10)");
    expect(within(strip).getByTestId("strip-encryption")).toHaveTextContent("not reported by this hive (needs 0.1.7)");
    expect(within(strip).getByTestId("strip-updates")).toHaveTextContent("not reported by this hive (needs 0.1.7)");
    expect(screen.getByTestId("move-data-row-state")).toHaveTextContent("Not reported by this hive (needs 0.1.10)");
    expect(within(screen.getByRole("region", { name: "Updates" })).getByTestId("needs-update")).toHaveTextContent("0.1.7");
    await waitFor(() => expect(calls).toContain("hive GET /v0/local-login"));
    for (const path of ["/v0/storage", "/v0/archive/encryption", "/v0/hive/updates", "/v0/hive/move"]) expect(calls).not.toContain(`hive GET ${path}`);
  });

  it("orders the groups and the rows for the owner, and shows a member only what is theirs", async () => {
    stub({ hive: current });
    mount();
    await screen.findByTestId("settings-strip");
    const groups = () => screen.getAllByTestId("settings-group").map((g) => within(g).getAllByRole("heading", { level: 2 })[0]!.textContent);
    expect(groups()).toEqual(["Connection", "Recordings and disk", "Access", "Software", "Danger zone"]);
    const rows = within(screen.getByRole("region", { name: "Recordings and disk" })).getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(rows).toEqual(["Storage", "Disk health", "Encrypted recordings", "Delete recordings", "Move to a new disk"]);
    const software = within(screen.getByRole("region", { name: "Software" })).getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(software).toEqual(["Updates", "Running version", "Hive id"]);
    expect(within(screen.getByRole("navigation", { name: "On this page" })).getAllByRole("link").map((a) => a.textContent))
      .toEqual(["Connection", "Recordings and disk", "Access", "Software", "Danger zone"]);
    cleanup();

    stub({ hive: current, role: "viewer" });
    mount();
    const strip = await screen.findByTestId("settings-strip");
    expect(groups()).toEqual(["Connection", "Recordings and disk", "Software"]);
    expect(within(strip).queryByTestId("strip-disk")).toBeNull();
    expect(within(screen.getByRole("region", { name: "Recordings and disk" })).getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual(["Storage"]);
  });

  it("shows owner deletion disabled with the update notice on a hive without archive.delete (ADR-0022)", async () => {
    const render2 = async (h: Record<string, unknown>) => {
      stub({ hive: h, lanUp: () => false });
      mount();
      return screen.findByRole("region", { name: "Delete recordings" });
    };

    const old = await render2({ ...hive, version: "0.1.6", capabilities: ["camera.identity", "motion"] });
    expect(within(old).getByRole("note")).toHaveTextContent("This needs hive version 0.1.3 or later; this hive runs 0.1.6.");
    expect(within(old).getByRole("button", { name: "Delete recordings…" })).toBeDisabled();
    cleanup();

    const now = await render2({ ...hive, version: "0.1.6", capabilities: ["archive.delete"] });
    expect(within(now).queryByRole("note")).toBeNull();
    expect(within(now).getByRole("button", { name: "Delete recordings…" })).toBeEnabled();
  });

  it("scrolls to a row's anchor once the page has data, on a cold load", async () => {
    const scrolled: string[] = [];
    Element.prototype.scrollIntoView = vi.fn(function (this: Element) { scrolled.push(this.id); });
    stub({ hive: current });
    mount("/hives/hv_1/settings#settings-move-data");
    await screen.findByTestId("move-data");
    expect(document.getElementById("settings-move-data")).toBe(screen.getByTestId("move-data"));
    await waitFor(() => expect(scrolled).toContain("settings-move-data"));
  });

  it("asks a hive that does not answer once per endpoint and says so under the strip", async () => {
    const calls = stub({ hive: current, answers: { "/v0/storage": json(500, { code: "internal", message: "" }) } });
    mount("/hives/hv_1/settings", new QueryClient()); // the app's own retry defaults
    const strip = await screen.findByTestId("settings-strip");
    await waitFor(() => expect(within(strip).getByTestId("strip-footage")).toHaveTextContent("not known right now"), { timeout: 3000 });
    expect(screen.getByTestId("hive-no-answer")).toHaveTextContent("The hive did not answer");
    await new Promise((r) => setTimeout(r, 1300)); // longer than the first retry's delay
    expect(calls.filter((c) => c === "hive GET /v0/storage")).toHaveLength(1);
  });

  it("says the hive is offline in the strip when the cloud reports it so", async () => {
    stub({ hive: { ...current, online: false }, lanUp: () => false });
    mount();
    const strip = await screen.findByTestId("settings-strip");
    for (const id of ["disk", "footage", "encryption", "updates"]) expect(within(strip).getByTestId(`strip-${id}`)).toHaveTextContent("hive offline");
    expect(screen.queryByTestId("hive-no-answer")).toBeNull();
  });

  it("ties each row's button to its panel, keeps the heading levels, and names what a set-up sign-in offers", async () => {
    stub({ hive: current, answers: { "/v0/local-login": { enabled: true, username: "anna", updated_at: "2026-11-16T10:00:00Z" } } });
    const { user } = mount();
    const login = await screen.findByRole("region", { name: "Local sign-in for emergencies" });
    const manage = await within(login).findByRole("button", { name: "Manage local sign-in" });
    expect(manage).toHaveAttribute("aria-expanded", "false");
    const storage = screen.getByRole("region", { name: "Storage" });
    const show = within(storage).getByRole("button", { name: "Show storage…" });
    const panel = document.getElementById(show.getAttribute("aria-controls") ?? "");
    expect(panel).not.toBeNull();
    expect(storage).toContainElement(panel);
    await user.click(show);
    expect(show).toHaveAttribute("aria-expanded", "true");
    expect(await within(panel!).findByRole("heading", { name: "Settings", level: 4 })).toBeInTheDocument();
    // The removal text names the command as code, not between backticks.
    const danger = screen.getByRole("region", { name: "Danger zone" });
    expect(within(danger).getByText("hive claim").tagName).toBe("CODE");
    expect(danger.textContent).not.toContain("`");
  });
});
