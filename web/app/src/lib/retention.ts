// A camera's own retention (#61, #124): the hive-wide value, or its own
// number of days (0 keeps footage until the disk needs the space).
import type { HiveCamera } from "@camhive/api";

/** The form's choice; days stay text while the owner types, and are kept while "Hive default" is chosen. */
export type Retention = { kind: "default" | "days"; days: string };

/** The most days the hive takes (internal/domain/camera MaxRetentionDays). */
export const MAX_RETENTION_DAYS = 3650;

/** The choice the camera has now; the hive omits retention_days on the hive-wide value. */
export function retentionOf(camera: Pick<HiveCamera, "retention_days">): Retention {
  return camera.retention_days == null ? { kind: "default", days: "" } : { kind: "days", days: String(camera.retention_days) };
}

/** Whole days from 0 to 3650, else null: the hive refuses anything else with 422 invalid. */
export function parseDays(raw: string): number | null {
  if (!/^\d+$/.test(raw.trim())) return null;
  const n = Number(raw.trim());
  return n <= MAX_RETENTION_DAYS ? n : null;
}

/**
 * What the PATCH body carries: nothing when the choice did not change,
 * null to go back to the hive-wide value, else the days; "invalid" for
 * days the hive would refuse.
 */
export function retentionChange(camera: Pick<HiveCamera, "retention_days">, choice: Retention): { retention_days?: number | null } | "invalid" {
  const current = camera.retention_days ?? null;
  if (choice.kind === "default") return current === null ? {} : { retention_days: null };
  const days = parseDays(choice.days);
  if (days === null) return "invalid";
  return days === current ? {} : { retention_days: days };
}
