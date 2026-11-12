// The owner's recording deletion (ADR-0016): the request a form makes and
// how its answer reads.
import type { hiveComponents } from "@camhive/api";

/** A request without dry_run, which preview and delete add. */
export type DeletionRequest = Omit<hiveComponents["schemas"]["DeletionRequest"], "dry_run">;
export type Deletion = hiveComponents["schemas"]["Deletion"];

export type DeletionMode = "everything" | "range" | "older";

export type DeletionForm = {
  all: boolean; // every camera, also those removed since
  cameras: string[];
  mode: DeletionMode;
  from: string; // datetime-local
  to: string;
  days: number;
  exports: boolean;
};

const DAY_MS = 86_400_000;

/** The request for a form, or null while it selects nothing. */
export function deletionRequest(f: DeletionForm, now: Date): DeletionRequest | null {
  if (!f.all && f.cameras.length === 0) return null;
  const req: DeletionRequest = f.all ? { include_exports: f.exports } : { cameras: f.cameras, include_exports: f.exports };
  if (f.mode === "range") {
    const from = Date.parse(f.from), to = Date.parse(f.to);
    if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to) return null;
    req.from = new Date(from).toISOString();
    req.to = new Date(to).toISOString();
  }
  if (f.mode === "older") {
    if (!(f.days >= 1)) return null;
    req.to = new Date(now.getTime() - f.days * DAY_MS).toISOString();
  }
  return req;
}

/** `YYYY-MM-DDTHH:MM` in the browser's zone, for a datetime-local input. */
export function localInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Decimal sizes as disks are sold: 2.5 GB. */
export function formatBytes(n: number): string {
  const [v, unit] = n >= 1e9 ? [n / 1e9, "GB"] : n >= 1e6 ? [n / 1e6, "MB"] : [n / 1e3, "KB"];
  return `${Number(v.toFixed(1))} ${unit}`;
}

/** Totals over every camera of an answer. */
export type DeletionTotals = { files: number; bytes: number; pending: number };

export function deletionTotals(d: Deletion): DeletionTotals {
  return (d.cameras ?? []).reduce<DeletionTotals>((t, c) => ({ files: t.files + (c.segments ?? 0), bytes: t.bytes + (c.bytes ?? 0), pending: t.pending + (c.pending ?? 0) }), { files: 0, bytes: 0, pending: 0 });
}
