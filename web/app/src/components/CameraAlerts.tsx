import { useTranslation } from "react-i18next";
import { errorMessage } from "@/lib/errors";
import { cameraPrefs, COOLDOWNS, useNotifySettings, useSaveNotifySettings, withCamera, type CameraNotify } from "@/lib/notify";

/** The owner's motion notifications for one camera (ADR-0021); saved on change. */
export function CameraAlerts({ hiveId, cameraId }: { hiveId: string; cameraId: string }) {
  const { t } = useTranslation();
  const settings = useNotifySettings();
  const save = useSaveNotifySettings();
  if (!settings.data) return settings.isError ? <p role="alert" className="error text-sm">{errorMessage(t, settings.error)}</p> : null;
  const doc = settings.data;
  const mine = cameraPrefs(doc, hiveId, cameraId);
  const change = (patch: Partial<CameraNotify>) => save.mutate(withCamera(doc, { ...mine, ...patch }));
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm" role="group" aria-label={t("notify.cameraTitle")}>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={mine.motion} disabled={save.isPending} onChange={(e) => change({ motion: e.target.checked })} />
        {t("notify.cameraMotion")}
      </label>
      <label className="muted" htmlFor={`cooldown-${cameraId}`}>{t("notify.cooldown")}</label>
      <select id={`cooldown-${cameraId}`} className="input input-sm !w-auto" value={mine.cooldown_minutes} disabled={save.isPending || !mine.motion}
        onChange={(e) => change({ cooldown_minutes: Number(e.target.value) })}>
        {COOLDOWNS.map((m) => <option key={m} value={m}>{t("notify.minutes", { count: m })}</option>)}
      </select>
      {!doc.events.motion.email && !doc.events.motion.push && <span className="muted">{t("notify.motionOffEverywhere")}</span>}
      {save.isError && <span role="alert" className="error">{errorMessage(t, save.error)}</span>}
    </div>
  );
}
