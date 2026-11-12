import { useNavigate, useParams } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { HiveNav } from "@/components/HiveNav";
import { useTranslation } from "react-i18next";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { useTransport } from "@/lib/useTransport";
import { lanBase, rebindHintApplies, relayBase } from "@/lib/transport";

/** Connection settings for one hive: which path the app uses and why (ADR-0012). */
export function HiveSettings() {
  const { t } = useTranslation();
  const { id } = useParams({ from: "/hives/$id/settings" });
  const me = useMe();
  const hives = useHives();
  const hive = hives.data ? [...hives.data.hives, ...hives.data.shared].find((h) => h.id === id) : undefined;
  const { decision, probing, reprobe } = useTransport(hive);
  const nav = useNavigate();
  const qc = useQueryClient();
  const [removeError, setRemoveError] = useState<string | null>(null);
  const owned = !!hives.data?.hives.find((h) => h.id === id);
  async function remove() {
    if (!hive || !window.confirm(t("transport.confirmRemove", { name: hive.name }))) return;
    setRemoveError(null);
    try {
      unwrap(await api.DELETE("/v1/hives/{id}", { params: { path: { id: hive.id } } }));
      await qc.invalidateQueries({ queryKey: ["hives"] });
      await nav({ to: "/" });
    } catch (err) {
      setRemoveError(errorMessage(t, err));
    }
  }
  if (me.isPending || hives.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (!hive) return <p className="card">{t("errors.not_found")}</p>;
  const pathKey = decision ? `transport.path.${decision.path}` : "transport.probing";
  return (
    <div className="space-y-6 max-w-none">
      <HiveNav hive={hive} active="settings" />
      <p className="text-xs muted font-mono">{hive.id}</p>
      <section className="card max-w-3xl" aria-labelledby="path-title" data-testid="transport">
        <h2 id="path-title" className="text-lg font-semibold mb-1">{t("transport.title")}</h2>
        <p className="muted mb-3 text-sm">{t("transport.liveNote")}</p>
        <dl className="text-sm space-y-2">
          <div className="flex justify-between gap-4"><dt className="muted">{t("transport.httpPath")}</dt><dd className="font-medium" data-testid="path">{t(pathKey)}</dd></div>
          {decision && <div className="flex justify-between gap-4"><dt className="muted">{t("transport.reasonLabel")}</dt><dd>{t(`transport.reason.${decision.reason}`)}</dd></div>}
          <div className="flex justify-between gap-4"><dt className="muted">{t("transport.lanName")}</dt><dd className="text-right break-all">{lanBase(hive) ?? t("transport.none")}</dd></div>
          {!!hive.lan?.addrs?.length && <div className="flex justify-between gap-4"><dt className="muted">{t("transport.lanAddrs")}</dt><dd className="text-right">{hive.lan.addrs.join(", ")}</dd></div>}
          {hive.lan?.cert_issuer && <div className="flex justify-between gap-4"><dt className="muted">{t("transport.cert")}</dt><dd>{t(`transport.issuer.${hive.lan.cert_issuer}`, { defaultValue: hive.lan.cert_issuer })}</dd></div>}
          <div className="flex justify-between gap-4"><dt className="muted">{t("transport.relay")}</dt><dd className="text-right break-all">{relayBase(hive) ?? t("transport.none")}</dd></div>
        </dl>
        {rebindHintApplies(hive, decision) && (
          <div className="callout mt-4 text-sm" data-testid="rebind-hint">
            <p className="font-medium">{t("transport.rebindTitle")}</p>
            <p className="muted mt-1">{t("transport.rebindHelp")}</p>
            <code className="block mt-2 text-xs">h.camhive.app</code>
          </div>
        )}
        {decision?.path === "lan" && <p className="mt-4 text-sm muted">{t("transport.lnaNote")}</p>}
        <button className="btn btn-secondary mt-4" disabled={probing} onClick={() => void reprobe()}>{probing ? t("transport.probing") : t("transport.reprobe")}</button>
      </section>
      {owned && hive.status !== "revoked" && (
        <section className="card max-w-3xl" aria-labelledby="remove-title">
          <h2 id="remove-title" className="text-lg font-semibold mb-1">{t("transport.removeTitle")}</h2>
          <p className="muted text-sm mb-3">{t("transport.removeHelp")}</p>
          {removeError && <p role="alert" className="error text-sm mb-3">{removeError}</p>}
          <button type="button" className="btn btn-danger" onClick={() => void remove()}>{t("transport.remove")}</button>
        </section>
      )}
    </div>
  );
}
