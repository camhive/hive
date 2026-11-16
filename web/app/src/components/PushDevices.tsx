import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { errorMessage } from "@/lib/errors";
import { browserSupport, enablePush, thisDevice, usePush, useRemoveDevice, useTestPush } from "@/lib/push";

/** The account's push devices, enabling this one, and a test (ADR-0021 §7). */
export function PushDevices() {
  const { t } = useTranslation();
  const push = usePush();
  const remove = useRemoveDevice();
  const test = useTestPush();
  const qc = useQueryClient();
  const [here, setHere] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const support = browserSupport();
  useEffect(() => {
    const data = push.data;
    if (!data?.public_key) return;
    void thisDevice(data.devices, data.public_key).then((id) => setHere(id !== null), () => setHere(false));
  }, [push.data]);

  if (!push.data) return push.isError ? <p role="alert" className="error">{errorMessage(t, push.error)}</p> : null;
  const { configured, public_key, devices, limit } = push.data;

  async function enable() {
    setError(null);
    try {
      await enablePush(public_key ?? "");
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(err instanceof Error && err.message.includes("permission") ? t("push.permissionRefused") : errorMessage(t, err));
    }
  }

  return (
    <section className="card space-y-3" aria-label={t("push.title")}>
      <h2 className="text-lg font-semibold">{t("push.title")}</h2>
      {!configured && <p className="muted text-sm">{t("push.notConfigured")}</p>}
      {configured && support === "unsupported" && <p className="muted text-sm">{t("push.unsupported")}</p>}
      {configured && support === "install-first" && <p className="muted text-sm">{t("push.installFirst")}</p>}
      {configured && support === "ok" && !here && <button type="button" className="btn btn-secondary btn-sm" onClick={() => void enable()}>{t("push.enable")}</button>}
      {configured && here && <p className="muted text-sm">{t("push.enabledHere")}</p>}
      <p className="muted text-xs">{limit < 0 ? t("push.limitUnlimited") : t("push.limit", { count: limit })}</p>
      {devices.length > 0 && (
        <ul className="space-y-2 text-sm">
          {devices.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center justify-between gap-2">
              <span>
                {d.label || t("push.unnamed")}
                {!d.active && <span className="muted"> · {t("push.sleeping")}</span>}
                {d.last_used_at && <span className="muted"> · {t("push.lastUsed", { when: new Date(d.last_used_at).toLocaleString() })}</span>}
              </span>
              <button type="button" className="btn btn-ghost btn-sm" aria-label={t("push.removeNamed", { name: d.label || t("push.unnamed") })}
                disabled={remove.isPending} onClick={() => remove.mutate(d.id)}>{t("push.remove")}</button>
            </li>
          ))}
        </ul>
      )}
      {configured && devices.length > 0 && (
        <button type="button" className="btn btn-secondary btn-sm" disabled={test.isPending} onClick={() => test.mutate()}>{t("push.test")}</button>
      )}
      {test.isSuccess && <p role="status" className="muted text-sm">{t("push.testSent", { count: test.data })}</p>}
      {(error || test.isError || remove.isError) && <p role="alert" className="error text-sm">{error ?? errorMessage(t, test.error ?? remove.error)}</p>}
    </section>
  );
}
