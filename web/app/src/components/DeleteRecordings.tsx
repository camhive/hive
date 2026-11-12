import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { unwrapHive, type HiveCamera, type HiveClient } from "@camhive/api";
import { errorMessage } from "@/lib/errors";
import { deletionRequest, deletionTotals, formatBytes, localInput, type Deletion, type DeletionForm, type DeletionMode, type DeletionRequest } from "@/lib/deletion";

type Props = {
  client: HiveClient;
  cameras: HiveCamera[];
  hiveName: string;
  /** Start from one camera and span, as "Delete this day" does. */
  preset?: { camera: string; from: Date; to: Date };
  onDone?: () => void;
};

/**
 * The owner deletes recordings (ADR-0016): pick cameras and what to delete,
 * preview what the hive will really take (whole files, so maybe a little
 * more), then delete. Deleting everything asks for a typed name. Any change
 * after the preview needs a new one.
 */
export function DeleteRecordings({ client, cameras, hiveName, preset, onDone }: Props) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [form, setForm] = useState<DeletionForm>({
    all: false, cameras: preset ? [preset.camera] : [], mode: preset ? "range" : "everything",
    from: preset ? localInput(preset.from) : "", to: preset ? localInput(preset.to) : "", days: 30, exports: false,
  });
  const [preview, setPreview] = useState<{ plan: Deletion; body: DeletionRequest } | null>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<Deletion | null>(null);
  const request = deletionRequest(form, new Date());
  const names = new Map(cameras.map((c) => [c.id, c.name]));
  const confirmWord = form.mode === "everything" ? (!form.all && form.cameras.length === 1 ? (names.get(form.cameras[0]!) ?? form.cameras[0]!) : hiveName) : null;

  function change(patch: Partial<DeletionForm>) {
    setForm((f) => ({ ...f, ...patch }));
    setPreview(null);
    setTyped("");
    setError(null);
    setDone(null);
  }

  async function send(dryRun: boolean, body: DeletionRequest): Promise<Deletion | null> {
    setBusy(true);
    setError(null);
    try {
      return unwrapHive(await client.POST("/v0/archive/deletions", { body: { ...body, dry_run: dryRun } }));
    } catch (err) {
      setError(errorMessage(t, err));
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function runPreview() {
    if (!request) return;
    const plan = await send(true, request);
    if (plan) setPreview({ plan, body: request });
  }

  async function runDelete() {
    if (!preview) return;
    const result = await send(false, preview.body);
    if (!result) return;
    setPreview(null);
    setTyped("");
    setDone(result);
    await Promise.all([qc.invalidateQueries({ queryKey: ["archive"] }), qc.invalidateQueries({ queryKey: ["exports"] })]);
    onDone?.();
  }

  const toggle = (id: string) => change({ cameras: form.cameras.includes(id) ? form.cameras.filter((c) => c !== id) : [...form.cameras, id] });
  const mode = (m: DeletionMode, label: string, extra?: ReactNode) => (
    <div className="flex flex-wrap items-center gap-2">
      <label className="flex items-center gap-2">
        <input type="radio" name="deletion-mode" checked={form.mode === m} onChange={() => change({ mode: m })} />
        {label}
      </label>
      {form.mode === m && extra}
    </div>
  );

  return (
    <div className="space-y-4 text-sm" data-testid="delete-recordings">
      <fieldset className="space-y-1">
        <legend className="font-medium mb-1">{t("deletion.cameras")}</legend>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={form.all} onChange={(e) => change({ all: e.target.checked })} />
          {t("deletion.allCameras")}
        </label>
        {cameras.map((c) => (
          <label key={c.id} className="flex items-center gap-2 pl-5">
            <input type="checkbox" disabled={form.all} checked={form.all || form.cameras.includes(c.id ?? "")} onChange={() => toggle(c.id ?? "")} />
            {c.name}
          </label>
        ))}
      </fieldset>

      <fieldset className="space-y-1">
        <legend className="font-medium mb-1">{t("deletion.what")}</legend>
        {mode("everything", t("deletion.everything"))}
        {mode("range", t("deletion.range"), (
          <span className="flex flex-wrap items-center gap-2">
            <label className="muted" htmlFor="deletion-from">{t("deletion.from")}</label>
            <input id="deletion-from" type="datetime-local" className="input input-sm !w-auto" value={form.from} onChange={(e) => change({ from: e.target.value })} />
            <label className="muted" htmlFor="deletion-to">{t("deletion.to")}</label>
            <input id="deletion-to" type="datetime-local" className="input input-sm !w-auto" value={form.to} onChange={(e) => change({ to: e.target.value })} />
          </span>
        ))}
        {mode("older", t("deletion.older"), (
          <span className="flex items-center gap-2">
            <input id="deletion-days" aria-label={t("deletion.days")} type="number" min={1} className="input input-sm !w-20" value={Number.isFinite(form.days) ? form.days : ""} onChange={(e) => change({ days: e.target.valueAsNumber })} />
            <span className="muted">{t("deletion.days")}</span>
          </span>
        ))}
      </fieldset>

      <label className="flex items-center gap-2">
        <input type="checkbox" checked={form.exports} onChange={(e) => change({ exports: e.target.checked })} />
        {t("deletion.includeExports")}
      </label>

      <button type="button" className="btn btn-secondary" disabled={!request || busy} onClick={() => void runPreview()}>{t("deletion.preview")}</button>
      {error && <p role="alert" className="error">{error}</p>}

      {preview && (
        <div className="callout space-y-2" data-testid="deletion-preview">
          {(preview.plan.cameras ?? []).length === 0 && (preview.plan.exports?.count ?? 0) === 0 ? <p>{t("deletion.nothing")}</p> : (
            <>
              <ul className="space-y-1">
                {(preview.plan.cameras ?? []).map((c) => (
                  <li key={c.camera} data-testid="deletion-row">
                    <span className="font-medium">{names.get(c.camera ?? "") ?? c.camera}</span>
                    {": "}{new Date(c.from ?? "").toLocaleString()} – {new Date(c.to ?? "").toLocaleString()}
                    {" · "}{t("deletion.files", { count: c.segments ?? 0 })}{" · "}{formatBytes(c.bytes ?? 0)}
                    {(c.pending ?? 0) > 0 && <span className="muted"> · {t("deletion.pendingShort", { count: c.pending })}</span>}
                  </li>
                ))}
              </ul>
              {(preview.plan.exports?.count ?? 0) > 0 && <p>{t("deletion.exports", { count: preview.plan.exports?.count ?? 0, size: formatBytes(preview.plan.exports?.bytes ?? 0) })}</p>}
              <p className="muted">{t("deletion.rounded")}</p>
              <p className="font-medium">{t("deletion.irreversible")}</p>
              {confirmWord && (
                <div>
                  <label htmlFor="deletion-confirm" className="block mb-1">{t("deletion.typeToConfirm", { word: confirmWord })}</label>
                  <input id="deletion-confirm" className="input input-sm" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
                </div>
              )}
              <button type="button" className="btn btn-danger" disabled={busy || (!!confirmWord && typed.trim() !== confirmWord)} onClick={() => void runDelete()}>{t("deletion.deleteNow")}</button>
            </>
          )}
        </div>
      )}

      {done && (() => {
        const tot = deletionTotals(done);
        return (
          <p role="status" className="callout">
            {t("deletion.deleted", { count: tot.files, size: formatBytes(tot.bytes) })}
            {tot.pending > 0 && <> {t("deletion.pendingNote", { count: tot.pending })}</>}
            {(done.exports?.count ?? 0) > 0 && <> {t("deletion.exportsDeleted", { count: done.exports?.count ?? 0 })}</>}
          </p>
        );
      })()}
    </div>
  );
}
