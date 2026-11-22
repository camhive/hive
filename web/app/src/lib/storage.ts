// The storage overview (#150, ADR-0024). The hive does the arithmetic
// (docs/dev/sizing.md, "The storage arithmetic"); the app only reads it,
// repeats the hive's validation so the form can say what is wrong, and
// warns before a change that deletes footage.
import type { hiveComponents } from "@camhive/api";

export type StorageOverview = hiveComponents["schemas"]["StorageOverview"];
export type CameraStorage = hiveComponents["schemas"]["CameraStorage"];
export type StorageValues = { retention_days: number; min_free_gb: number };

const GIB = 2 ** 30;
/** The hive's limit on any retention (internal/domain/camera MaxRetentionDays). */
export const MAX_RETENTION_DAYS = 3650;

/** The largest floor the hive accepts on this disk: smaller than the disk. */
export function maxFloorGB(totalBytes: number): number {
  return Math.ceil(totalBytes / GIB) - 1;
}

/** Reads the form as the hive's PATCH /v0/hive/storage validates it. */
export function validateStorage(raw: { retention_days: string; min_free_gb: string }, totalBytes: number): StorageValues | { invalid: keyof StorageValues } {
  const days = whole(raw.retention_days);
  if (days === null || days < 0 || days > MAX_RETENTION_DAYS) return { invalid: "retention_days" };
  const floor = whole(raw.min_free_gb);
  if (floor === null || floor < 1 || floor > maxFloorGB(totalBytes)) return { invalid: "min_free_gb" };
  return { retention_days: days, min_free_gb: floor };
}

function whole(s: string): number | null {
  const t = s.trim();
  return /^-?\d+$/.test(t) ? Number(t) : null;
}

/** The hive-wide retention as the sweeper applies it: the setting under the licence cap. */
export function appliedRetention(o: StorageOverview): number {
  const s = o.settings;
  if (!s) return 0;
  const cap = s.retention_days_max ?? 0;
  if (cap <= 0) return s.retention_days;
  return s.retention_days === 0 ? cap : Math.min(s.retention_days, cap);
}

export type Line = { key: string; values: Record<string, string | number> };

/** The sentence under the figures: what the disk holds and what retention becomes. */
export function forecastLine(o: StorageOverview, lang: string): Line {
  const f = o.forecast;
  if (!f || !o.settings || f.bytes_per_day === 0) return { key: "storage.forecast.nothing", values: {} };
  const retention = appliedRetention(o);
  const values = { rate: formatSize(f.bytes_per_day, lang, "up"), days: f.disk_days, retention, effective: f.horizon_days ?? retention };
  if (retention === 0) return { key: f.horizon_days === undefined ? "storage.forecast.unlimitedPlain" : "storage.forecast.unlimited", values };
  if (!f.retention_met) return { key: "storage.forecast.short", values };
  const shortest = shortestOwn(o, retention);
  if (f.disk_days < retention && shortest) return { key: "storage.forecast.fitsOwn", values: { ...values, camera: shortest.name ?? shortest.id, own: shortest.retention_days ?? 0 } };
  return { key: "storage.forecast.fits", values };
}

/** The recording camera that keeps the fewest days below the hive-wide retention. */
function shortestOwn(o: StorageOverview, retention: number): CameraStorage | undefined {
  return o.cameras
    .filter((c) => cameraRate(c) > 0 && (c.retention_days ?? 0) > 0 && (c.retention_days ?? 0) < retention)
    .sort((a, b) => (a.retention_days ?? 0) - (b.retention_days ?? 0))[0];
}

/** When the space rule deletes: now, within a day, in N days, or not at all. */
export function fillsLine(o: StorageOverview): Line | null {
  const f = o.forecast, s = o.settings;
  if (!f || !s) return null;
  if (f.deleting_for_space) return { key: "storage.fillsNow", values: { floor: s.min_free_gb } };
  if (f.fills_in_days === undefined) return null;
  if (f.fills_in_days === 0) return { key: "storage.fillsSoon", values: {} };
  return { key: "storage.fillsIn", values: { count: f.fills_in_days } };
}

/**
 * Whether the next sweep deletes footage because of these values, as the
 * hive decides it: the age rule deletes a camera on the hive-wide value
 * whose oldest footage is older than the new retention; the space rule
 * deletes when a raised floor is above the free space.
 */
export function deletesFootage(o: StorageOverview, next: StorageValues): boolean {
  const cap = o.settings?.retention_days_max ?? 0;
  const days = cap > 0 && (next.retention_days === 0 || next.retention_days > cap) ? cap : next.retention_days;
  const byAge = days > 0 && o.cameras.some((c) => c.follows_hive && (c.oldest_age_days ?? 0) > days);
  const raised = next.min_free_gb > (o.settings?.min_free_gb ?? 0);
  const bySpace = raised && !!o.disk && next.min_free_gb * GIB > o.disk.free_bytes;
  return byAge || bySpace;
}

/** A camera's rate as the forecast uses it: the larger window. */
export function cameraRate(c: CameraStorage): number {
  return Math.max(c.bytes_per_day_24h ?? 0, c.bytes_per_day_7d ?? 0);
}

/** Days with one decimal, rounded down: what is held never flatters. */
export function formatDays(days: number, lang: string): string {
  return new Intl.NumberFormat(lang, { maximumFractionDigits: 1 }).format(Math.floor(days * 10) / 10);
}

/**
 * Decimal sizes as disks are sold, with the reader's decimal sign. Rates
 * and space needed round up ("up"), measured sizes to the nearest.
 */
export function formatSize(n: number, lang: string, round: "up" | "near" = "near"): string {
  const [unit, scale] = n >= 1e12 ? ["TB", 1e12] : n >= 1e9 ? ["GB", 1e9] : n >= 1e6 ? ["MB", 1e6] : ["KB", 1e3];
  const v = n / scale;
  const r = round === "up" ? Math.ceil(v * 10 - 1e-9) / 10 : Math.round(v * 10) / 10;
  return `${new Intl.NumberFormat(lang, { maximumFractionDigits: 1 }).format(r)} ${unit}`;
}
