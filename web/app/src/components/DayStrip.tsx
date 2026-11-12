import { useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { dayStrip } from "@/lib/archive";

type Props = {
  /** Days with footage, `YYYY-MM-DD`. */
  footage: string[];
  day: string;
  today: string;
  onPick: (day: string) => void;
};

/**
 * A row of days from the oldest footage to today: weekday and date, the
 * month over its first day, days with footage bright and pickable, the
 * chosen one filled. Scrolls sideways and keeps the chosen day in view.
 */
export function DayStrip({ footage, day, today, onPick }: Props) {
  const { t, i18n } = useTranslation();
  const days = useMemo(() => dayStrip(footage, today), [footage, today]);
  const chosen = useRef<HTMLButtonElement>(null);
  useEffect(() => { chosen.current?.scrollIntoView?.({ block: "nearest", inline: "center" }); }, [day, days.length]);
  const lang = i18n.language;
  return (
    <div className="flex gap-1 overflow-x-auto pb-1" data-testid="calendar" role="group" aria-label={t("archive.calendar")}>
      {days.map((d) => {
        const selected = d.key === day;
        const pickable = d.footage || d.key === today;
        return (
          <div key={d.key} className="flex flex-col items-center shrink-0 w-10">
            <span className="text-[10px] leading-4 muted whitespace-nowrap h-4">{d.monthStart ? d.date.toLocaleDateString(lang, { month: "short" }) : ""}</span>
            <span className="text-[10px] leading-4 muted">{d.date.toLocaleDateString(lang, { weekday: "short" })}</span>
            <button ref={selected ? chosen : undefined} type="button" aria-pressed={selected} disabled={!pickable} onClick={() => onPick(d.key)}
              aria-label={d.date.toLocaleDateString(lang, { weekday: "long", day: "numeric", month: "long", year: "numeric" })}
              className="w-8 h-8 rounded-full text-sm tabular-nums font-medium disabled:cursor-default"
              style={selected ? { background: "var(--accent)", color: "#1a1300" } : d.footage ? { background: "var(--accent-soft)", color: "var(--accent-text)" } : { color: "var(--muted)", opacity: pickable ? 1 : 0.5, border: d.key === today ? "1px solid var(--border)" : undefined }}>
              {d.date.getDate()}
            </button>
          </div>
        );
      })}
    </div>
  );
}
