// Offline access tokens (ADR-0004, #56): owners may hold a 7-day token for
// LAN use while the cloud is unreachable. The app fetches one per hive
// while online (at most once a day) and keeps it in localStorage; when the
// cloud is down the hive pages fall back to it for the LAN path and local
// signalling.
import { useEffect } from "react";
import type { AccessGrant } from "@camhive/api";
import { issueAccess } from "./access";

export const OFFLINE_REFRESH_MS = 24 * 3_600_000;
const KEY = (hiveID: string) => `camhive.offline.${hiveID}`;

export type StoredOffline = { grant: AccessGrant; fetched_at: number };

function storage(): Storage | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

export function readOffline(hiveID: string, now = Date.now()): AccessGrant | null {
  try {
    const raw = storage()?.getItem(KEY(hiveID));
    if (!raw) return null;
    const s = JSON.parse(raw) as StoredOffline;
    return Date.parse(s.grant.expires_at) > now ? s.grant : null;
  } catch {
    return null;
  }
}

export function offlineFetchedAt(hiveID: string): number {
  try {
    const raw = storage()?.getItem(KEY(hiveID));
    return raw ? (JSON.parse(raw) as StoredOffline).fetched_at : 0;
  } catch {
    return 0;
  }
}

export function writeOffline(hiveID: string, grant: AccessGrant, now = Date.now()): void {
  try {
    storage()?.setItem(KEY(hiveID), JSON.stringify({ grant, fetched_at: now } satisfies StoredOffline));
  } catch {
    /* storage blocked: the LAN fallback is a convenience */
  }
}

export function clearOffline(hiveID: string): void {
  try {
    storage()?.removeItem(KEY(hiveID));
  } catch {
    /* ignore */
  }
}

/** Keeps a fresh offline grant for an owned hive while the cloud is reachable. */
export function useKeepOfflineGrant(hiveID: string | undefined, owner: boolean, online: boolean, issue: typeof issueAccess = issueAccess) {
  useEffect(() => {
    if (!hiveID || !owner || !online) return;
    if (Date.now() - offlineFetchedAt(hiveID) < OFFLINE_REFRESH_MS) return;
    let cancelled = false;
    issue(hiveID, { offline: true }).then((g) => { if (!cancelled) writeOffline(hiveID, g); }).catch(() => undefined);
    return () => { cancelled = true; };
  }, [hiveID, owner, online, issue]);
}
