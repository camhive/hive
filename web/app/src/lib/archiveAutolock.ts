// Automatic locking of the encrypted archive (#147, ADR-0019 addendum): the
// owner's two settings, and the app's report whether it is at home. The
// hive decides; the report is advisory, and the inactivity timer is the
// fallback when the app is closed.
import type { HiveClient, hiveComponents } from "@camhive/api";
import { unwrapHive } from "@camhive/api";
import { direct, type Decision } from "./transport";

export type AutolockSettings = hiveComponents["schemas"]["AutolockSettings"];
export type AutolockAfter = NonNullable<AutolockSettings["after"]>;
export type AutolockAway = NonNullable<AutolockSettings["away"]>;

/** The choices, in the order the selects show them. */
export const AFTER_CHOICES: AutolockAfter[] = ["off", "1h", "8h", "24h", "7d"];
export const AWAY_CHOICES: AutolockAway[] = ["off", "10m", "1h"];

/** The owner's requests; the settings need a session through Camhive (a local one gets 403). */
export function autolockActions(client: HiveClient) {
  return {
    set: async (patch: AutolockSettings) => unwrapHive(await client.PATCH("/v0/archive/encryption/autolock", { body: patch })),
    presence: async (atHome: boolean) => { unwrapHive(await client.POST("/v0/archive/encryption/presence", { body: { at_home: atHome } })); },
  };
}

/** At home on the LAN or in local mode, away through the relay; null while probing, or while the path is not known or the hive is unreachable. */
export function presenceOf(local: boolean, d: Decision | null, probing = false): boolean | null {
  if (probing) return null;
  if (local || direct(d)) return true;
  return d?.path === "relay" ? false : null;
}

/** The fewest milliseconds between two reports for one hive, and the heartbeat while a page is open. */
export const PRESENCE_EVERY_MS = 60_000;

type Send = (atHome: boolean) => Promise<void>;

/**
 * Sends each hive the latest presence: at once when a minute has passed
 * since the last request (or `now`: an unlock, a restart of the hive),
 * otherwise a minute after it. The same value goes out again too: it is
 * the heartbeat, and a hive that restarted has forgotten what it was told.
 * A failed request counts only for the throttle; the next report retries.
 */
export class PresenceReporter {
  private readonly tried = new Map<string, number>();
  private readonly pending = new Map<string, { atHome: boolean; send: Send }>();
  constructor(
    private readonly clock: () => number = Date.now,
    private readonly schedule: (f: () => void, ms: number) => void = (f, ms) => { setTimeout(f, ms); },
  ) {}

  report(hiveID: string, atHome: boolean, send: Send, opts: { now?: boolean } = {}): void {
    const tried = this.tried.get(hiveID);
    const wait = tried === undefined || opts.now ? 0 : tried + PRESENCE_EVERY_MS - this.clock();
    if (wait <= 0) {
      this.flush(hiveID, atHome, send);
      return;
    }
    const queued = this.pending.has(hiveID);
    this.pending.set(hiveID, { atHome, send });
    if (!queued) this.schedule(() => this.due(hiveID), wait);
  }

  private due(hiveID: string): void {
    const p = this.pending.get(hiveID);
    if (p) this.flush(hiveID, p.atHome, p.send);
  }

  private flush(hiveID: string, atHome: boolean, send: Send): void {
    this.pending.delete(hiveID);
    this.tried.set(hiveID, this.clock());
    send(atHome).catch(() => { /* advisory: the heartbeat retries within a minute */ });
  }
}

let reporter = new PresenceReporter();

/** The app-wide reporter; tests get a fresh one. */
export function presenceReporter(): PresenceReporter {
  return reporter;
}

export function resetPresenceForTest(): void {
  reporter = new PresenceReporter();
}

export type LocksIn = { unit: "minutes" | "hours" | "days"; count: number };

/** How long until the archive locks itself, rounded up: minutes under two hours, hours under two days. */
export function locksIn(locksAt: string, now = Date.now()): LocksIn {
  const minutes = Math.max(1, Math.ceil((Date.parse(locksAt) - now) / 60_000));
  if (minutes < 120) return { unit: "minutes", count: minutes };
  const hours = Math.ceil(minutes / 60);
  return hours < 48 ? { unit: "hours", count: hours } : { unit: "days", count: Math.ceil(hours / 24) };
}
