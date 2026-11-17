import { describe, expect, it, vi } from "vitest";
import { Transport, TransportError, atHome, lanBase, openableByAddress, parseLanAddress, probeLAN, rebindHintApplies, relayBase, type Decision } from "../src/lib/transport";
import type { Hive } from "../src/lib/hives";
import { cacheClear } from "../src/lib/cache";

const hive: Hive = {
  id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" },
  relay_host: "hv_1.relay.camhive.app",
};
const ok = () => Promise.resolve(new Response("{}", { status: 200 }));
const refuse = () => Promise.reject(new TypeError("Failed to fetch"));

function memStorage() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k), map: m };
}

describe("bases", () => {
  it("builds LAN and relay base URLs only from complete data", () => {
    expect(lanBase(hive)).toBe("https://hv_1.h.camhive.app:16017");
    expect(relayBase(hive)).toBe("https://hv_1.relay.camhive.app");
    expect(lanBase({ lan: { host: "x", port: 1, addrs: [] } })).toBeNull();
    expect(lanBase({ lan: { host: "x", addrs: ["1.2.3.4"] } })).toBeNull();
    expect(relayBase({})).toBeNull();
  });
});

describe("probeLAN", () => {
  it("is true on 2xx, false on error status, network error and timeout", async () => {
    expect(await probeLAN("https://h", { fetch: ok })).toBe(true);
    expect(await probeLAN("https://h", { fetch: () => Promise.resolve(new Response("", { status: 503 })) })).toBe(false);
    expect(await probeLAN("https://h", { fetch: refuse })).toBe(false);
    const hang = (_: unknown, init?: RequestInit) => new Promise<Response>((_r, rej) => init?.signal?.addEventListener("abort", () => rej(new DOMException("aborted", "AbortError"))));
    const t0 = Date.now();
    expect(await probeLAN("https://h", { fetch: hang, timeoutMs: 50 })).toBe(false);
    expect(Date.now() - t0).toBeLessThan(1000);
  });
});

describe("Transport.select", () => {
  it("prefers the LAN when the probe answers and remembers it in storage", async () => {
    const fetch = vi.fn(ok);
    const storage = memStorage();
    const t = new Transport({ fetch, storage, now: () => 1000 });
    const d = await t.select(hive);
    expect(d).toEqual({ path: "lan", reason: "lan_ok", at: 1000 });
    expect(fetch).toHaveBeenCalledWith("https://hv_1.h.camhive.app:16017/healthz", expect.objectContaining({ cache: "no-store" }));
    await t.select(hive);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(JSON.parse(storage.map.get("camhive.transport.hv_1")!)).toEqual(d);
    // A fresh instance (page reload) reads the decision back without probing.
    const t2 = new Transport({ fetch, storage, now: () => 2000 });
    expect(t2.remembered("hv_1")).toEqual(d);
  });

  it("falls back to the relay when the LAN refuses, and reports why", async () => {
    const t = new Transport({ fetch: refuse, now: () => 5 });
    expect(await t.select(hive)).toEqual({ path: "relay", reason: "lan_failed", at: 5 });
    expect(rebindHintApplies(hive, t.remembered("hv_1"))).toBe(true);
  });

  it("explains missing LAN data and missing relay", async () => {
    const t = new Transport({ fetch: refuse });
    const noLan: Hive = { ...hive, lan: { host: hive.lan!.host, port: 16017, addrs: [] } };
    expect((await t.select(noLan)).reason).toBe("lan_unavailable");
    expect(rebindHintApplies(noLan, t.remembered("hv_1"))).toBe(false);
    const nothing: Hive = { ...noLan, id: "hv_2", relay_host: undefined };
    expect(await t.select(nothing)).toMatchObject({ path: "none", reason: "lan_unavailable" });
    const lanOnly: Hive = { ...hive, id: "hv_3", relay_host: undefined };
    expect(await t.select(lanOnly)).toMatchObject({ path: "none", reason: "relay_unavailable" });
  });

  it("expires decisions, dedupes concurrent probes and re-probes on force or network change", async () => {
    let now = 0;
    const fetch = vi.fn(ok);
    const t = new Transport({ fetch, now: () => now, ttlMs: 100 });
    await Promise.all([t.select(hive), t.select(hive)]);
    expect(fetch).toHaveBeenCalledTimes(1);
    now = 150;
    await t.select(hive);
    expect(fetch).toHaveBeenCalledTimes(2);
    await t.select(hive, { force: true });
    expect(fetch).toHaveBeenCalledTimes(3);
    const target = new EventTarget();
    const detach = t.attach(target);
    target.dispatchEvent(new Event("online"));
    expect(t.remembered("hv_1")).toBeNull();
    detach();
    await t.select(hive);
    target.dispatchEvent(new Event("online"));
    expect(t.remembered("hv_1")).not.toBeNull();
  });

  it("notifies subscribers of every decision", async () => {
    const t = new Transport({ fetch: ok });
    const seen: Decision[] = [];
    const off = t.subscribe((_id, d) => seen.push(d));
    await t.select(hive);
    off();
    await t.select(hive, { force: true });
    expect(seen).toHaveLength(1);
  });
});

