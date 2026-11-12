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

/** A value of a segment's activity profile placed in time, scaled 0..1. */
export type ActivityPoint = { at: number; v: number };

/** Quietest sound the timeline shows above its baseline, in dBFS. */
const SOUND_FLOOR_DB = -60;

/**
 * The day's activity profiles (ADR-0015) as runs of points, one run per
 * segment so gaps stay gaps. Video is scaled to the day's busiest value
 * (a hint of movement, relative to this camera); sound from SOUND_FLOOR_DB
 * to 0 dBFS. Points sit in the middle of their step.
 */
export function activityRuns(segments: Segment[], day: Date): { video: ActivityPoint[][]; sound: ActivityPoint[][] } {
  const d0 = day.getTime();
  const d1 = d0 + DAY_MS;
  const shown = segments.filter((s) => {
    const a = Date.parse(s.start ?? "");
    return s.activity && Number.isFinite(a) && a + (s.duration_ms ?? 0) > d0 && a < d1;
  });
  const busiest = Math.max(1, ...shown.flatMap((s) => s.activity!.video_kbps));
  const run = (s: Segment, values: number[], scale: (x: number) => number) => {
    const t = Date.parse(s.start!);
    const step = s.activity!.step_s * 1000;
    return values.map((x, k) => ({ at: t + k * step + step / 2, v: scale(x) })).filter((p) => p.at >= d0 && p.at < d1);
  };
  const clamp = (x: number) => Math.min(1, Math.max(0, x));
  return {
    video: shown.map((s) => run(s, s.activity!.video_kbps, (x) => clamp(x / busiest))),
    sound: shown.filter((s) => s.activity!.sound_db).map((s) => run(s, s.activity!.sound_db!, (db) => clamp((db - SOUND_FLOOR_DB) / -SOUND_FLOOR_DB))),
  };
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

/** The shortest window the timeline zooms to. */
export const MIN_SPAN = 600_000;

/**
 * Zooms a window by `factor` (< 1 in, > 1 out) around `anchor`, which keeps
 * its place on screen, as under a mouse wheel or between pinching fingers.
 * Returns the new span and centre for zoomWindow.
 */
export function zoomAround(day: Date, [from, to]: [number, number], anchor: number, factor: number): [number, number] {
  const span = Math.min(DAY_MS, Math.max(MIN_SPAN, (to - from) * factor));
  const start = anchor - ((anchor - from) / (to - from)) * span;
  const [a, b] = zoomWindow(day, span, start + span / 2);
  return [b - a, (a + b) / 2];
}

export type Tick = { at: number; major: boolean; label?: string };

/**
 * Ruler ticks for a window: a label on every major tick, minor ticks in
 * between. Steps scale with the span so labels never crowd: 24 h → 2 h/1 h,
 * 6 h → 30 min/10 min, 1 h → 10 min/5 min, shorter → 5 min/1 min. Given
 * the bar's width, only every n-th major tick keeps its label, n counted
 * from midnight so labels stay on the same times while the bar pans.
 */
/** Pixels a ruler label needs to stay clear of the next one. */
const LABEL_PX = 44;

export function ticks([from, to]: [number, number], widthPx?: number): Tick[] {
  const span = to - from;
  const [major, minor] = span > 12 * 3_600_000 ? [2 * 3_600_000, 3_600_000] : span > 3 * 3_600_000 ? [1_800_000, 600_000] : span > 1_800_000 ? [600_000, 300_000] : [300_000, 60_000];
  const out: Tick[] = [];
  const offset = new Date(from).getTimezoneOffset() * 60_000; // align ticks to local clock time
  const every = widthPx ? Math.max(1, Math.ceil(LABEL_PX / ((widthPx * major) / span))) : 1;
  for (let t = Math.ceil((from - offset) / minor) * minor + offset; t <= to; t += minor) {
    const isMajor = (t - offset) % major === 0;
    const labelled = isMajor && Math.round((t - offset) / major) % every === 0;
    out.push({ at: t, major: isMajor, ...(labelled ? { label: clock(new Date(t)) } : {}) });
  }
  return out;
}

/** `HH:MM` in the browser's zone. */
export function clock(d: Date): string {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** `HH:MM:SS` in the browser's zone. */
export function clockSeconds(d: Date): string {
  return `${clock(d)}:${String(d.getSeconds()).padStart(2, "0")}`;
}

export type StripDay = { key: string; date: Date; footage: boolean; monthStart: boolean };

/** Days the strip shows at most; older footage is reached by URL. */
const STRIP_MAX_DAYS = 366;

/**
 * The day strip: every day from the oldest with footage up to today, so gaps
 * show as days without footage, at most STRIP_MAX_DAYS. A month label goes
 * on the first day shown and on each 1st.
 */
export function dayStrip(footage: string[], today: string): StripDay[] {
  const has = new Set(footage);
  const end = dayStart(today);
  const oldest = footage.reduce((a, b) => (b < a ? b : a), today);
  const out: StripDay[] = [];
  for (const d = dayStart(oldest); d <= end; d.setDate(d.getDate() + 1)) {
    const key = dayKey(d);
    out.push({ key, date: new Date(d), footage: has.has(key), monthStart: d.getDate() === 1 });
  }
  const shown = out.slice(-STRIP_MAX_DAYS);
  if (shown[0]) shown[0].monthStart = true;
  return shown;
}

/** File name for a still of `camera` at `at`: `Camera-name_YYYY-MM-DD_HH-MM-SS.png`. */
export function snapshotName(camera: string, at: Date): string {
  const name = camera.trim().replace(/[^\p{L}\p{N}_-]+/gu, "-") || "camera";
  return `${name}_${dayKey(at)}_${clockSeconds(at).replaceAll(":", "-")}.png`;
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
