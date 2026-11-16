// Notification preferences (ADR-0021): one document per account.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { unwrap, type components } from "@camhive/api";
import { api } from "./api";

export type NotifySettings = components["schemas"]["NotificationSettings"];
export type CameraNotify = NotifySettings["cameras"][number];
export type NotifyGroup = keyof NotifySettings["events"];
export type NotifyChannel = "email" | "push";

export const GROUPS: NotifyGroup[] = ["offline", "storage", "motion"];
export const COOLDOWNS = [1, 5, 10, 15, 30, 60] as const;
const KEY = ["me", "notifications"];

export function useNotifySettings() {
  return useQuery({ queryKey: KEY, queryFn: async () => unwrap(await api.GET("/v1/me/notifications")) });
}

/** Saves the whole document; the server's answer becomes the cached copy. */
export function useSaveNotifySettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (s: NotifySettings) => {
      const { channels: _, ...rest } = s; // read-only
      const body = { ...rest, time_zone: localZone() }; // times in mails and quiet hours follow this browser
      return unwrap(await api.PUT("/v1/me/notifications", { body }));
    },
    onSuccess: (saved) => qc.setQueryData(KEY, saved),
  });
}

/** A camera's motion setting; motion on every 10 minutes when it has none. */
export function cameraPrefs(s: NotifySettings, hive: string, camera: string): CameraNotify {
  return s.cameras.find((c) => c.hive_id === hive && c.camera_id === camera) ?? { hive_id: hive, camera_id: camera, motion: true, cooldown_minutes: 10 };
}

/** The document with one camera's setting replaced or added. */
export function withCamera(s: NotifySettings, c: CameraNotify): NotifySettings {
  const rest = s.cameras.filter((x) => x.hive_id !== c.hive_id || x.camera_id !== c.camera_id);
  const at = s.cameras.findIndex((x) => x.hive_id === c.hive_id && x.camera_id === c.camera_id);
  const cameras = at < 0 ? [...rest, c] : [...rest.slice(0, at), c, ...rest.slice(at)];
  return { ...s, cameras };
}

/** The browser's IANA time zone, saved with every change. */
export function localZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}
