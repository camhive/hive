// The hive's settings page (#174): what the status strip and the rows say,
// worked out from the hive's answers before anything is rendered. Every
// state is a list of facts (an i18n key with its values and a tone), so the
// strip and the rows say the same thing in the same words.
import type { hiveComponents } from "@camhive/api";
import { hiveCapability, type Capability, type HiveInfo } from "./capabilities";
import { direct, rebindHintApplies, type Decision } from "./transport";
import { fillsLine, forecastLine, formatSize, type Line, type StorageOverview } from "./storage";
import type { Encryption } from "./archiveEncryption";
import { moveProgress, type DataMove } from "./diskHealth";
import type { RecordingStop } from "./recording";
import type { Hive } from "./hives";

export type UpdateStatus = hiveComponents["schemas"]["UpdateStatus"];
export type LocalLogin = hiveComponents["schemas"]["LocalLogin"];

export type Tone = "good" | "warn" | "bad";
export type Fact = { key: string; values?: Record<string, string | number>; tone?: Tone };

/**
 * One endpoint's answer as far as the page has it: the data, whether it is
 * still coming, and whether the hive could not be asked because the cloud
 * reports it offline.
 */
export type Answer<T> = { data?: T; loading: boolean; offline?: boolean };

/** The page's groups, top to bottom, on the phone and on wide screens alike. */
export const GROUPS = ["connection", "recordings", "access", "software", "danger"] as const;
export type Group = (typeof GROUPS)[number];

export type StripId = "version" | "path" | "disk" | "footage" | "encryption" | "updates";
export type StripItem = { id: StripId; facts: Fact[] };

export type StripInput = {
  hive: HiveInfo;
  /** Disk, footage, encryption and updates are the owner's (a member's token gets none of them). */
  owner: boolean;
  decision: Decision | null;
  lang: string;
  storage: Answer<StorageOverview>;
  encryption: Answer<Encryption>;
  updates: Answer<UpdateStatus>;
  /** The hive does not record (#172): the hive's own word, else the cloud's. */
  stop?: RecordingStop;
};

const fact = (key: string, values?: Fact["values"], tone?: Tone): Fact => ({ key, ...(values && { values }), ...(tone && { tone }) });

/** The one line at the top: everything the owner looks for first. */
export function stripItems(s: StripInput): StripItem[] {
  const items: StripItem[] = [
    { id: "version", facts: [versionFact(s.hive.version)] },
    { id: "path", facts: [pathFact(s.decision)] },
  ];
  if (!s.owner) return items;
  return [
    ...items,
    { id: "disk", facts: [...stopFacts(s.stop, s.lang), ...answered(s.hive, "disk.health", s.storage, (o) => [diskFact(o)])] },
    { id: "footage", facts: answered(s.hive, "storage", s.storage, (o) => footageFacts(o, s.lang)) },
    { id: "encryption", facts: answered(s.hive, "archive.encryption", s.encryption, encryptionFacts) },
    { id: "updates", facts: answered(s.hive, "updates", s.updates, (u) => [updatesModeFact(u)]) },
  ];
}

/** The reasons a stop names that the app has words for; others read as unknown. */
const STOP_REASONS = new Set(["disk_not_mounted", "archive_missing", "archive_changed", "archive_unwritable", "database", "engine_stalled"]);

/** "recording stopped since <time>: <reason>" first in the disk item while the hive does not record (#172). */
export function stopFacts(stop: RecordingStop | undefined, lang: string): Fact[] {
  if (!stop) return [];
  const reason = STOP_REASONS.has(stop.reason) ? stop.reason : "unknown";
  return [fact("settings.fact.recordingStopped", { time: new Date(stop.since).toLocaleString(lang), reason }, "bad")];
}

/**
 * The facts of an answer, or why there are none: the hive does not report
 * the capability (which version brings it), the answer is coming, the hive
 * is offline, or it did not answer.
 */
export function answered<T>(hive: HiveInfo, capability: Capability, a: Answer<T>, facts: (data: T) => Fact[]): Fact[] {
  const cap = hiveCapability(hive, capability);
  if (!cap.has) return [fact("settings.fact.notReported", { since: cap.since })];
  if (a.data !== undefined) return facts(a.data);
  if (a.loading) return [fact("settings.fact.checking")];
  return [fact(a.offline ? "settings.fact.offline" : "settings.fact.unknown")];
}

export function versionFact(version: string | undefined): Fact {
  return version ? fact("settings.fact.version", { version }) : fact("settings.fact.unknownVersion");
}

export function pathFact(d: Decision | null): Fact {
  if (!d) return fact("transport.probing");
  if (direct(d)) return fact("settings.fact.path.direct", undefined, "good");
  return d.path === "relay" ? fact("settings.fact.path.relay") : fact("settings.fact.path.none", undefined, "bad");
}

