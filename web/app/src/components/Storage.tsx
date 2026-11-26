import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import { unwrapHive } from "@camhive/api";
import { errorMessage } from "@/lib/errors";
import { useGrant } from "@/lib/useGrant";
import { useHiveClient, useStorage } from "@/lib/hiveApi";
import { useHiveCapability } from "@/lib/capabilities";
import { appliedRetention, cameraRate, deletesFootage, fillsLine, forecastLine, formatDays, formatSize, maxFloorGB, validateStorage, type CameraStorage, type StorageOverview, type StorageValues } from "@/lib/storage";
import { NeedsUpdate } from "@/components/NeedsUpdate";
import { Facts, PanelRow } from "@/components/SettingsLayout";
import type { Fact } from "@/lib/hiveSettings";
import type { Hive } from "@/lib/hives";

/**
 * The storage overview and forecast (#150, ADR-0024): the owner sees the
 * disk, every camera's footage and rate, what the disk holds and what
 * retention becomes, and changes retention and the floor; a member sees
 * the days held of their cameras. The row shows what the page read; the
 * panel opens below.
 */
export function StorageSettings({ hive, owner, state }: { hive: Hive; owner: boolean; state: Fact[] }) {
  const { t } = useTranslation();
  const cap = useHiveCapability(hive, "storage");
  return (
    <PanelRow id="storage" title={t("storage.title")} state={cap.has && <Facts facts={state} sentence />} help={t(owner ? "storage.help" : "storage.helpMember")}
      openLabel={cap.has ? t("storage.open") : undefined} disabled={!hive.online}
      notice={!cap.has && <NeedsUpdate hive={hive} since={cap.since} owner={owner} />} panel={() => <StoragePanel hive={hive} owner={owner} />} />
  );
}

function StoragePanel({ hive, owner }: { hive: Hive; owner: boolean }) {
  const { t } = useTranslation();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const overview = useStorage(hive, client);
  if (access.error) return <p role="alert" className="error text-sm">{errorMessage(t, access.error)}</p>;
  if (overview.error) return <p role="alert" className="error text-sm">{errorMessage(t, overview.error)}</p>;
  if (!client || !overview.data) return <p className="muted text-sm">…</p>;
  const o = overview.data;
  if (!owner || !o.disk || !o.settings) return <MemberDays cameras={o.cameras} />;
  return (
    <div className="text-sm space-y-4">
      <OwnerFigures o={o} />
      <StorageForm hive={hive} o={o} save={async (body) => unwrapHive(await client.PATCH("/v0/hive/storage", { body }))} />
    </div>
  );
}

/** A member's short read-only line per camera. */
function MemberDays({ cameras }: { cameras: CameraStorage[] }) {
  const { t, i18n } = useTranslation();
  if (cameras.length === 0) return <p className="text-sm muted">{t("storage.noFootage")}</p>;
  return (
    <ul className="text-sm space-y-1" data-testid="storage-member">
      {cameras.map((c) => <li key={c.id}>{t("storage.memberLine", { name: c.name ?? c.id, count: c.days_held, days: formatDays(c.days_held, i18n.language), size: formatSize(c.bytes, i18n.language) })}</li>)}
    </ul>
  );
}

