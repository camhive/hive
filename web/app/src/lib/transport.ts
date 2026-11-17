// HTTP path selection to a hive (ADR-0010 / ADR-0012 / ADR-0023). Live
// video media never comes through here: it is always WebRTC (see live.ts).
// Archive, export, signalling at home and the hive API go straight to the
// hive's LAN name, to one of its LAN addresses when the name does not
// resolve (an emergency without DNS, ADR-0023), or through the cloud relay;
// the choice is probed once per hive, remembered for the session and
// re-probed on network changes.
import type { Hive } from "./hives";
import { cacheKey } from "./cache";

export type Reason = "lan_ok" | "address_ok" | "lan_failed" | "lan_unavailable" | "relay_unavailable";
/** `address` is `host:port` of the LAN address that answered, IPv6 in brackets. */
export type Decision =
  | { path: "lan" | "relay" | "none"; reason: Reason; at: number }
  | { path: "address"; reason: "address_ok"; at: number; address: string };
export type HttpPath = Decision["path"];

type KeyValue = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export interface TransportDeps {
  fetch?: typeof fetch;
  storage?: KeyValue | null;
  /** Lasting storage for addresses the owner typed (ADR-0023); they must survive a restart in an outage. */
  keep?: KeyValue | null;
  now?: () => number;
  /** Probe timeout; the LAN answers in milliseconds or not at all. */
  timeoutMs?: number;
  /** How long a decision is trusted without a fresh probe. */
  ttlMs?: number;
}

export const PROBE_TIMEOUT_MS = 1500;
export const DECISION_TTL_MS = 10 * 60_000;

/** `https://<id>.h.camhive.app:<port>` when the hive has reported a LAN listener. */
export function lanBase(h: Pick<Hive, "lan">): string | null {
  const lan = h.lan;
  if (!lan?.host || !lan.port || !(lan.addrs?.length ?? 0)) return null;
  return `https://${lan.host}:${lan.port}`;
}

/** `https://<id>.relay.camhive.app[:port]`, TLS terminated on the hive. */
export function relayBase(h: Pick<Hive, "relay_host">): string | null {
  return h.relay_host ? `https://${h.relay_host}` : null;
}

/** `host:port` of an address the owner may open the hive at: a private IPv4 (RFC 1918: 10/8, 172.16/12, 192.168/16), IPv4 link-local (169.254/16) or IPv6 unique local (fc00::/7) literal, optionally with `https://` and a port as `hive status` prints it; null for anything else (names, public, loopback, IPv6 link-local, which a browser cannot open without a zone), so a token never leaves the LAN and no name needs DNS. */
export function parseLanAddress(input: string, port: number): string | null {
  const raw = input.trim().replace(/^lan:\s*/, "").replace(/^https:\/\//, "").replace(/\/$/, "");
  // IPv4 octets without a leading zero: the URL parser reads 010 as octal, so "010.1.2.3" would open 8.1.2.3.
  const m = /^\[([0-9a-f:]+)\](?::(\d{1,5}))?$/i.exec(raw) ?? /^([0-9a-f]*:[0-9a-f:]+)()$/i.exec(raw) ?? /^((?:0|[1-9]\d{0,2})(?:\.(?:0|[1-9]\d{0,2})){3})(?::(\d{1,5}))?$/.exec(raw);
  const literal = m?.[1];
  if (!m || !literal) return null;
  const host = literal.includes(":") ? privateIPv6(literal) : privateIPv4(literal);
  const p = m[2] ? Number(m[2]) : port;
  return host && p >= 1 && p <= 65535 ? `${host}:${p}` : null;
}

function privateIPv4(a: string): string | null {
  const octets = a.split(".").map(Number);
  const [x = 0, y = 0] = octets;
  if (octets.some((o) => o > 255)) return null;
  return x === 10 || (x === 172 && y >= 16 && y <= 31) || (x === 192 && y === 168) || (x === 169 && y === 254) ? a : null;
}

/** The bracketed, normalised form of a unique local IPv6 address (fc00::/7), the private range a hive reports. */
function privateIPv6(a: string): string | null {
  try {
    const host = new URL(`https://[${a}]/`).hostname;
    return /^\[f[cd]/i.test(host) ? host : null;
  } catch {
    return null;
  }
}

/** Whether a browser may be sent to the hive's LAN address (ADR-0023): it has a TLS port and a publicly issued (Let's Encrypt) certificate. Anything else (self-signed, our own CA, an unknown or missing issuer) is refused: its warning at an address is an untrusted-issuer one, which an attacker reproduces exactly. */
export function openableByAddress(hive: Pick<Hive, "lan">): boolean {
  return !!hive.lan?.port && hive.lan.cert_issuer === "letsencrypt";
}

/** GET /healthz on the LAN name with a short timeout; any failure (mixed content, permission denied, rebinding, timeout) is "no". */
export async function probeLAN(base: string, deps: TransportDeps = {}): Promise<boolean> {
  const f = deps.fetch ?? ((i, init) => globalThis.fetch(i, init));
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), deps.timeoutMs ?? PROBE_TIMEOUT_MS);
  try {
    const res = await f(`${base}/healthz`, { signal: ctl.signal, cache: "no-store", mode: "cors" });
    return res.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timer);
  }
}