const DISK_TONE: Record<string, Tone | undefined> = { ok: "good", failing: "warn", failed: "bad" };

export function diskFact(o: StorageOverview): Fact {
  const state = o.disk?.health?.state ?? "unknown";
  return fact(`settings.fact.disk.${state}`, undefined, DISK_TONE[state]);
}

/** What the archive holds and what the disk does next: full now, fills in N days, or keeps the retention. */
export function footageFacts(o: StorageOverview, lang: string): Fact[] {
  const bytes = o.disk?.archive_bytes ?? o.cameras.reduce((n, c) => n + c.bytes, 0);
  if (bytes === 0) return [fact("settings.fact.noFootage")];
  const size = formatSize(bytes, lang);
  const days = Math.floor(Math.max(0, ...o.cameras.map((c) => c.days_held)));
  const held = days >= 1 ? fact("settings.fact.held", { size, count: days }) : fact("settings.fact.heldToday", { size });
  const next = outlook(o);
  return next ? [held, next] : [held];
}

/**
 * What the disk does next, in the panel's own terms (fillsLine, then
 * forecastLine), so the strip, the row and the Storage panel cannot disagree.
 */
function outlook(o: StorageOverview): Fact | null {
  const fills = fillsLine(o);
  if (fills) return FILLS[fills.key]!(fills.values);
  const line = forecastLine(o, "en"); // only its key and numbers are read here
  return FORECAST[line.key]?.(line.values) ?? null;
}

type Values = Line["values"];
const FILLS: Record<string, (v: Values) => Fact> = {
  "storage.fillsNow": () => fact("settings.fact.fullNow", undefined, "warn"),
  "storage.fillsSoon": () => fact("settings.fact.fillsSoon", undefined, "warn"),
  "storage.fillsIn": (v) => fact("settings.fact.fillsIn", { count: v.count! }),
};
const FORECAST: Record<string, (v: Values) => Fact> = {
  "storage.forecast.fits": (v) => fact("settings.fact.keeps", { count: v.retention! }),
  "storage.forecast.fitsOwn": (v) => fact("settings.fact.keepsOwn", { count: v.retention!, camera: v.camera!, own: v.own! }),
  "storage.forecast.short": (v) => fact("settings.fact.keepsShort", { count: v.effective!, retention: v.retention! }, "warn"),
  "storage.forecast.unlimited": (v) => fact("settings.fact.keepsUntilFull", { count: v.effective! }),
  "storage.forecast.unlimitedPlain": () => fact("settings.fact.keepsUntilFullPlain"),
};

export function encryptionFacts(e: Encryption): Fact[] {
  const state = fact(`settings.fact.encryption.${e.state}`);
  return e.state !== "off" && !e.sealing ? [state, fact("settings.fact.encryption.notSealing", undefined, "warn")] : [state];
}

export function updatesModeFact(u: UpdateStatus): Fact {
  return u.supported ? fact(`settings.fact.updates.${u.updates}`) : fact("settings.fact.updates.unsupported");
}

/** The Updates row: what runs, how it updates, and what waits. */
export function updatesFacts(u: UpdateStatus): Fact[] {
  const out = [fact("settings.fact.version", { version: u.running }), updatesModeFact(u)];
  if (!u.supported) return out;
  const [from, to] = u.update_window.split("-");
  if (u.updates === "automatic") out.push(fact("settings.fact.updates.window", { from: from ?? "", to: to ?? "" }));
  if (u.available) out.push(fact("settings.fact.updates.available", { version: u.available }, "warn"));
  return out;
}

export function localLoginFacts(l: LocalLogin): Fact[] {
  return [l.enabled ? fact("settings.fact.localLogin.on", { username: l.username ?? "" }) : fact("settings.fact.localLogin.off")];
}

/** The Move row: whether this hive can move, and how far a move has come; a cancelled one is said once. */
export function moveFacts(m: DataMove): Fact[] {
  switch (m.state) {
    case "copying": return [fact("settings.fact.move.copying", { pct: moveProgress(m) }, "warn")];
    case "switching": return [fact("settings.fact.move.switching", undefined, "warn")];
    case "done": return [fact("settings.fact.move.done", { to: m.to ?? "" })];
  }
  const can = fact(m.supported ? "settings.fact.move.ready" : "settings.fact.move.unsupported");
  return m.state === "failed" ? [can, fact("settings.fact.move.failed")] : [can];
}

/** The connection callouts, each only where it applies (#174). */
export function connectionHints(hive: Pick<Hive, "lan">, d: Decision | null, userAgent: string) {
  return {
    /** The LAN name failed although the hive reports addresses: the router's rebind protection. */
    rebind: rebindHintApplies(hive, d),
    /** Chromium asks for local network access; only on the direct path is there something to allow. */
    chrome: direct(d) && /\b(Chrome|Chromium)\//.test(userAgent),
  };
}
