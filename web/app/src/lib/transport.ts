// HTTP path selection to a hive (ADR-0010 / ADR-0012). Live video never
// comes through here: it is always WebRTC via signalling (see live.ts).
// Archive, export and the hive API go either straight to the hive's LAN
// name or through the cloud relay; the choice is probed once per hive,
// remembered for the session and re-probed on network changes.
import type { Hive } from "./hives";

export type HttpPath = "lan" | "relay" | "none";
export type Reason = "lan_ok" | "lan_failed" | "lan_unavailable" | "relay_unavailable";
export type Decision = { path: HttpPath; reason: Reason; at: number };

export interface TransportDeps {
  fetch?: typeof fetch;
  storage?: Pick<Storage, "getItem" | "setItem" | "removeItem"> | null;
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
    if (relay) return { path: "relay", reason: lan ? "lan_failed" : "lan_unavailable", at };
    return { path: "none", reason: lan ? "relay_unavailable" : "lan_unavailable", at };
  }

  /** Base URL for the decided path, or null when the hive is unreachable over HTTP. */
  baseFor(hive: Hive, d: Decision): string | null {
    return d.path === "lan" ? lanBase(hive) : d.path === "relay" ? relayBase(hive) : null;
  }

  /**
   * Fetches `path` on the hive with the access token. A LAN request that
   * fails at the network level demotes the hive to the relay and is retried
   * there once; HTTP error statuses are returned as they are.
   */
  async fetch(hive: Hive, path: string, init: RequestInit & { token: string }): Promise<Response> {
    const { token, ...rest } = init;
    const headers = new Headers(rest.headers);
    headers.set("Authorization", `Bearer ${token}`);
    const f = this.deps.fetch ?? ((i, ini) => globalThis.fetch(i, ini));
    let d = await this.select(hive);
    for (let attempt = 0; ; attempt++) {
      const base = this.baseFor(hive, d);
      if (!base) throw new TransportError("unreachable", d);
      try {
        return await f(`${base}${path}`, { ...rest, headers, mode: "cors" });
      } catch (err) {
        if (d.path !== "lan" || attempt > 0 || !relayBase(hive)) throw err;
        d = this.remember(hive.id, { path: "relay", reason: "lan_failed", at: this.now() });
      }
    }
  }

  /** Forgets every decision: the network changed, paths must be probed again. */
  invalidateAll(): void {
    for (const id of this.memory.keys()) {
      try {
        this.deps.storage?.removeItem(this.key(id));
      } catch {
        /* ignore */
      }
    }
    this.memory.clear();
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

export class TransportError extends Error {
  readonly decision: Decision;
  constructor(code: "unreachable", decision: Decision) {
    super(code);
    this.name = "TransportError";
    this.decision = decision;
  }
}

/**
 * The viewer is at home when the hive answers on its LAN name (or the app runs
 * in local mode because the cloud is unreachable). At home live video is
 * signalled straight to the hive and the plan does not limit quality; the
 * hive applies plan limits to relayed and cloud-signalled requests only (#58).
 */
export function atHome(local: boolean, d: Decision | null): boolean {
  return local || d?.path === "lan";
}

/** When the LAN name failed although the hive reports LAN addresses, the router's DNS-rebinding protection is the usual cause. */
export function rebindHintApplies(hive: Pick<Hive, "lan">, d: Decision | null): boolean {
  return d?.reason === "lan_failed" && (hive.lan?.addrs?.length ?? 0) > 0;
}

function defaultStorage(): TransportDeps["storage"] {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage;
  } catch {
    return null;
  }
}

/** The app-wide instance; tests build their own. */
export const transport = new Transport({ storage: defaultStorage() });
