import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { useTranslation } from "react-i18next";
import { DAY_MS, MIN_SPAN, ZOOM_LEVELS, clock, clockSeconds, dayStart, ticks, zoomAround, zoomWindow, type ActivityPoint } from "@/lib/archive";

type Props = {
  day: string;
  ranges: Array<[number, number]>;
  events: number[];
  position: Date | null;
  onSeek: (at: Date) => void;
  /** Activity and sound runs (ADR-0015); recorded spans turn pale behind them. */
  activity?: { video: ActivityPoint[][]; sound: ActivityPoint[][] };
};

/** A pointer gesture on the bar: a drag from `start`, or a pinch once two pointers are down. */
type Gesture = {
  pointers: Map<number, number>; // pointer id → clientX
  startX: number;
  start: number; // the time under the playhead when the drag began
  moved: boolean;
  pinch?: { dist: number; window: [number, number] };
};

/** Pixels a pointer moves before a press becomes a drag. */
const DRAG_SLOP = 4;

/**
 * One day of footage as a ruler: recorded spans, activity waves, events, the
 * playhead and a large readout of its time. Zoomed in, the window follows
 * the playhead and dragging pulls the footage under it; over the whole day a
 * drag moves the playhead. A click seeks to that spot, the wheel or two
 * fingers zoom around the pointer, the arrow keys step a minute (ten with
 * Shift) and + / − zoom.
 */
