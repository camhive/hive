import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { unwrapHive, type HiveClient } from "@camhive/api";
import { errorMessage } from "@/lib/errors";
import { useGrant } from "@/lib/useGrant";
import { useHiveClient } from "@/lib/hiveApi";
import { useHiveCapability } from "@/lib/capabilities";
import { formatSize } from "@/lib/storage";
import { filesToSave, moveCommand, moveProgress, moveTargetError, needsAttention, type DataMove, type DiskHealth, type MoveFile } from "@/lib/diskHealth";
import type { Hive } from "@/lib/hives";

function useMove(hive: Hive, client: HiveClient | null, enabled: boolean) {
  return useQuery({
    queryKey: ["hive", hive.id, "move"],
    queryFn: async () => unwrapHive(await client!.GET("/v0/hive/move")),
    enabled: !!client && enabled,
    refetchInterval: (q) => (q.state.data?.state === "copying" ? 3000 : false), // the command writes its note every two seconds
  });
}

/** The files to save first, where the hive says they are. */
function FilesToSave({ files }: { files?: MoveFile[] }) {
  return (
    <ul className="font-mono text-xs space-y-0.5" data-testid="files-to-save">
      {filesToSave(files).map((f) => <li key={f}>{f}</li>)}
    </ul>
  );
}

/**
 * The owner's banner on the hive page (#155): the archive disk is failing
 * or has failed. It says what the hive saw, which files to save first by
 * hand and the command that moves the hive to a new disk. It reads the
 * storage overview through the page's own session.
 */
export function DiskHealthNotice({ hive, client, owner }: { hive: Hive; client: HiveClient | null; owner: boolean }) {
  const { t } = useTranslation();
  const cap = useHiveCapability(hive, "disk.health");
  const storage = useQuery({
    queryKey: ["hive", hive.id, "storage"],
    queryFn: async () => unwrapHive(await client!.GET("/v0/storage")),
    enabled: !!client && owner && cap.has,
    staleTime: 5 * 60_000,
  });
  const health: DiskHealth | undefined = storage.data?.disk?.health;
  const attention = needsAttention(health);
  const move = useMove(hive, client, attention);
  if (!attention || !health) return null;
  const failed = health.state === "failed";
  return (
    <section aria-labelledby={`disk-${hive.id}`} className="card callout text-sm space-y-2" data-testid="disk-health" data-state={health.state}>
      <p id={`disk-${hive.id}`} className="font-medium">{t(failed ? "disk.failedTitle" : "disk.failingTitle", { device: health.device ?? t("disk.theDisk") })}</p>
      <p>{t(failed ? "disk.failedBody" : "disk.failingBody")}</p>
      {health.evidence.length > 0 && (
        <ul className="list-disc pl-5 muted" data-testid="disk-evidence">
          {health.evidence.map((e, i) => <li key={i}>{t(`disk.signal.${e.signal}`)}: {e.detail}</li>)}
        </ul>
      )}
      <p>{t("disk.saveFirst")}</p>
      <FilesToSave files={move.data?.files} />
      {move.data?.supported === false ? <p>{t("disk.moveUnsupported")}</p> : <>
        <p>{t("disk.thenRun")}</p>
        <code className="block text-xs" data-testid="disk-command">{moveCommand("")}</code>
        <Link to="/hives/$id/settings" params={{ id: hive.id }} className="link">{t("disk.guided")}</Link>
      </>}
    </section>
  );
}

/**
 * The guided replacement in the Connection card (#155): the steps, the
 * command for the directory the owner types, and the progress of a move
 * the command runs on the hive's computer.
 */
export function MoveDataSettings({ hive, owner }: { hive: Hive; owner: boolean }) {
  const { t } = useTranslation();
  const cap = useHiveCapability(hive, "disk.health");
  const [open, setOpen] = useState(false);
  if (!owner || !cap.has) return null;
  return (
    <div className="mt-4 border-t pt-4" style={{ borderColor: "var(--border)" }} data-testid="move-data">
      <h3 className="font-medium mb-1">{t("disk.moveTitle")}</h3>
      <p className="muted text-sm mb-3">{t("disk.moveHelp")}</p>
      {open ? <MovePanel hive={hive} />
        : <button type="button" className="btn btn-secondary" disabled={!hive.online} onClick={() => setOpen(true)}>{t("disk.moveOpen")}</button>}
    </div>
  );
}

