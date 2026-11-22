// The archive disk's health and the guided replacement (#155, ADR-0024 §6).
// The hive watches the disk and moves the data (hive move-data); the app
// says what the hive saw, which files to save first, and the command.
import type { hiveComponents } from "@camhive/api";

export type DiskHealth = hiveComponents["schemas"]["DiskHealth"];
export type DataMove = hiveComponents["schemas"]["DataMove"];
export type MoveFile = { name: string; path: string };

/** What to copy by hand first, hardest to replace first: without archive-key/ an encrypted archive is lost. */
export const FILES_TO_SAVE = ["hive.key", "cloud.json", "hive.yaml", "archive-key/", "hive.db"] as const;

/** The example the docs use when the owner has not typed a directory yet. */
export const EXAMPLE_TARGET = "/mnt/newdisk/camhive";

/** The files with their real places when the hive said them (hive.yaml may be in /etc/camhive), else their names. */
export function filesToSave(files: MoveFile[] | undefined): string[] {
  return files?.length ? files.map((f) => f.path) : [...FILES_TO_SAVE];
}

export function needsAttention(h: DiskHealth | undefined): boolean {
  return h?.state === "failing" || h?.state === "failed";
}

/** The shell command; the directory is one the hive accepts (moveTargetError), so it needs no quoting. */
export function moveCommand(dir: string): string {
  return `sudo hive move-data ${dir.trim() || EXAMPLE_TARGET}`;
}

/**
 * Characters systemd splits or expands in the service definition, and
 * characters a shell acts on in the command the owner pastes into a root
 * shell; the hive refuses the same (datamove.SafePath).
 */
const UNSAFE = /[\s%\\"'$;&|()<>*?#`!~{}[\]\u0000-\u001f\u007f]/;

function overlaps(a: string, b: string): boolean {
  return a === b || a.startsWith(b + "/") || b.startsWith(a + "/");
}

/** Why the hive would refuse the directory: not absolute, characters systemd mangles, or inside or around the data or archive directory. */
export function moveTargetError(dir: string, dataDir: string | undefined, archiveDir?: string): "absolute" | "characters" | "overlaps" | undefined {
  const raw = dir.trim();
  if (!raw) return undefined;
  if (!raw.startsWith("/")) return "absolute";
  if (UNSAFE.test(raw)) return "characters";
  const d = raw.replace(/\/+$/, "");
  if (!d) return "overlaps"; // the root holds everything
  for (const other of [dataDir, archiveDir]) {
    const o = (other ?? "").replace(/\/+$/, "");
    if (o && overlaps(d, o)) return "overlaps";
  }
  return undefined;
}

/** Percent copied of what the move plans to copy. */
export function moveProgress(m: DataMove): number {
  if (m.state === "done" || m.state === "switching") return 100;
  if (!m.planned_bytes) return 0;
  return Math.min(100, Math.floor(((m.copied_bytes ?? 0) * 100) / m.planned_bytes));
}
