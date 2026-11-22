// Camera care (#163): what the hive saw about a camera, one sentence per
// reason, each with what the owner can do. The hive's English suggestion
// is not shown; the app speaks the reader's language.
import type { TFunction } from "i18next";
import type { CameraCare, CareReason } from "@camhive/api";

type Track = { codec?: string; width?: number; height?: number };

export interface CareFact {
  reason: CareReason;
  text: string;
  advice: string;
}

/** The facts behind each reason of a degraded camera, in the hive's order. */
export function careFacts(care: CameraCare, t: TFunction): CareFact[] {
  return care.reasons.map(({ reason }) => ({ reason, text: factText(reason, care.signals, t), advice: t(`cameraCare.advice.${reason}`) }));
}

function factText(reason: CareReason, s: CameraCare["signals"], t: TFunction): string {
  switch (reason) {
    case "bitrate_drop": {
      const st = s.streams.find((x) => x.usual_kbps && x.last_hour_kbps && x.last_hour_kbps < x.usual_kbps) ?? s.streams[0];
      return t("cameraCare.fact.bitrate_drop", { stream: streamName(st?.stream, t), pct: percent(st?.last_hour_kbps, st?.usual_kbps) });
    }
    case "frame_loss":
      return s.late_s > 0 && s.missing_s <= s.late_s
        ? t("cameraCare.fact.late", { count: Math.max(1, Math.round(s.late_s / 60)) })
        : t("cameraCare.fact.frame_loss", { seconds: Math.round(s.missing_s) });
    case "reconnects":
      return t("cameraCare.fact.reconnects", { count: s.reconnects_24h });
    case "clock_drift":
      return t("cameraCare.fact.clock_drift", { seconds: Math.abs(Math.round(s.clock_drift_s ?? 0)) });
    case "offline_pattern":
      return t("cameraCare.fact.offline_pattern", { hours: listOf(s.pattern_hours.map((h) => `${String(h).padStart(2, "0")}:00`), t) });
    case "night_picture": {
      const st = s.streams.find((x) => x.night && x.day_kbps) ?? s.streams[0];
      return t("cameraCare.fact.night_picture", { stream: streamName(st?.stream, t), pct: percent(st?.last_hour_kbps, st?.day_kbps) });
    }
    case "stream_changed": {
      const ch = s.stream_change;
      return t("cameraCare.fact.stream_changed", { stream: streamName(ch?.stream, t), from: trackName(ch?.from), to: trackName(ch?.to) });
    }
  }
}

function streamName(stream: string | undefined, t: TFunction): string {
  return stream === "sub" ? t("cameraCare.stream.sub") : t("cameraCare.stream.main");
}

function percent(part: number | undefined, whole: number | undefined): number {
  return part && whole ? Math.round((part / whole) * 100) : 0;
}

function trackName(v: Track | undefined): string {
  if (!v?.codec) return "?";
  return v.width ? `${v.codec} ${v.width}×${v.height}` : v.codec;
}

function listOf(items: string[], t: TFunction): string {
  if (items.length <= 1) return items.join("");
  return `${items.slice(0, -1).join(", ")} ${t("cameraCare.and")} ${items[items.length - 1]}`;
}
