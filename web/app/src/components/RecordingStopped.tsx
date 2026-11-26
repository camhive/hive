import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { unwrapHive, type HiveClient } from "@camhive/api";
import { recordingStop, type RecordingStop } from "@/lib/recording";
import type { Hive } from "@/lib/hives";

/**
 * The red banner of a hive that stopped recording (#172), on the hive page
 * and above a camera's recordings: since when and why, from the hive when
 * the app reaches it (checked every minute), else from the cloud; the
 * cloud's word only once the hive's query failed or there is no session.
 */
export function RecordingStoppedNotice({ hive, client }: { hive: Hive; client: HiveClient | null }) {
  const { t, i18n } = useTranslation();
  const stop = useRecordingStop(hive, client);
  if (!stop) return null;
  const reason = t(`recording.reason.${stop.reason}`, { defaultValue: t("recording.reason.unknown") });
  return (
    <section role="alert" className="card error text-sm space-y-1" data-testid="recording-stopped" data-source={stop.source}>
      <p className="font-medium">{t("recording.stopped", { time: new Date(stop.since).toLocaleString(i18n.language), reason })}</p>
      <p>{t(stop.reason === "archive_changed" ? "recording.restart" : "recording.hint")}</p>
      {stop.source === "cloud" && <p className="muted">{t("recording.lastWord")}</p>}
    </section>
  );
}

/**
 * The stop to show, the hive's own answer (GET /v0/hive, every minute)
 * when the app has a session, else the cloud's; nothing while the hive's
 * answer is still coming, so the cloud's older word never flashes.
 */
export function useRecordingStop(hive: Hive, client: HiveClient | null): RecordingStop | undefined {
  const info = useQuery({
    queryKey: ["hive", hive.id, "info"],
    queryFn: async () => unwrapHive(await client!.GET("/v0/hive")),
    enabled: !!client,
    refetchInterval: 60_000,
    retry: false,
  });
  if (client && info.isPending) return undefined;
  return recordingStop(hive, info.isSuccess ? info.data : undefined);
}