type Listener = (hiveID: string, d: Decision) => void;

export class Transport {
  private readonly deps: TransportDeps;
  private readonly inflight = new Map<string, Promise<Decision>>();
  private readonly listeners = new Set<Listener>();
  private readonly memory = new Map<string, Decision>();

  constructor(deps: TransportDeps = {}) {
    this.deps = deps;
  }

  private now(): number {
    return (this.deps.now ?? Date.now)();
  }

  private key(hiveID: string): string {
    return `camhive.transport.${hiveID}`;
  }

  /** Under the cache prefix: a typed address belongs to the signed-in account and goes on sign-out. */
  private keepKey(hiveID: string): string {
    return cacheKey(`lanaddr.${hiveID}`);
  }

  /** The LAN addresses to try, `host:port`: the one the owner typed first, then those the hive last reported; none unless the hive is openable by address. */
  addresses(hive: Pick<Hive, "id" | "lan">): string[] {
    const lan = hive.lan;
    if (!lan?.port || !openableByAddress(hive)) return [];
    let typed: string | null = null;
    try {
      typed = this.deps.keep?.getItem(this.keepKey(hive.id)) ?? null;
    } catch {
      /* storage blocked */
    }
    const all = [typed, ...(lan.addrs ?? [])].map((a) => (a ? parseLanAddress(a, lan.port!) : null)).filter((a): a is string => !!a);
    return [...new Set(all)];
  }

  /** Keeps an address the owner typed for a hive and forgets the hive's decision; returns it as `host:port`, or null when it is not a private address literal. */
  setAddress(hive: Pick<Hive, "id" | "lan">, input: string): string | null {
    const port = hive.lan?.port;
    const addr = port ? parseLanAddress(input, port) : null;
    if (!addr) return null;
    try {
      this.deps.keep?.setItem(this.keepKey(hive.id), addr);
    } catch {
      /* storage blocked: it lasts as long as the decision */
    }
    this.forget(hive.id);
    return addr;
  }

  /** The last decision for a hive if it is still fresh. */
  remembered(hiveID: string): Decision | null {
    const d = this.memory.get(hiveID) ?? this.read(hiveID);
    if (!d) return null;
    if (this.now() - d.at > (this.deps.ttlMs ?? DECISION_TTL_MS)) return null;
    return d;
  }

  private read(hiveID: string): Decision | null {
    try {
      const raw = this.deps.storage?.getItem(this.key(hiveID));
      return raw ? (JSON.parse(raw) as Decision) : null;
    } catch {
      return null;
    }
  }

  private remember(hiveID: string, d: Decision): Decision {
    this.memory.set(hiveID, d);
    try {
      this.deps.storage?.setItem(this.key(hiveID), JSON.stringify(d));
    } catch {
      /* storage full or blocked: memory is enough */
    }
    for (const l of this.listeners) l(hiveID, d);
    return d;
  }

  /** Decides the HTTP path for a hive, probing the LAN unless a fresh decision exists (or `force`). */
  select(hive: Hive, opts: { force?: boolean } = {}): Promise<Decision> {
    if (!opts.force) {
      const d = this.remembered(hive.id);
      if (d) return Promise.resolve(d);
    }
    const running = this.inflight.get(hive.id);
    if (running) return running;
    const p = this.decide(hive).then((d) => this.remember(hive.id, d)).finally(() => this.inflight.delete(hive.id));
    this.inflight.set(hive.id, p);
    return p;
  }

  private async decide(hive: Hive): Promise<Decision> {
    const at = this.now();
    const lan = lanBase(hive);
    const relay = relayBase(hive);
    if (lan && (await probeLAN(lan, this.deps))) return { path: "lan", reason: "lan_ok", at };
    // Only when the name failed: the addresses, in parallel with the same short timeout (ADR-0023).
    const a = await this.firstAnswering(this.addresses(hive));
    if (a) return { path: "address", reason: "address_ok", at, address: a };
    if (relay) return { path: "relay", reason: lan ? "lan_failed" : "lan_unavailable", at };
    return { path: "none", reason: lan ? "relay_unavailable" : "lan_unavailable", at };
  }

