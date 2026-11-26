// Whether a hive records (#172, ADR-0024 amendment). The hive's recording
// watchdog says it in GET /v0/hive (not_recording) when the app reaches the
// hive; the cloud keeps the hive's last word (recording_stopped in the hive
// list) for when it cannot. The hive's own answer is fresher and wins.
import type { hiveComponents } from "@camhive/api";
import type { Hive } from "./hives";

export type NotRecording = hiveComponents["schemas"]["NotRecording"];
export type HiveInfoAnswer = { not_recording?: NotRecording };

export type RecordingStop = { since: string; reason: string; source: "hive" | "cloud" };

/** The stop to show: the hive's answer when there is one, else the cloud's; undefined while recording. */
export function recordingStop(hive: Hive, info: HiveInfoAnswer | undefined): RecordingStop | undefined {
  if (info) return info.not_recording ? { since: info.not_recording.since, reason: info.not_recording.reason, source: "hive" } : undefined;
  const r = hive.recording_stopped;
  return r ? { since: r.since, reason: r.reason, source: "cloud" } : undefined;
}
