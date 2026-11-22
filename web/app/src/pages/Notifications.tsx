import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useMe } from "@/lib/session";
import { errorMessage } from "@/lib/errors";
import { PushDevices } from "@/components/PushDevices";
import { GROUPS, MODES, localZone, useNotifySettings, useSaveNotifySettings, type NotifyChannel, type NotifyMode, type NotifySettings } from "@/lib/notify";

/** What reaches the account and how (ADR-0021): channels and when per event group, quiet hours, and the week's counts (#157). */
export function Notifications() {
  const { t } = useTranslation();
  const me = useMe();
  const settings = useNotifySettings();
  if (me.isPending || settings.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (!settings.data) return <p role="alert" className="card error">{errorMessage(t, settings.error)}</p>;
  return (
    <div className="space-y-6">
      <div>
        <h1 className="page-title">{t("notify.title")}</h1>
        <p className="muted text-sm mt-1">{t("notify.intro", { email: me.data.email })}</p>
      </div>
      <PushDevices />
      <SettingsForm initial={settings.data} />
    </div>
  );
}

function SettingsForm({ initial }: { initial: NotifySettings }) {
  const { t, i18n } = useTranslation();
  const save = useSaveNotifySettings();
  const [doc, setDoc] = useState(initial);
  const [saved, setSaved] = useState(false);
  useEffect(() => setSaved(false), [doc]);
  const channels = (initial.channels ?? []) as NotifyChannel[];
  const quiet = doc.quiet_hours ?? null;

  async function submit(e: FormEvent) {
    e.preventDefault();
    await save.mutateAsync(doc);
    setSaved(true);
  }
  const toggle = (g: (typeof GROUPS)[number], c: NotifyChannel, on: boolean) =>
    setDoc({ ...doc, events: { ...doc.events, [g]: { ...doc.events[g], [c]: on } } });
  const setQuiet = (q: NotifySettings["quiet_hours"]) => setDoc({ ...doc, quiet_hours: q });
  const setMotionMode = (mode: NotifyMode) => setDoc({ ...doc, events: { ...doc.events, motion: { ...doc.events.motion, mode } } });
  const counts = initial.counts;
  const number = (n: number) => n.toLocaleString(i18n.language);

  return (
    <form className="space-y-6" onSubmit={(e) => void submit(e).catch(() => undefined)}>
      <section className="card" aria-label={t("notify.events")}>
        <h2 className="text-lg font-semibold mb-3">{t("notify.events")}</h2>
        {counts && <p className="text-sm mb-3">{t("notify.counts", { episodes: number(counts.episodes_week), pushes: number(counts.pushes_week) })}</p>}
        <table className="w-full text-sm">
          <thead>
            <tr>
              <th className="text-left py-2 muted font-normal">{t("notify.event")}</th>
              {channels.map((c) => <th key={c} className="text-left py-2 muted font-normal">{t(`notify.channel.${c}`)}</th>)}
              <th className="text-left py-2 muted font-normal">{t("notify.mode")}</th>
            </tr>
          </thead>
          <tbody>
            {GROUPS.map((g) => (
              <tr key={g} className="border-t" style={{ borderColor: "var(--border)" }}>
                <td className="py-2">{t(`notify.group.${g}`)}<div className="muted text-xs">{t(`notify.groupHelp.${g}`)}</div></td>
                {channels.map((c) => (
                  <td key={c} className="py-2">
                    <input type="checkbox" aria-label={`${t(`notify.group.${g}`)}: ${t(`notify.channel.${c}`)}`} checked={doc.events[g][c]}
                      onChange={(e) => toggle(g, c, e.target.checked)} />
                  </td>
                ))}
                <td className="py-2">
                  {g === "motion" ? (
                    <select className="input input-sm !w-auto" aria-label={t("notify.modeLabel", { group: t(`notify.group.${g}`) })} value={doc.events.motion.mode ?? "immediate"}
                      onChange={(e) => setMotionMode(e.target.value as NotifyMode)}>
                      {MODES.map((m) => <option key={m} value={m}>{t(`notify.modes.${m}`)}</option>)}
                    </select>
                  ) : <span className="muted">{t("notify.modes.immediate")}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="muted text-xs mt-3">{t("notify.modesHelp")}</p>
        <p className="muted text-xs mt-1">{t("notify.perCamera")}</p>
      </section>

      <section className="card space-y-3" aria-label={t("notify.quiet")}>
        <label className="flex items-center gap-2 font-semibold">
          <input type="checkbox" checked={!!quiet} onChange={(e) => setQuiet(e.target.checked ? { start: "22:00", end: "07:00" } : null)} />
          {t("notify.quiet")}
        </label>
        <p className="muted text-sm">{t("notify.quietHelp")}</p>
        {quiet && (
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <label htmlFor="quiet-start">{t("notify.from")}</label>
            <input id="quiet-start" type="time" className="input input-sm !w-auto" value={quiet.start} required onChange={(e) => setQuiet({ ...quiet, start: e.target.value })} />
            <label htmlFor="quiet-end">{t("notify.until")}</label>
            <input id="quiet-end" type="time" className="input input-sm !w-auto" value={quiet.end} required onChange={(e) => setQuiet({ ...quiet, end: e.target.value })} />
            <span className="muted">{localZone()}</span>
          </div>
        )}
      </section>

      {save.isError && <p role="alert" className="error">{errorMessage(t, save.error)}</p>}
      {saved && <p role="status" className="muted">{t("notify.saved")}</p>}
      <button className="btn" disabled={save.isPending}>{t("notify.save")}</button>
    </form>
  );
}
