import { describe, expect, it, vi } from "vitest";
import { Transport, TransportError, atHome, lanBase, probeLAN, rebindHintApplies, relayBase, type Decision } from "../src/lib/transport";
import type { Hive } from "../src/lib/hives";

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

  it("throws TransportError when no path exists, and rethrows relay failures", async () => {
    const t = new Transport({ fetch: refuse });
    const nothing: Hive = { ...hive, id: "hv_none", lan: undefined, relay_host: undefined };
    await expect(t.fetch(nothing, "/v0/cameras", { token: "t" })).rejects.toBeInstanceOf(TransportError);
    await expect(t.fetch(hive, "/v0/cameras", { token: "t" })).rejects.toBeInstanceOf(TypeError);
  });
});

describe("atHome", () => {
  it("is true on the LAN path or in local mode, false through the relay or with no decision yet", () => {
    const d = (path: Decision["path"]): Decision => ({ path, reason: "lan_ok", at: 0 });
    expect(atHome(false, d("lan"))).toBe(true);
    expect(atHome(false, d("relay"))).toBe(false);
    expect(atHome(false, d("none"))).toBe(false);
    expect(atHome(false, null)).toBe(false);
    expect(atHome(true, null)).toBe(true);
    expect(atHome(true, d("relay"))).toBe(true);
  });
});