describe("Transport.fetch", () => {
  it("adds the bearer token and uses the decided base", async () => {
    const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      if (String(url).endsWith("/healthz")) return new Response("{}");
      expect(String(url)).toBe("https://hv_1.h.camhive.app:16017/v0/cameras");
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer tok");
      return new Response("[]", { status: 200 });
    });
    const t = new Transport({ fetch });
    const res = await t.fetch(hive, "/v0/cameras", { token: "tok" });
    expect(res.status).toBe(200);
  });

  it("demotes to the relay when a LAN request dies mid-session and retries there once", async () => {
    let lanAlive = true;
    const fetch = vi.fn(async (url: RequestInfo | URL) => {
      const u = String(url);
      if (u.startsWith("https://hv_1.h.")) {
        if (lanAlive) return new Response("{}");
        throw new TypeError("Failed to fetch");
      }
      return new Response("relayed", { status: 200 });
    });
    const t = new Transport({ fetch });
    await t.select(hive);
    lanAlive = false;
    const res = await t.fetch(hive, "/v0/cameras", { token: "tok" });
    expect(await res.text()).toBe("relayed");
    expect(t.remembered("hv_1")).toMatchObject({ path: "relay", reason: "lan_failed" });
    // HTTP errors are not transport failures: they come back as they are.
    const t2 = new Transport({ fetch: async (u: RequestInfo | URL) => new Response("", { status: String(u).endsWith("/healthz") ? 200 : 401 }) });
    expect((await t2.fetch(hive, "/v0/cameras", { token: "bad" })).status).toBe(401);
  });

  it("never sends a local session's token through the relay (#57)", async () => {
    const urls: string[] = [];
    let lanAlive = true;
    const fetch = vi.fn(async (url: RequestInfo | URL) => {
      const u = String(url);
      urls.push(u);
      if (u.startsWith("https://hv_1.h.") && lanAlive) return new Response("{}");
      if (u.startsWith("https://hv_1.h.")) throw new TypeError("Failed to fetch");
      return new Response("relayed");
    });
    const t = new Transport({ fetch });
    await t.select(hive);
    lanAlive = false;
    await expect(t.fetch(hive, "/v0/cameras", { token: "v4.local.abc" })).rejects.toBeInstanceOf(TypeError); // no retry on the relay
    const relay = new Transport({ fetch: vi.fn(async (u: RequestInfo | URL) => String(u).endsWith("/healthz") ? Promise.reject(new TypeError("x")) : new Response("relayed")) });
    await expect(relay.fetch(hive, "/v0/cameras", { token: "v4.local.abc" })).rejects.toBeInstanceOf(TransportError);
    expect(urls.some((u) => u.includes("relay"))).toBe(false);
  });

  it("throws TransportError when no path exists, and rethrows relay failures", async () => {
    const t = new Transport({ fetch: refuse });
    const nothing: Hive = { ...hive, id: "hv_none", lan: undefined, relay_host: undefined };
    await expect(t.fetch(nothing, "/v0/cameras", { token: "t" })).rejects.toBeInstanceOf(TransportError);
    await expect(t.fetch(hive, "/v0/cameras", { token: "t" })).rejects.toBeInstanceOf(TypeError);
  });
});

describe("atHome", () => {
  it("is true on the LAN path or in local mode, false through the relay or with no decision yet", () => {
    const d = (path: "lan" | "relay" | "none"): Decision => ({ path, reason: "lan_ok", at: 0 });
    expect(atHome(false, d("lan"))).toBe(true);
    expect(atHome(false, { path: "address", reason: "address_ok", at: 0, address: "192.168.1.20:16017" })).toBe(true);
    expect(atHome(false, d("relay"))).toBe(false);
    expect(atHome(false, d("none"))).toBe(false);
    expect(atHome(false, null)).toBe(false);
    expect(atHome(true, null)).toBe(true);
    expect(atHome(true, d("relay"))).toBe(true);
  });
});

