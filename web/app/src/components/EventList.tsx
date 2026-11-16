import { useTranslation } from "react-i18next";
import { clock, type TimelineMark } from "@/lib/archive";
import { eventLabel } from "@/components/Timeline";

/** How long an ended event lasted, in the reader's language: "12 sec", "5 min", "2 hr". */
export function lasted(ms: number, lang: string): string {
  const [value, unit] = ms < 60_000 ? [Math.max(1, Math.round(ms / 1000)), "second"] : ms < 3_600_000 ? [Math.round(ms / 60_000), "minute"] : [Math.round(ms / 360_000) / 10, "hour"];
  return new Intl.NumberFormat(lang, { style: "unit", unit, unitDisplay: "short" }).format(value);
}

/**
 * The day's events under the timeline, newest first (ADR-0020): motion with
 * how long it lasted and where it came from, storage.low, camera status.
 * Picking one plays the recording from its start.
 */
export function EventList({ marks, onSeek }: { marks: TimelineMark[]; onSeek: (at: Date) => void }) {
  const { t, i18n } = useTranslation();
  if (marks.length === 0) return null;
  const newest = [...marks].reverse();
  return (
    <details className="text-sm" data-testid="events">
      <summary className="muted cursor-pointer select-none">{t("archive.events.title", { count: marks.length })}</summary>
      <ul className="mt-2 max-h-56 overflow-auto divide-y divide-[var(--border)]">
        {newest.map((m) => (
          <li key={m.id}>
            <button type="button" className="w-full text-left py-1.5 flex flex-wrap gap-x-3 hover:bg-[var(--sunken)]" onClick={() => onSeek(new Date(m.at))}>
              <span className="tabular-nums">{clock(new Date(m.at))}</span>
              <span className="font-medium">{t(eventLabel(m.kind), { defaultValue: m.kind })}</span>
              {m.ongoing ? <span className="muted">{t("archive.events.ongoing")}</span> : m.end !== undefined && <span className="muted">{lasted(m.end - m.at, i18n.language)}</span>}
              {m.source && <span className="muted">{t(`archive.events.source.${m.source}`, { defaultValue: "" })}</span>}
            </button>
          </li>
        ))}
      </ul>
    </details>
  );
}
