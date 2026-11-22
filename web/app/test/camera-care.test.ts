import { describe, expect, it } from "vitest";
import i18n from "i18next";
import type { CameraCare } from "@camhive/api";
import { careFacts } from "../src/lib/cameraCare";
import "../src/i18n";

const thresholds = { bitrate_drop: 0.6, bitrate_ok: 0.8, night_picture: 0.25, night_ok: 0.35, missing_s: 30, missing_ok_s: 5, reconnects: 4, reconnects_ok: 1,
  clock_drift_s: 30, clock_every_h: 6, pattern_days: 3, pattern_minutes: 5, stream_changed_h: 24, baseline_days: 7, baseline_min_days: 3, late_checks: 3 };

export const sick: CameraCare = {
  camera: "cam_1", checked_at: "2026-11-22T23:00:00Z", degraded: true, since: "2026-11-22T22:40:00Z",
  reasons: [
    { reason: "bitrate_drop", suggestion: "Check the cable or the PoE budget." },
    { reason: "frame_loss", suggestion: "" },
    { reason: "reconnects", suggestion: "Check the cable or the PoE budget." },
    { reason: "clock_drift", suggestion: "Set the camera's NTP server." },
    { reason: "offline_pattern", suggestion: "" },
    { reason: "night_picture", suggestion: "The IR illuminator may have failed." },
    { reason: "stream_changed", suggestion: "" },
  ],
  signals: {
    streams: [{ stream: "main", last_hour_kbps: 400, usual_kbps: 2000, day_kbps: 4000, night: true }, { stream: "sub", last_hour_kbps: 500, usual_kbps: 500, day_kbps: 500, night: true }],
    missing_s: 95, late_s: 0, reconnects_hour: 5, reconnects_24h: 14, clock_drift_s: -42, clock_checked_at: "2026-11-22T20:00:00Z",
    offline_minutes: [], pattern_hours: [3, 4],
    stream_change: { stream: "main", at: "2026-11-22T21:00:00Z", from: { codec: "H264", width: 1920, height: 1080 }, to: { codec: "H264", width: 640, height: 360 } },
  },
  thresholds,
};

describe("camera care facts (#163)", () => {
  it("says what the hive saw, one sentence per reason, with what to do", async () => {
    await i18n.changeLanguage("en");
    const facts = careFacts(sick, i18n.t.bind(i18n));
    expect(facts.map((f) => f.text)).toEqual([
      "The main stream sent 20 % of its usual bitrate in the last hour.",
      "95 s of footage went missing in the last hour.",
      "Reconnected 14 times in the last 24 hours.",
      "The camera's clock is 42 s off.",
      "Offline around 03:00 and 04:00 on several days.",
      "At night the main stream drops to 10 % of its daytime bitrate.",
      "The main stream changed from H264 1920×1080 to H264 640×360.",
    ]);
    expect(facts.map((f) => f.advice)).toEqual([
      "A quieter scene also lowers the bitrate; if the picture looks wrong, check the cable or the PoE budget.",
      "Check the cable, then update the camera's firmware.",
      "Check the cable or the PoE budget.",
      "Set the camera's NTP server.",
      "Check what switches the camera's power or network off at those hours.",
      "The IR illuminator may have failed.",
      "Check the camera's video settings, and update the camera's firmware if it reset them.",
    ]);
  });

  it("speaks German and names late segments", async () => {
    await i18n.changeLanguage("de");
    const late: CameraCare = { ...sick, reasons: [{ reason: "frame_loss", suggestion: "" }], signals: { ...sick.signals, missing_s: 0, late_s: 600 } };
    expect(careFacts(late, i18n.t.bind(i18n))[0]).toEqual({ reason: "frame_loss",
      text: "Neue Aufnahmen kommen seit 10 Minuten zu spät.", advice: "Prüfen Sie das Kabel und aktualisieren Sie dann die Firmware der Kamera." });
    await i18n.changeLanguage("en");
  });
});
