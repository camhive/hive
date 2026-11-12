import { useMemo, useState, type KeyboardEvent, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { DAY_MS, ZOOM_LEVELS, clock, dayStart, ticks, zoomWindow, type ActivityPoint } from "@/lib/archive";

type Props = {
  day: string;
  ranges: Array<[number, number]>;
  events: number[];
  position: Date | null;
  onSeek: (at: Date) => void;
  /** Activity and sound runs (ADR-0015); recorded spans turn pale behind them. */
  activity?: { video: ActivityPoint[][]; sound: ActivityPoint[][] };
};

/**
 * One day of footage as a ruler: recorded spans as blocks, events as marks,
 * the playhead with its time, a hover readout, and 24 h / 6 h / 1 h zoom
 * around the playhead. Click or use the arrow keys to seek.
 */
export function Timeline({ day, ranges, events, position, onSeek, activity }: Props) {
  const { t } = useTranslation();
  const d0 = dayStart(day).getTime();
  const [span, setSpan] = useState<number>(DAY_MS);
  const [hover, setHover] = useState<number | null>(null);
  const pos = position?.getTime() ?? null;
  const [from, to] = useMemo(() => zoomWindow(dayStart(day), span, pos), [day, span, pos]);
  const marks = useMemo(() => ticks([from, to]), [from, to]);
  const pct = (ms: number) => `${((ms - from) / (to - from)) * 100}%`;
  const visible = (a: number, b: number) => b > from && a < to;
  const profiled = (activity?.video.length ?? 0) > 0;
  // Waves in a 1000 × 100 box stretched over the bar; y grows downwards.
  const x = (ms: number) => (((ms - from) / (to - from)) * 1000).toFixed(1);
  const shownRuns = (runs: ActivityPoint[][] | undefined) => (runs ?? []).filter((r) => r.length > 0 && visible(r[0]!.at, r[r.length - 1]!.at));
  const area = (r: ActivityPoint[]) => `M${x(r[0]!.at)},100 ${r.map((p) => `L${x(p.at)},${(100 - p.v * 92).toFixed(1)}`).join(" ")} L${x(r[r.length - 1]!.at)},100 Z`;
  const line = (r: ActivityPoint[]) => r.map((p, k) => `${k ? "L" : "M"}${x(p.at)},${(100 - p.v * 92).toFixed(1)}`).join(" ");
  const timeAtX = (e: MouseEvent<HTMLElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const f = r.width ? Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) : 0;
    return from + f * (to - from);
  };
  const onKey = (e: KeyboardEvent<HTMLElement>) => {
    if (!pos) return;
    const step = e.shiftKey ? 600_000 : 60_000;
    if (e.key === "ArrowRight") onSeek(new Date(Math.min(pos + step, d0 + DAY_MS - 1)));
    if (e.key === "ArrowLeft") onSeek(new Date(Math.max(pos - step, d0)));
  };

  return (
    <div className="space-y-1 select-none">
      <div className="flex items-center justify-between text-xs">
        <span className="muted" data-testid="window">{clock(new Date(from))} – {to >= d0 + DAY_MS ? "24:00" : clock(new Date(to))}</span>
        <div role="group" aria-label={t("archive.zoom")} className="seg seg-sm">
          {ZOOM_LEVELS.map((z) => (
            <button key={z} type="button" aria-pressed={span === z} onClick={() => setSpan(z)}>
              {z >= DAY_MS ? "24h" : `${z / 3_600_000}h`}
            </button>
          ))}
        </div>
      </div>

      <div role="slider" tabIndex={0} aria-label={t("archive.timeline")} aria-valuemin={0} aria-valuemax={24} aria-valuenow={pos ? ((pos - d0) / DAY_MS) * 24 : 0}
        aria-valuetext={position ? clock(position) : undefined}
        className="relative h-20 rounded-lg cursor-crosshair overflow-hidden" style={{ background: "var(--sunken)" }} data-testid="timeline"
        onClick={(e) => onSeek(new Date(timeAtX(e)))} onMouseMove={(e) => setHover(timeAtX(e))} onMouseLeave={() => setHover(null)} onKeyDown={onKey}>
        {/* footage */}
        {ranges.filter(([a, b]) => visible(a, b)).map(([a, b]) => (
          <span key={a} className="absolute top-2 bottom-7 rounded-sm" style={{ left: pct(Math.max(a, from)), width: `calc(${pct(Math.min(b, to))} - ${pct(Math.max(a, from))})`, background: profiled ? "var(--accent-soft)" : "var(--accent)" }} data-testid="segment" />
        ))}
        {profiled && (
          <div className="absolute inset-x-0 top-2 bottom-7 pointer-events-none" aria-hidden="true">
            <svg className="w-full h-full" viewBox="0 0 1000 100" preserveAspectRatio="none">
              {shownRuns(activity?.video).map((r) => <path key={r[0]!.at} d={area(r)} fill="var(--accent)" data-testid="activity-video" />)}
              {shownRuns(activity?.sound).map((r) => <path key={r[0]!.at} d={line(r)} fill="none" stroke="var(--accent-text)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" data-testid="activity-sound" />)}
            </svg>
          </div>
        )}
        {/* events */}
        {events.filter((e) => Number.isFinite(e) && visible(e, e)).map((e, i) => (
          <span key={i} className="absolute bottom-5 w-0.5 h-3 rounded-sm" style={{ left: pct(e), background: "var(--danger)" }} data-testid="event" title={clock(new Date(e))} />
        ))}
        {/* ruler */}
        <div className="absolute inset-x-0 bottom-0 h-5 border-t" style={{ borderColor: "var(--border)", background: "var(--card)" }}>
          {marks.map((m) => (
            <span key={m.at} className="absolute top-0" style={{ left: pct(m.at) }}>
              <span className="block w-px" style={{ height: m.major ? 8 : 4, background: "var(--muted)" }} />
              {m.label && <span className="absolute top-1.5 -translate-x-1/2 text-[10px] leading-none muted whitespace-nowrap">{m.label}</span>}
            </span>
          ))}
        </div>
        {/* hover readout */}
        {hover !== null && (
          <span className="absolute top-0 bottom-5 w-px pointer-events-none" style={{ left: pct(hover), background: "var(--fg)", opacity: 0.4 }}>
            <span className="absolute top-1 left-1 text-[10px] px-1 rounded pointer-events-none" style={{ background: "var(--card)", color: "var(--fg)" }}>{clock(new Date(hover))}</span>
          </span>
        )}
        {/* playhead */}
        {pos !== null && visible(pos, pos) && (
          <span className="absolute top-0 bottom-0 w-0.5 pointer-events-none" style={{ left: pct(pos), background: "var(--fg)" }} data-testid="playhead">
            <span className="absolute top-1 left-1 text-[10px] font-semibold px-1.5 py-0.5 rounded tabular-nums" style={{ background: "var(--fg)", color: "var(--bg)" }}>{clock(position!)}</span>
          </span>
        )}
      </div>
      {ranges.length === 0 && <p className="muted text-xs">{t("archive.noFootage")}</p>}
      {profiled && (
        <p className="muted text-[11px] flex gap-4" data-testid="activity-legend">
          <span className="inline-flex items-center gap-1.5"><span className="inline-block w-3 h-2 rounded-sm" style={{ background: "var(--accent)" }} />{t("archive.activity")}</span>
          {(activity?.sound.length ?? 0) > 0 && <span className="inline-flex items-center gap-1.5"><span className="inline-block w-3 h-0.5" style={{ background: "var(--accent-text)" }} />{t("archive.soundLevel")}</span>}
        </p>
      )}
    </div>
  );
}