export function Timeline({ day, ranges, events, position, onSeek, activity }: Props) {
  const { t } = useTranslation();
  const d0 = dayStart(day).getTime();
  const [span, setSpan] = useState<number>(DAY_MS);
  const [center, setCenter] = useState<number | null>(null); // where the user left the view
  const [follow, setFollow] = useState(true); // the view follows the playhead
  const [hover, setHover] = useState<number | null>(null);
  const [target, setTarget] = useState<number | null>(null); // the time a drag would seek to
  const bar = useRef<HTMLDivElement>(null);
  const gesture = useRef<Gesture | null>(null);
  const dragged = useRef(false); // swallow the click that ends a drag
  const pos = position?.getTime() ?? null;
  const head = target ?? pos;
  const [from, to] = useMemo(() => zoomWindow(dayStart(day), span, target ?? (follow ? pos : center ?? pos)), [day, span, target, follow, pos, center]);
  const [width, setWidth] = useState<number | undefined>(undefined);
  const marks = useMemo(() => ticks([from, to], width), [from, to, width]);
  const pct = (ms: number) => `${((ms - from) / (to - from)) * 100}%`;
  const visible = (a: number, b: number) => b > from && a < to;
  const inDay = (ms: number) => Math.min(Math.max(ms, d0), d0 + DAY_MS - 1);
  const profiled = (activity?.video.length ?? 0) > 0;
  // Waves in a 1000 × 100 box stretched over the bar; y grows downwards.
  const x = (ms: number) => (((ms - from) / (to - from)) * 1000).toFixed(1);
  const shownRuns = (runs: ActivityPoint[][] | undefined) => (runs ?? []).filter((r) => r.length > 0 && visible(r[0]!.at, r[r.length - 1]!.at));
  const area = (r: ActivityPoint[]) => `M${x(r[0]!.at)},100 ${r.map((p) => `L${x(p.at)},${(100 - p.v * 92).toFixed(1)}`).join(" ")} L${x(r[r.length - 1]!.at)},100 Z`;
  const line = (r: ActivityPoint[]) => r.map((p, k) => `${k ? "L" : "M"}${x(p.at)},${(100 - p.v * 92).toFixed(1)}`).join(" ");
  const fractionAt = (clientX: number) => {
    const r = bar.current?.getBoundingClientRect();
    return r?.width ? Math.min(1, Math.max(0, (clientX - r.left) / r.width)) : 0;
  };
  const timeAtX = (clientX: number, [a, b]: [number, number] = [from, to]) => a + fractionAt(clientX) * (b - a);
  const barWidth = () => bar.current?.getBoundingClientRect().width || 1;

  function zoom(anchor: number, factor: number, window: [number, number] = [from, to]) {
    const [s, c] = zoomAround(dayStart(day), window, anchor, factor);
    setSpan(s);
    setCenter(c);
    setFollow(false);
  }

  function pickSpan(z: number) {
    setSpan(z);
    setFollow(true);
  }

  function seek(at: number) {
    setFollow(true);
    onSeek(new Date(at));
  }

  const onKey = (e: KeyboardEvent<HTMLElement>) => {
    if (e.key === "+" || e.key === "=") zoom(pos ?? (from + to) / 2, 0.5);
    if (e.key === "-") zoom(pos ?? (from + to) / 2, 2);
    if (!pos) return;
    const step = e.shiftKey ? 600_000 : 60_000;
    if (e.key === "ArrowRight") seek(Math.min(pos + step, d0 + DAY_MS - 1));
    if (e.key === "ArrowLeft") seek(Math.max(pos - step, d0));
  };

  const onPointerDown = (e: PointerEvent<HTMLElement>) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    e.currentTarget.setPointerCapture?.(e.pointerId);
    dragged.current = false;
    const g: Gesture = (gesture.current ??= { pointers: new Map<number, number>(), startX: e.clientX, start: pos ?? (from + to) / 2, moved: false });
    g.pointers.set(e.pointerId, e.clientX);
    if (g.pointers.size === 2) {
      const [a, b] = [...g.pointers.values()] as [number, number];
      g.pinch = { dist: Math.abs(a - b) || 1, window: [from, to] };
      g.moved = true;
      setTarget(null);
    }
  };

  const onPointerMove = (e: PointerEvent<HTMLElement>) => {
    const g = gesture.current;
    if (!g?.pointers.has(e.pointerId)) {
      if (e.pointerType === "mouse") setHover(timeAtX(e.clientX));
      return;
    }
    g.pointers.set(e.pointerId, e.clientX);
    if (g.pinch) {
      if (g.pointers.size < 2) return;
      const [a, b] = [...g.pointers.values()] as [number, number];
      zoom(timeAtX((a + b) / 2, g.pinch.window), g.pinch.dist / (Math.abs(a - b) || 1), g.pinch.window);
      return;
    }
    const dx = e.clientX - g.startX;
    if (!g.moved && Math.abs(dx) < DRAG_SLOP) return;
    g.moved = true;
    setHover(null);
    // Over the whole day the playhead follows the pointer; zoomed in, the footage does.
    const sign = span >= DAY_MS ? 1 : -1;
    setTarget(inDay(g.start + (sign * dx * span) / barWidth()));
  };

  const onPointerUp = (e: PointerEvent<HTMLElement>) => {
    const g = gesture.current;
    if (!g?.pointers.delete(e.pointerId) || g.pointers.size > 0) return;
    gesture.current = null;
    dragged.current = g.moved;
    if (target !== null && !g.pinch && e.type === "pointerup") seek(target);
    setTarget(null);
  };

  const onClick = (e: MouseEvent<HTMLElement>) => {
    if (dragged.current) {
      dragged.current = false;
      return;
    }
    seek(timeAtX(e.clientX));
  };

  // The wheel zooms around the pointer (and pans sideways on a trackpad). It
  // needs a non-passive listener to keep the page from scrolling, so it is
  // added by hand and reads the current state through a ref.
  const onWheel = useRef<(e: WheelEvent) => void>(() => undefined);
  onWheel.current = (e) => {
    if (Math.abs(e.deltaX) > Math.abs(e.deltaY)) {
      if (span >= DAY_MS) return;
      e.preventDefault();
      setCenter(inDay((from + to) / 2 + (e.deltaX * span) / barWidth()));
      setFollow(false);
      return;
    }
    if ((e.deltaY < 0 && span <= MIN_SPAN) || (e.deltaY > 0 && span >= DAY_MS) || e.deltaY === 0) return;
    e.preventDefault();
    zoom(timeAtX(e.clientX), Math.exp(e.deltaY * 0.003));
  };
  useEffect(() => {
    const el = bar.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => setWidth(entry?.contentRect.width || undefined));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  useEffect(() => {
    const el = bar.current;
    if (!el) return;
    const listener = (e: WheelEvent) => onWheel.current(e);
    el.addEventListener("wheel", listener, { passive: false });
    return () => el.removeEventListener("wheel", listener);
  }, []);

  return (
    <div className="space-y-1 select-none">
      <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-2 text-xs">
        <span className="muted" data-testid="window">{clock(new Date(from))} – {to >= d0 + DAY_MS ? "24:00" : clock(new Date(to))}</span>
        <span className="text-base font-semibold tabular-nums" data-testid="readout" aria-live="off">{head !== null ? clockSeconds(new Date(head)) : "--:--:--"}</span>
        <div role="group" aria-label={t("archive.zoom")} className="seg seg-sm justify-self-end">
          {ZOOM_LEVELS.map((z) => (
            <button key={z} type="button" aria-pressed={span === z} onClick={() => pickSpan(z)}>
              {z >= DAY_MS ? "24h" : `${z / 3_600_000}h`}
            </button>
          ))}
        </div>
      </div>

      <div ref={bar} role="slider" tabIndex={0} aria-label={t("archive.timeline")} aria-valuemin={0} aria-valuemax={24} aria-valuenow={pos ? ((pos - d0) / DAY_MS) * 24 : 0}
        aria-valuetext={position ? clock(position) : undefined}
        className={`relative h-20 rounded-lg overflow-hidden touch-none ${target !== null ? "cursor-grabbing" : span < DAY_MS ? "cursor-grab" : "cursor-crosshair"}`}
        style={{ background: "var(--sunken)" }} data-testid="timeline"
        onClick={onClick} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={onPointerUp}
        onPointerLeave={() => setHover(null)} onKeyDown={onKey}>
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
        {hover !== null && target === null && (
          <span className="absolute top-0 bottom-5 w-px pointer-events-none" style={{ left: pct(hover), background: "var(--fg)", opacity: 0.4 }}>
            <span className="absolute top-1 left-1 text-[10px] px-1 rounded pointer-events-none" style={{ background: "var(--card)", color: "var(--fg)" }}>{clock(new Date(hover))}</span>
          </span>
        )}
        {/* playhead */}
        {head !== null && visible(head, head) && (
          <span className="absolute top-0 bottom-0 w-0.5 pointer-events-none" style={{ left: pct(head), background: "var(--fg)" }} data-testid="playhead">
            <span className="absolute top-1 left-1 text-[10px] font-semibold px-1.5 py-0.5 rounded tabular-nums" style={{ background: "var(--fg)", color: "var(--bg)" }}>{clock(new Date(head))}</span>
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