function MovePanel({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const move = useMove(hive, client, true);
  const [dir, setDir] = useState("");
  const [copied, setCopied] = useState(false);
  if (access.error) return <p role="alert" className="error text-sm">{errorMessage(t, access.error)}</p>;
  if (move.error) return <p role="alert" className="error text-sm">{errorMessage(t, move.error)}</p>;
  if (!client || !move.data) return <p className="muted text-sm">…</p>;
  const m = move.data;
  if (m.state === "copying" || m.state === "switching") return <MoveProgress m={m} />;
  const result = m.state === "done" || m.state === "failed" ? <MoveProgress m={m} /> : null; // the last move's outcome; the steps stay below it
  if (!m.supported) {
    return (
      <div className="text-sm space-y-2" data-testid="move-unsupported">
        {result}
        <p>{t("disk.moveUnsupported")}</p>
        <FilesToSave files={m.files} />
      </div>
    );
  }
  const problem = moveTargetError(dir, m.data_dir, m.archive_dir);
  const command = moveCommand(dir);
  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="text-sm space-y-3">
      {result}
      <ol className="space-y-3 list-decimal pl-5">
        <li>{t("disk.step1")}<FilesToSave files={m.files} /></li>
        <li>{t("disk.step2")}</li>
        <li>
          <label className="block">{t("disk.target")}
            <input className="input mt-1 w-full font-mono" value={dir} placeholder="/mnt/newdisk/camhive" onChange={(e) => { setDir(e.target.value); setCopied(false); }} aria-invalid={!!problem} />
          </label>
          {problem && <p role="alert" className="error mt-1">{t(`disk.targetError.${problem}`, { dir: m.data_dir })}</p>}
        </li>
        <li>{t("disk.step3")}
          <code className="block text-xs mt-1" data-testid="move-command">{problem ? "" : command}</code>
          <button type="button" className="btn btn-secondary mt-2" disabled={!!problem} onClick={() => void copy()}>{t(copied ? "disk.copied" : "disk.copy")}</button>
        </li>
        <li>{t("disk.step4")}</li>
      </ol>
    </div>
  );
}

function MoveProgress({ m }: { m: DataMove }) {
  const { t, i18n } = useTranslation();
  const lang = i18n.language;
  const pct = moveProgress(m);
  return (
    <div className="text-sm space-y-2" data-testid="move-progress" data-state={m.state}>
      {m.state === "copying" && <>
        <p>{t("disk.copying", { to: m.to, step: m.step ?? "" })}</p>
        <progress className="w-full" max={100} value={pct} aria-label={t("disk.progress")} />
        <p className="muted">{t("disk.copiedOf", { copied: formatSize(m.copied_bytes ?? 0, lang), planned: formatSize(m.planned_bytes ?? 0, lang), pct })}</p>
        {m.error && <p role="alert" className="error">{t("disk.stuck", { error: m.error, to: m.to })}</p>}
        <p className="muted">{t("disk.notRecording")}</p>
      </>}
      {m.state === "switching" && <p>{t(m.error ? "disk.switchStuck" : "disk.switching", { to: m.to, error: m.error ?? "" })}</p>}
      {m.state === "done" && <p>{t("disk.done", { from: m.from, to: m.to })}</p>}
      {m.state === "failed" && <p>{t("disk.cancelled", { from: m.from })}</p>}
      {m.state === "done" && (m.unreadable_segments ?? 0) > 0 && (
        <p data-testid="move-unreadable">{t("disk.unreadable", { count: m.unreadable_segments, size: formatSize(m.unreadable_bytes ?? 0, lang) })}</p>
      )}
      {m.state === "done" && (m.skipped_segments ?? 0) > 0 && (
        <p data-testid="move-skipped">{t("disk.skipped", { count: m.skipped_segments, size: formatSize(m.skipped_bytes ?? 0, lang), newest: m.skipped_newest ? new Date(m.skipped_newest).toLocaleString(lang) : "" })}</p>
      )}
    </div>
  );
}