  private async firstAnswering(addrs: string[]): Promise<string | null> {
    const answers = await Promise.all(addrs.map((a) => probeLAN(`https://${a}`, this.deps)));
    return addrs[answers.indexOf(true)] ?? null;
  }

  /** Base URL for the decided path, or null when the hive is unreachable over HTTP. */
  baseFor(hive: Hive, d: Decision): string | null {
    switch (d.path) {
      case "lan": return lanBase(hive);
      case "address": return `https://${d.address}`;
      case "relay": return relayBase(hive);
      case "none": return null;
    }
  }

  /**
   * Fetches `path` on the hive with the access token. A LAN request that
   * fails at the network level demotes the hive to the relay and is retried
   * there once; HTTP error statuses are returned as they are. A local
   * session's token (#57) stays on the LAN: the hive refuses it through the
   * relay, so it is never sent there.
   */
  async fetch(hive: Hive, path: string, init: RequestInit & { token: string }): Promise<Response> {
    const { token, ...rest } = init;
    const lanOnly = isLocalToken(token);
    const headers = new Headers(rest.headers);
    headers.set("Authorization", `Bearer ${token}`);
    const f = this.deps.fetch ?? ((i, ini) => globalThis.fetch(i, ini));
    let d = await this.select(hive);
    for (let attempt = 0; ; attempt++) {
      const base = this.baseFor(hive, d);
      if (!base || (lanOnly && !direct(d))) throw new TransportError("unreachable", d);
      try {
        return await f(`${base}${path}`, { ...rest, headers, mode: "cors" });
      } catch (err) {
        if (lanOnly || !direct(d) || attempt > 0 || !relayBase(hive)) throw err;
        d = this.remember(hive.id, { path: "relay", reason: "lan_failed", at: this.now() });
      }
    }
  }

  /** Forgets every decision: the network changed, paths must be probed again. */
  invalidateAll(): void {
    for (const id of [...this.memory.keys()]) this.forget(id);
  }

  private forget(hiveID: string): void {
    this.memory.delete(hiveID);
    try {
      this.deps.storage?.removeItem(this.key(hiveID));
    } catch {
      /* ignore */
    }
  }

  subscribe(l: Listener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }

  /** Re-probes on `online` and on connection-type changes (mobile ↔ Wi-Fi). Returns a detach function. */
  attach(target: EventTarget, connection: EventTarget | null | undefined = browserConnection()): () => void {
    const reset = () => this.invalidateAll();
    target.addEventListener("online", reset);
    connection?.addEventListener("change", reset);
    return () => {
      target.removeEventListener("online", reset);
      connection?.removeEventListener("change", reset);
    };
  }
}

/** navigator.connection where the browser has it (Chromium, Android). */
function browserConnection(): EventTarget | null {
  const nav = (globalThis as { navigator?: { connection?: EventTarget } }).navigator;
  return nav?.connection ?? null;
}

/** A token the hive issued itself after a local sign-in (PASETO v4.local), not a cloud token. */
export function isLocalToken(token: string): boolean {
  return token.startsWith("v4.local.");
}

export class TransportError extends Error {
  readonly decision: Decision;
  constructor(code: "unreachable", decision: Decision) {
    super(code);
    this.name = "TransportError";
    this.decision = decision;
  }
}

/** The hive answers on the LAN, by name or by address: no cloud, no relay in between. */
export function direct(d: Decision | null): boolean {
  return d?.path === "lan" || d?.path === "address";
}

/**
 * The viewer is at home when the hive answers on the LAN (or the app runs
 * in local mode because the cloud is unreachable). At home live video is
 * signalled straight to the hive and the plan does not limit quality; the
 * hive applies plan limits to relayed and cloud-signalled requests only (#58).
 */
export function atHome(local: boolean, d: Decision | null): boolean {
  return local || direct(d);
}

/** When the LAN name failed although the hive reports LAN addresses, the router's DNS-rebinding protection is the usual cause. */
export function rebindHintApplies(hive: Pick<Hive, "lan">, d: Decision | null): boolean {
  return d?.reason === "lan_failed" && (hive.lan?.addrs?.length ?? 0) > 0;
}

function defaultStorage(kind: "sessionStorage" | "localStorage"): KeyValue | null {
  try {
    return typeof globalThis[kind] === "undefined" ? null : globalThis[kind];
  } catch {
    return null;
  }
}

/** The app-wide instance; tests build their own. */
export const transport = new Transport({ storage: defaultStorage("sessionStorage"), keep: defaultStorage("localStorage") });
