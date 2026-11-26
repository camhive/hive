import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import { unwrapHive, type hiveComponents } from "@camhive/api";
import { errorMessage } from "@/lib/errors";
import { useGrant } from "@/lib/useGrant";
import { useHiveClient, useUpdateStatus } from "@/lib/hiveApi";
import { useHiveCapability } from "@/lib/capabilities";
import { NeedsUpdate } from "@/components/NeedsUpdate";
import { Facts, PanelRow } from "@/components/SettingsLayout";
import type { Fact } from "@/lib/hiveSettings";
import type { Hive } from "@/lib/hives";

type UpdateStatus = hiveComponents["schemas"]["UpdateStatus"];
type UpdateSettings = hiveComponents["schemas"]["UpdateSettings"];

/**
 * The owner's update settings and status (#82, ADR-0018 §6): mode, window,
 * channel, the version on offer with "Update now", the last update or
 * rollback. The row shows the state the page read; the panel opens below.
 */
export function UpdatesSettings({ hive, state }: { hive: Hive; state: Fact[] }) {
  const { t } = useTranslation();
  const cap = useHiveCapability(hive, "updates");
  return (
    <PanelRow id="updates" title={t("updates.title")} state={cap.has && <Facts facts={state} sentence />} help={t("updates.help")}
      openLabel={cap.has ? t("updates.open") : undefined} disabled={!hive.online}
      notice={!cap.has && <NeedsUpdate hive={hive} since={cap.since} owner />} panel={() => <UpdatesPanel hive={hive} />} />
  );
}

function UpdatesPanel({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const status = useUpdateStatus(hive, client);
  if (access.error) return <p role="alert" className="error text-sm">{errorMessage(t, access.error)}</p>;
  if (!client || !status.data) return <p className="muted text-sm">…</p>;
  return <UpdatesForm hive={hive} status={status.data} save={async (body) => unwrapHive(await client.PUT("/v0/hive/updates", { body }))}
    install={async () => { unwrapHive(await client.POST("/v0/hive/updates/install")); }} />;
}

type FormProps = {
  hive: Hive;
  status: UpdateStatus;
  save: (s: UpdateSettings) => Promise<UpdateStatus>;
  install: () => Promise<void>;
};

function UpdatesForm({ hive, status, save, install }: FormProps) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [from, to] = status.update_window.split("-");
  const [form, setForm] = useState({ updates: status.updates, from: from ?? "", to: to ?? "", channel: status.update_channel });
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const change = (patch: Partial<typeof form>) => { setForm((f) => ({ ...f, ...patch })); setNote(null); };
  async function run(f: () => Promise<void>, done: string) {
    setError(null);
    setNote(null);
    try {
      await f();
      setNote(done);
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  const submit = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      const next = await save({ updates: form.updates, update_window: `${form.from}-${form.to}`, update_channel: form.channel });
      qc.setQueryData(["hive", hive.id, "updates"], next);
    }, t("updates.saved"));
  };
  return (
    <form onSubmit={submit} className="text-sm">
      <UpdateState status={status} onInstall={() => void run(install, t("updates.requested"))} />
      <div className="grid gap-3 sm:grid-cols-2 my-4">
        <label className="block"><span className="block mb-1 font-medium">{t("updates.mode")}</span>
          <select className="input" value={form.updates} onChange={(e) => change({ updates: e.target.value as UpdateSettings["updates"] })}>
            <option value="automatic">{t("updates.automatic")}</option>
            <option value="manual">{t("updates.manual")}</option>
          </select>
        </label>
        <label className="block"><span className="block mb-1 font-medium">{t("updates.channel")}</span>
          <select className="input" value={form.channel} onChange={(e) => change({ channel: e.target.value as UpdateSettings["update_channel"] })}>
            <option value="stable">{t("updates.stable")}</option>
            <option value="beta">{t("updates.beta")}</option>
          </select>
        </label>
        <fieldset className="sm:col-span-2">
          <legend className="mb-1 font-medium">{t("updates.window")}</legend>
          <div className="flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-2">{t("updates.from")}<input type="time" className="input !w-auto" required value={form.from} onChange={(e) => change({ from: e.target.value })} /></label>
            <label className="flex items-center gap-2">{t("updates.to")}<input type="time" className="input !w-auto" required value={form.to} onChange={(e) => change({ to: e.target.value })} /></label>
          </div>
        </fieldset>
      </div>
      {error && <p role="alert" className="error mb-3">{error}</p>}
      {note && <p role="status" className="mb-3">{note}</p>}
      <button className="btn">{t("updates.save")}</button>
    </form>
  );
}

/** What waits: in manual mode for Update now, in automatic mode for the window. */
function waiting(t: TFunction, status: UpdateStatus): string {
  if (!status.available) return t("updates.noneWaiting");
  if (status.updates === "manual") return t("updates.available", { version: status.available });
  const [from, to] = status.update_window.split("-");
  return t("updates.inWindow", { version: status.available, from, to });
}

/** Running version, why a build never updates, the offer and the last result. */
function UpdateState({ status, onInstall }: { status: UpdateStatus; onInstall: () => void }) {
  const { t, i18n } = useTranslation();
  const last = status.last;
  return (
    <div className="space-y-2">
      <dl className="kv"><div><dt className="muted">{t("updates.running")}</dt><dd className="font-medium">{status.running}</dd></div></dl>
      {!status.supported && <p className="callout">{t(`updates.unsupported.${status.unsupported_reason ?? "no_updater"}`)}</p>}
      {status.settings_need_restart && <p className="callout">{t("updates.needRestart")}</p>}
      {last && (
        <p data-testid="update-last">{t(last.action === "update.applied" ? "updates.lastApplied" : "updates.lastRolledBack",
          { from: last.from, to: last.to, date: new Date(last.at).toLocaleString(i18n.language) })}</p>
      )}
      {status.supported && (
        <div className="flex flex-wrap items-center gap-3" data-testid="update-available">
          <span>{waiting(t, status)}</span>
          {status.available && <button type="button" className="btn btn-secondary btn-sm" onClick={onInstall}>{t("updates.updateNow")}</button>}
        </div>
      )}
      {status.supported && status.needs_package && <p className="callout" data-testid="update-needs-package">{t("updates.needsPackage", { version: status.needs_package })}</p>}
    </div>
  );
}
