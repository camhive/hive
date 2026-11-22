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
import { useGrant } from "@/lib/useGrant";
import { useCameras, useHiveClient } from "@/lib/hiveApi";
import { DeleteRecordings } from "@/components/DeleteRecordings";
import { LocalLoginSettings } from "@/components/LocalLogin";
import { NeedsUpdate } from "@/components/NeedsUpdate";
import { UpdatesSettings } from "@/components/Updates";
import { EncryptionSettings } from "@/components/ArchiveEncryption";
import { StorageSettings } from "@/components/Storage";
import { MoveDataSettings } from "@/components/DiskHealth";
import { useHiveCapability } from "@/lib/capabilities";
import type { Hive } from "@/lib/hives";

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
  const [deleting, setDeleting] = useState(false);
  const owned = !!hives.data?.hives.find((h) => h.id === id);
  const canDelete = useHiveCapability(hive ?? {}, "archive.delete");
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
        <dl className="kv text-sm space-y-2">
          <div><dt className="muted">{t("transport.httpPath")}</dt><dd className="font-medium" data-testid="path">{t(pathKey)}</dd></div>
          {decision && <div><dt className="muted">{t("transport.reasonLabel")}</dt><dd>{t(`transport.reason.${decision.reason}`)}</dd></div>}
          <div><dt className="muted">{t("transport.lanName")}</dt><dd>{lanBase(hive) ?? t("transport.none")}</dd></div>
          {!!hive.lan?.addrs?.length && <div><dt className="muted">{t("transport.lanAddrs")}</dt><dd>{hive.lan.addrs.join(", ")}</dd></div>}
          {hive.lan?.cert_issuer && <div><dt className="muted">{t("transport.cert")}</dt><dd>{t(`transport.issuer.${hive.lan.cert_issuer}`, { defaultValue: hive.lan.cert_issuer })}</dd></div>}
          <div><dt className="muted">{t("transport.relay")}</dt><dd>{relayBase(hive) ?? t("transport.none")}</dd></div>
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
        {hive.status !== "revoked" && <MoveDataSettings hive={hive} owner={owned} />}
      </section>
      {hive.status !== "revoked" && <StorageSettings hive={hive} owner={owned} />}
      {owned && hive.status !== "revoked" && <UpdatesSettings hive={hive} />}
      {owned && hive.status !== "revoked" && <LocalLoginSettings hive={hive} />}
      {owned && hive.status !== "revoked" && <EncryptionSettings hive={hive} />}
      {owned && hive.status !== "revoked" && (
        <section className="card max-w-3xl" aria-labelledby="delete-recordings-title">
          <h2 id="delete-recordings-title" className="text-lg font-semibold mb-1">{t("deletion.title")}</h2>
          <p className="muted text-sm mb-3">{t("deletion.help")}</p>
          {!canDelete.has && <div className="mb-3"><NeedsUpdate hive={hive} since={canDelete.since} owner /></div>}
          {deleting ? <HiveDeletion hive={hive} /> : <button type="button" className="btn btn-secondary" disabled={!hive.online || !canDelete.has} onClick={() => setDeleting(true)}>{t("deletion.open")}</button>}
        </section>
      )}
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

/**
 * The deletion form with the hive's cameras. It asks the hive for access
 * only once opened: a remote session counts against the plan.
 */
function HiveDeletion({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const cameras = useCameras(hive, client);
  if (access.error) return <p role="alert" className="error text-sm">{errorMessage(t, access.error)}</p>;
  if (!client || !cameras.data) return <p className="muted text-sm">…</p>;
  return <DeleteRecordings client={client} cameras={cameras.data} hiveName={hive.name ?? hive.id} />;
}
