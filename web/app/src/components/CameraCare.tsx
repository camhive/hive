import { useTranslation } from "react-i18next";
import type { CameraHealth, HiveClient } from "@camhive/api";
import type { Hive } from "@/lib/hives";
import { useHiveCapability } from "@/lib/capabilities";
import { useCameraCare } from "@/lib/hiveApi";
import { careFacts } from "@/lib/cameraCare";

/** The health line on the camera page (#163): what the hive saw and what to do. */
export function CameraCareLine({ hive, client, cameraId }: { hive: Hive; client: HiveClient | null; cameraId: string }) {
  const { t, i18n } = useTranslation();
  const cap = useHiveCapability(hive, "camera.care");
  const care = useCameraCare(hive, client, cameraId, cap.has).data;
  if (!cap.has || !care?.checked_at) return null;
  if (!care.degraded) {
    return <p className="text-sm muted" data-testid="camera-care">{t("cameraCare.title")}: {t("cameraCare.ok")}</p>;
  }
  const since = care.since ? new Date(care.since).toLocaleString(i18n.language, { dateStyle: "short", timeStyle: "short" }) : "";
  return (
    <div role="status" className="callout text-sm space-y-1" data-testid="camera-care">
      <p className="font-medium">{t("cameraCare.title")}: {since ? t("cameraCare.degradedSince", { time: since }) : t("cameraCare.degraded")}</p>
      <ul className="list-disc pl-5 space-y-0.5">
        {careFacts(care, t).map((f) => <li key={f.reason}>{f.text} <span className="muted">{f.advice}</span></li>)}
      </ul>
    </div>
  );
}

/** The small badge in the camera list and on tiles: shown while the hive says the camera is degraded. */
export function CareBadge({ health, onTile }: { health?: CameraHealth; onTile?: boolean }) {
  const { t } = useTranslation();
  const care = health?.care;
  if (!care?.degraded) return null;
  const label = t("cameraCare.badge", { reasons: care.reasons.map((r) => t(`cameraCare.reason.${r}`)).join(", ") });
  return (
    <span className={onTile ? "inline-flex items-center gap-1 rounded px-1.5 py-0.5 bg-black/50 text-amber-300" : "badge badge-danger"} title={label} aria-label={label} data-testid="care-badge">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M12 3 2 21h20L12 3z" /><path d="M12 10v5M12 18h.01" /></svg>
      {!onTile && t("cameraCare.degraded")}
    </span>
  );
}