describe("the emergency path by address (ADR-0023)", () => {
  const answering = (...hosts: string[]) => vi.fn((input: RequestInfo | URL) => (hosts.includes(new URL(String(input)).host) ? ok() : refuse()));

  it("uses the remembered LAN address when the name does not resolve, and keeps local tokens there", async () => {
    const fetch = answering("192.168.1.20:16017");
    const t = new Transport({ fetch, now: () => 7 });
    const d = await t.select(hive);
    expect(d).toEqual({ path: "address", reason: "address_ok", at: 7, address: "192.168.1.20:16017" });
    expect(t.baseFor(hive, d)).toBe("https://192.168.1.20:16017");
    expect(atHome(false, d)).toBe(true);
    await t.fetch(hive, "/v0/cameras", { token: "v4.local.loc-tok" });
    expect(fetch).toHaveBeenLastCalledWith("https://192.168.1.20:16017/v0/cameras", expect.objectContaining({ mode: "cors" }));
  });

  it("never probes an address while the name answers", async () => {
    const fetch = answering("hv_1.h.camhive.app:16017", "192.168.1.20:16017");
    const t = new Transport({ fetch, now: () => 1 });
    expect(await t.select(hive)).toEqual({ path: "lan", reason: "lan_ok", at: 1 });
    expect(fetch.mock.calls.map((c) => String(c[0]))).toEqual(["https://hv_1.h.camhive.app:16017/healthz"]);
  });

  it("gives up on an address that hangs after the same short timeout", async () => {
    const hang = (input: RequestInfo | URL, init?: RequestInit) => new URL(String(input)).host === "192.168.1.20:16017"
      ? new Promise<Response>((_r, rej) => init?.signal?.addEventListener("abort", () => rej(new DOMException("aborted", "AbortError"))))
      : refuse();
    const t = new Transport({ fetch: hang, timeoutMs: 50, now: () => 1 });
    const t0 = Date.now();
    expect(await t.select(hive)).toEqual({ path: "relay", reason: "lan_failed", at: 1 });
    expect(Date.now() - t0).toBeLessThan(1000);
  });

  it("tries addresses only for a publicly issued certificate: any other warning at an address could be forged", async () => {
    for (const cert_issuer of ["self-signed", "camhive", "", undefined] as const) {
      const fetch = answering("192.168.1.20:16017");
      const t = new Transport({ fetch, now: () => 1 });
      const h = { ...hive, lan: { ...hive.lan!, cert_issuer: cert_issuer as "letsencrypt" | undefined } };
      expect(openableByAddress(h), String(cert_issuer)).toBe(false);
      expect(await t.select(h), String(cert_issuer)).toEqual({ path: "relay", reason: "lan_failed", at: 1 });
      expect(fetch.mock.calls.map((c) => String(c[0]))).not.toContain("https://192.168.1.20:16017/healthz");
    }
    expect(openableByAddress(hive)).toBe(true);
  });

  it("tries an address the owner typed first, and keeps it across reloads", async () => {
    const keep = memStorage();
    const fetch = answering("192.168.1.31:16017", "192.168.1.20:16017");
    const t = new Transport({ fetch, keep, now: () => 1 });
    expect(t.setAddress(hive, "not an address")).toBeNull();
    expect(t.setAddress(hive, "192.168.1.31")).toBe("192.168.1.31:16017");
    expect(await t.select(hive)).toMatchObject({ path: "address", address: "192.168.1.31:16017" });
    expect(new Transport({ keep }).addresses(hive)).toEqual(["192.168.1.31:16017", "192.168.1.20:16017"]);
  });

  it("forgets a typed address on sign-out, with the rest of the account's cached data", () => {
    const t = new Transport({ keep: localStorage });
    expect(t.setAddress(hive, "192.168.1.31")).toBe("192.168.1.31:16017");
    expect(new Transport({ keep: localStorage }).addresses(hive)[0]).toBe("192.168.1.31:16017");
    cacheClear();
    expect(new Transport({ keep: localStorage }).addresses(hive)).toEqual(["192.168.1.20:16017"]);
  });

  it("accepts only private address literals, as typed or as `hive status` prints them", () => {
    expect(parseLanAddress("192.168.1.23", 16443)).toBe("192.168.1.23:16443");
    expect(parseLanAddress("169.254.10.20", 16443)).toBe("169.254.10.20:16443"); // IPv4 link-local
    expect(parseLanAddress(" https://192.168.1.23:16017/ ", 16443)).toBe("192.168.1.23:16017");
    expect(parseLanAddress("10.0.0.7:1", 16443)).toBe("10.0.0.7:1");
    expect(parseLanAddress("172.16.4.2", 16443)).toBe("172.16.4.2:16443");
    expect(parseLanAddress("fd00::5", 16443)).toBe("[fd00::5]:16443");
    expect(parseLanAddress("lan: https://[fd00::5]:16017", 16443)).toBe("[fd00::5]:16017");
    const refused = [
      "", "8.8.8.8", "172.32.0.1", "100.64.0.1", "2001:db8::1", // public or not private
      "127.0.0.1", "::1", "[::1]:16443", "0.0.0.0", "::", // loopback and unspecified
      "fe80::1", "[fe80::1]:16443", // IPv6 link-local: a browser cannot open it without a zone
      "hive.local", "hv-1.h.camhive.app", "localhost", // names need DNS or mDNS
      "010.1.2.3", "172.016.0.1", "192.168.001.010", "0x0a.1.2.3", // a leading zero is octal to the URL parser: 010.1.2.3 opens 8.1.2.3
      "192.168.1", "192.168.1.300", "192.168.1.23:0", "192.168.1.23:70000", "http://192.168.1.23", "192.168.1.23/x", "user@192.168.1.23",
    ];
    for (const bad of refused) {
      expect(parseLanAddress(bad, 16443), bad).toBeNull();
    }
  });
});
