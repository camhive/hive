// Local emergency login (#57, ADR-0004): when the cloud is unreachable and
// no offline grant is left, the owner signs in on the LAN, at the hive's LAN
// name or, without DNS, at its LAN address (ADR-0023), with
// the local username and password set while online. The hive answers with
// an owner token it issued itself, valid for an hour and only on the LAN.
// The session lives in sessionStorage: it ends with the tab.
import { useEffect, useSyncExternalStore } from "react";
import { CamhiveError, type AccessGrant, type hiveComponents } from "@camhive/api";
import type { Hive } from "./hives";
import { retryAfterSeconds } from "./retryAfter";

export type LocalSession = hiveComponents["schemas"]["LocalSession"];

const KEY = (hiveID: string) => `camhive.local.${hiveID}`;
const listeners = new Set<() => void>();
let version = 0;

function storage(): Storage | null {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage;
  } catch {
    return null;
  }
}

function changed() {
  version++;
  for (const l of listeners) l();
}

export function readLocal(hiveID: string, now = Date.now()): LocalSession | null {
  try {
    const raw = storage()?.getItem(KEY(hiveID));
    if (!raw) return null;
    const s = JSON.parse(raw) as LocalSession;
    return Date.parse(s.expires_at) > now ? s : null;
  } catch {
    return null;
  }
}

export function writeLocal(hiveID: string, s: LocalSession): void {
  try {
    storage()?.setItem(KEY(hiveID), JSON.stringify(s));
  } catch {
    /* storage blocked: the session lasts as long as the page */
  }
  changed();
}

export function clearLocal(hiveID: string): void {
  try {
    storage()?.removeItem(KEY(hiveID));
  } catch {
    /* ignore */
  }
  changed();
}

/** The hive refused a sign-in because of too many failures; `retryAfterS` comes from Retry-After, null when hidden. */
export class LocalLoginLocked extends CamhiveError {
  readonly retryAfterS: number | null;
  constructor(body: { code?: string; message?: string } | undefined, retryAfterS: number | null) {
    super(429, body);
    this.retryAfterS = retryAfterS;
  }
}

/** Signs in at `base`, the hive's direct LAN base the transport decided (name or address); never through the relay, which refuses it anyway. */
export async function signInLocally(base: string | null, username: string, password: string, f: typeof fetch = (i, init) => globalThis.fetch(i, init)): Promise<LocalSession> {
  if (!base) throw new CamhiveError(403, { code: "lan_only" });
  const res = await f(`${base}/v0/local-login/sessions`, {
    method: "POST", mode: "cors", cache: "no-store",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  const body = await res.json().catch(() => undefined);
  if (res.status === 429) throw new LocalLoginLocked(body, retryAfterSeconds(res.headers));
  if (!res.ok) throw new CamhiveError(res.status, body);
  return body as LocalSession;
}

/** The local session as the grant hive pages use: owner, every camera, no cloud session behind it. */
export function localGrant(s: LocalSession, hive: Hive): AccessGrant {
  return { token: s.token, session_id: s.session_id, expires_at: s.expires_at, offline: false, role: "owner", cameras: s.cameras, hive, ice_servers: [] };
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** The valid local session for a hive; re-renders on sign-in, sign-out and expiry. */
export function useLocalSession(hiveID: string | undefined): LocalSession | null {
  useSyncExternalStore(subscribe, () => version, () => version);
  const s = hiveID ? readLocal(hiveID) : null;
  useEffect(() => {
    if (!s) return;
    const timer = setTimeout(changed, Math.max(0, Date.parse(s.expires_at) - Date.now()) + 50);
    return () => clearTimeout(timer);
  }, [s?.expires_at]); // eslint-disable-line react-hooks/exhaustive-deps
  return s;
}
