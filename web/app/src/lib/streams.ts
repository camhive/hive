import type { HiveCamera } from "@camhive/api";

/**
 * Whether a camera stream URL is one the hive takes: rtsp:// or rtsps://
 * with a host and without user or password (those go in the camera's
 * credentials). The hive checks the same; this only saves a round trip.
 */
export function isStreamUrl(raw: string): boolean {
  try {
    const u = new URL(raw.trim());
    return (u.protocol === "rtsp:" || u.protocol === "rtsps:") && u.host !== "" && !u.username && !u.password;
  } catch {
    return false;
  }
}

/** An optional URL the hive would refuse: not empty and not a stream URL. */
export const refusedOptional = (raw: string): boolean => raw.trim() !== "" && !isStreamUrl(raw);

/** The sub stream's URL as the form shows it ("" when the camera has none). */
export const subStreamOf = (camera: HiveCamera): string => camera.streams?.sub?.URL ?? "";

/** What the PATCH body carries for the edited sub stream: nothing when unchanged, "" removes it, "invalid" for a URL the hive would refuse. */
export function subStreamChange(camera: HiveCamera, raw: string): { sub_stream?: string } | "invalid" {
  const next = raw.trim();
  if (next === subStreamOf(camera)) return {};
  return refusedOptional(next) ? "invalid" : { sub_stream: next };
}
