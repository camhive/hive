// Archive data and timeline math (#52). Times are RFC 3339 from the hive;
// the timeline is one local day.
import { useQuery } from "@tanstack/react-query";
import { unwrapHive, type HiveClient, type hiveComponents } from "@camhive/api";

export type Segment = hiveComponents["schemas"]["Segment"];
export type ArchiveEvent = hiveComponents["schemas"]["Event"];
export type ExportJob = hiveComponents["schemas"]["Export"];
export type StreamName = "main" | "sub";

export const DAY_MS = 86_400_000;

/** `YYYY-MM-DD` of a date in the browser's zone. */
export function dayKey(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** Local midnight of a `YYYY-MM-DD` key. */
export function dayStart(key: string): Date {
  const [y, m, d] = key.split("-").map(Number);
  return new Date(y!, (m ?? 1) - 1, d ?? 1);
}

/** Browser zone name for the calendar query. */
export function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** Segments as [startMs, endMs) clipped to the day, merged when touching (gap ≤ 1 s). */
export function coverage(segments: Segment[], day: Date): Array<[number, number]> {
  const d0 = day.getTime();
  const d1 = d0 + DAY_MS;
  const ranges = segments
    .map((s) => [Date.parse(s.start ?? ""), Date.parse(s.start ?? "") + (s.duration_ms ?? 0)] as [number, number])
    .filter(([a, b]) => Number.isFinite(a) && b > d0 && a < d1)
    .map(([a, b]) => [Math.max(a, d0), Math.min(b, d1)] as [number, number])
    .sort((x, y) => x[0] - y[0]);
  const out: Array<[number, number]> = [];
  for (const r of ranges) {
    const last = out[out.length - 1];
    if (last && r[0] - last[1] <= 1000) last[1] = Math.max(last[1], r[1]);
    else out.push([r[0], r[1]]);
  }
  return out;
}

/** Fraction of the day (0..1) → absolute time. */
export function timeAt(day: Date, fraction: number): Date {
  return new Date(day.getTime() + Math.min(1, Math.max(0, fraction)) * DAY_MS);
}

/** Zoom levels of the timeline, in ms of visible span. */
export const ZOOM_LEVELS = [DAY_MS, 6 * 3_600_000, 3_600_000] as const;

/**
 * The visible window for a zoom span centred on `center`, kept inside the
 * day. The whole day is always [d0, d0 + DAY_MS).
 */
export function zoomWindow(day: Date, span: number, center: number | null): [number, number] {
  const d0 = day.getTime();
  if (span >= DAY_MS) return [d0, d0 + DAY_MS];
  const mid = center ?? d0 + DAY_MS / 2;
  const from = Math.min(Math.max(mid - span / 2, d0), d0 + DAY_MS - span);
  return [from, from + span];
}

export type Tick = { at: number; major: boolean; label?: string };

/**
 * Ruler ticks for a window: a label on every major tick, minor ticks in
 * between. Steps scale with the span so labels never crowd: 24 h → 2 h/1 h,
 * 6 h → 30 min/10 min, 1 h → 10 min/5 min, shorter → 5 min/1 min.
 */
export function ticks([from, to]: [number, number]): Tick[] {
  const span = to - from;
  const [major, minor] = span > 12 * 3_600_000 ? [2 * 3_600_000, 3_600_000] : span > 3 * 3_600_000 ? [1_800_000, 600_000] : span > 1_800_000 ? [600_000, 300_000] : [300_000, 60_000];
  const out: Tick[] = [];
  const offset = new Date(from).getTimezoneOffset() * 60_000; // align ticks to local clock time
  for (let t = Math.ceil((from - offset) / minor) * minor + offset; t <= to; t += minor) {
    const isMajor = (t - offset) % major === 0;
    out.push({ at: t, major: isMajor, ...(isMajor ? { label: clock(new Date(t)) } : {}) });
  }
  return out;
}

/** `HH:MM` in the browser's zone. */
export function clock(d: Date): string {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** Whether `t` falls inside recorded footage; returns the range or null. */
export function recordedAt(ranges: Array<[number, number]>, t: Date): [number, number] | null {
  const ms = t.getTime();
  return ranges.find(([a, b]) => ms >= a && ms < b) ?? null;
}

/** The first recorded moment at or after `t` on the day, else null. */
export function nextRecorded(ranges: Array<[number, number]>, t: Date): Date | null {
  const ms = t.getTime();
  const hit = recordedAt(ranges, t);
  if (hit) return t;
  const next = ranges.find(([a]) => a >= ms);
  return next ? new Date(next[0]) : null;
}

export function useCalendar(client: HiveClient | null, camera: string | undefined, stream: StreamName) {
  return useQuery({
    queryKey: ["archive", camera, stream, "calendar"],
    queryFn: async () => (unwrapHive(await client!.GET("/v0/cameras/{id}/archive/calendar", { params: { path: { id: camera! }, query: { stream, tz: browserZone() } } })) as Array<{ day?: string }>).map((d) => d.day ?? "").filter(Boolean),
    enabled: !!client && !!camera,
    staleTime: 60_000,
  });
}

export function useTimeline(client: HiveClient | null, camera: string | undefined, stream: StreamName, day: string | undefined) {
  const from = day ? dayStart(day) : undefined;
  return useQuery({
    queryKey: ["archive", camera, stream, "timeline", day],
    queryFn: async () => unwrapHive(await client!.GET("/v0/cameras/{id}/archive/timeline", { params: { path: { id: camera! }, query: { stream, from: from!.toISOString(), to: new Date(from!.getTime() + DAY_MS).toISOString() } } })) as Segment[],
    enabled: !!client && !!camera && !!from,
    refetchInterval: 60_000,
  });
}

export function useEvents(client: HiveClient | null, camera: string | undefined, day: string | undefined) {
  const from = day ? dayStart(day) : undefined;
  return useQuery({
    queryKey: ["archive", camera, "events", day],
    queryFn: async () => unwrapHive(await client!.GET("/v0/cameras/{id}/events", { params: { path: { id: camera! }, query: { from: from!.toISOString(), to: new Date(from!.getTime() + DAY_MS).toISOString() } } })) as ArchiveEvent[],
    enabled: !!client && !!camera && !!from,
    refetchInterval: 60_000,
  });
}

export function useExports(client: HiveClient | null) {
  return useQuery({
    queryKey: ["exports"],
    queryFn: async () => unwrapHive(await client!.GET("/v0/exports")) as ExportJob[],
    enabled: !!client,
    refetchInterval: (q) => ((q.state.data ?? []).some((j) => j.status === "running") ? 3000 : false),
  });
}

/** Minutes the grant allows per export; -1 means unlimited (cap at a day). */
export function exportCapMinutes(exportMaxMinutes: number | undefined): number {
  if (exportMaxMinutes === undefined || exportMaxMinutes < 0) return 24 * 60;
  return exportMaxMinutes;
}