function OwnerFigures({ o }: { o: StorageOverview }) {
  const { t, i18n } = useTranslation();
  const lang = i18n.language;
  const disk = o.disk!, s = o.settings!, f = o.forecast;
  const line = forecastLine(o, lang);
  const fills = fillsLine(o);
  const retention = appliedRetention(o);
  const short = !!f && f.bytes_per_day > 0 && !f.retention_met && retention > 0;
  const cap = s.retention_days_max ?? 0;
  return (
    <>
      <dl className="kv space-y-1">
        <div><dt className="muted">{t("storage.diskTotal")}</dt><dd>{formatSize(disk.total_bytes, lang)}</dd></div>
        <div><dt className="muted">{t("storage.archive")}</dt><dd>{formatSize(disk.archive_bytes, lang)}</dd></div>
        <div><dt className="muted">{t("storage.exports")}</dt><dd>{formatSize(disk.export_bytes, lang)}</dd></div>
        <div><dt className="muted">{t("storage.free")}</dt><dd>{formatSize(disk.free_bytes, lang)}</dd></div>
      </dl>
      <p className="font-medium" data-testid="storage-forecast">{t(line.key, line.values)}</p>
      {cap > 0 && (s.retention_days === 0 || s.retention_days > cap) && <p className="callout" data-testid="storage-cap">{t("storage.cap", { cap })}</p>}
      {(short || fills) && (
        <div role="note" className="callout" data-testid="storage-short">
          {short && <p>{f.needed_bytes !== undefined ? t("storage.short", { retention, needed: formatSize(f.needed_bytes, lang, "up") }) : t("storage.shortOwn", { retention })}</p>}
          {fills && <p className={short ? "muted mt-1" : ""}>{t(fills.key, fills.values)}</p>}
        </div>
      )}
      {o.cameras.length === 0 ? <p className="muted">{t("storage.noFootage")}</p> : (
        <ul className="space-y-2" aria-label={t("storage.cameras")}>
          {o.cameras.map((c) => (
            <li key={c.id} data-testid="storage-camera">
              <span className="font-medium">{c.name ?? t("storage.removed")}</span>
              <span className="muted">{" · "}{t("storage.cameraLine", { count: c.days_held, days: formatDays(c.days_held, lang), size: formatSize(c.bytes, lang), rate: formatSize(cameraRate(c), lang, "up") })}{" · "}{keeps(t, c)}</span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

/** What a camera will keep: its retention, or what the disk lets it reach. */
function keeps(t: TFunction, c: CameraStorage): string {
  const retention = c.retention_days ?? 0, effective = c.effective_days ?? 0;
  if (retention === 0) return cameraRate(c) > 0 && effective > 0 ? t("storage.keepsUnlimited", { effective }) : t("storage.keepsUnlimitedPlain");
  if (effective < retention) return t("storage.keepsShort", { effective, retention });
  return t("storage.keeps", { retention });
}

type FormProps = { hive: Hive; o: StorageOverview; save: (v: StorageValues) => Promise<StorageOverview> };

function StorageForm({ hive, o, save }: FormProps) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const s = o.settings!, total = o.disk!.total_bytes;
  const [form, setForm] = useState({ retention_days: String(s.retention_days), min_free_gb: String(s.min_free_gb) });
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const max = maxFloorGB(total);
  const change = (patch: Partial<typeof form>) => { setForm((f) => ({ ...f, ...patch })); setNote(null); setError(null); };
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setNote(null);
    const v = validateStorage(form, total);
    if ("invalid" in v) {
      setError(t(`storage.invalid.${v.invalid}`, { max }));
      return;
    }
    if (deletesFootage(o, v) && !window.confirm(t("storage.confirmDelete"))) return;
    try {
      qc.setQueryData(["hive", hive.id, "storage"], await save(v));
      setNote(t("storage.saved"));
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  return (
    <form onSubmit={(e) => void submit(e)} noValidate>
      <h4 className="font-semibold mb-2">{t("storage.settings")}</h4>
      <div className="grid gap-3 sm:grid-cols-2 mb-3">
        <div>
          <label className="block"><span className="block mb-1 font-medium">{t("storage.retention")}</span>
            <input className="input" inputMode="numeric" aria-describedby="storage-retention-help" value={form.retention_days} onChange={(e) => change({ retention_days: e.target.value })} />
          </label>
          <p id="storage-retention-help" className="mt-1 muted text-xs">{t("storage.retentionHelp")}</p>
        </div>
        <div>
          <label className="block"><span className="block mb-1 font-medium">{t("storage.floor")}</span>
            <input className="input" inputMode="numeric" aria-describedby="storage-floor-help" value={form.min_free_gb} onChange={(e) => change({ min_free_gb: e.target.value })} />
          </label>
          <p id="storage-floor-help" className="mt-1 muted text-xs">{t("storage.floorHelp", { max })}</p>
        </div>
      </div>
      {error && <p role="alert" className="error mb-3">{error}</p>}
      {note && <p role="status" className="mb-3">{note}</p>}
      <button className="btn">{t("storage.save")}</button>
    </form>
  );
}
