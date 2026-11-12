import { describe, expect, it } from "vitest";
import { DAY_MS, MIN_SPAN, activityRuns, clock, clockSeconds, coverage, dayKey, dayStart, dayStrip, exportCapMinutes, nextRecorded, recordedAt, snapshotName, ticks, timeAt, zoomAround, zoomWindow } from "../src/lib/archive";
import { mimeFor, playPath } from "../src/lib/archivePlayer";

const day = dayStart("2026-11-09");
const at = (h: number, m = 0) => new Date(day.getTime() + (h * 60 + m) * 60_000).toISOString();

describe("activity on the timeline (ADR-0015)", () => {
  it("turns segment profiles into runs of points scaled 0..1, one run per segment", () => {
    const runs = activityRuns([
      { start: at(10), duration_ms: 30_000, activity: { step_s: 10, video_kbps: [100, 400, 0], sound_db: [-90, -30, 0] } },
      { start: at(11), duration_ms: 20_000, activity: { step_s: 10, video_kbps: [200, 200] } }, // no audio recorded
      { start: at(12), duration_ms: 600_000 }, // not profiled yet
      { start: new Date(day.getTime() - 60_000).toISOString(), duration_ms: 30_000, activity: { step_s: 10, video_kbps: [9999] } }, // yesterday
    ], day);
    const t0 = Date.parse(at(10));
    // Points sit in the middle of their 10 s; video is scaled to the day's busiest value.
    expect(runs.video).toEqual([
      [{ at: t0 + 5_000, v: 0.25 }, { at: t0 + 15_000, v: 1 }, { at: t0 + 25_000, v: 0 }],
      [{ at: Date.parse(at(11)) + 5_000, v: 0.5 }, { at: Date.parse(at(11)) + 15_000, v: 0.5 }],
    ]);
    // Sound from -60 dBFS (quiet room) to 0 dBFS; silence and quieter sit at 0.
    expect(runs.sound).toEqual([[{ at: t0 + 5_000, v: 0 }, { at: t0 + 15_000, v: 0.5 }, { at: t0 + 25_000, v: 1 }]]);
    expect(activityRuns([], day)).toEqual({ video: [], sound: [] });
  });
});

describe("timeline math", () => {
  it("builds a zoom window inside the day and ruler ticks that scale with it", () => {
    const d0 = dayStart("2026-11-10");
    expect(zoomWindow(d0, DAY_MS, null)).toEqual([d0.getTime(), d0.getTime() + DAY_MS]);
    // 1 h around 00:10 is clamped to the start of the day; around 23:50 to its end.
    expect(zoomWindow(d0, 3_600_000, d0.getTime() + 600_000)).toEqual([d0.getTime(), d0.getTime() + 3_600_000]);
    expect(zoomWindow(d0, 3_600_000, d0.getTime() + DAY_MS - 600_000)).toEqual([d0.getTime() + DAY_MS - 3_600_000, d0.getTime() + DAY_MS]);
    // 6 h centred on noon.
    expect(zoomWindow(d0, 6 * 3_600_000, d0.getTime() + 12 * 3_600_000)).toEqual([d0.getTime() + 9 * 3_600_000, d0.getTime() + 15 * 3_600_000]);

    const day = ticks([d0.getTime(), d0.getTime() + DAY_MS]);
    expect(day.filter((x) => x.major).map((x) => x.label)).toEqual(["00:00", "02:00", "04:00", "06:00", "08:00", "10:00", "12:00", "14:00", "16:00", "18:00", "20:00", "22:00", "00:00"]);
    expect(day.filter((x) => !x.major)).toHaveLength(12);
    const hour = ticks([d0.getTime() + 8 * 3_600_000, d0.getTime() + 9 * 3_600_000]);
    expect(hour.filter((x) => x.major).map((x) => x.label)).toEqual(["08:00", "08:10", "08:20", "08:30", "08:40", "08:50", "09:00"]);
    expect(hour.filter((x) => !x.major)).toHaveLength(6);
    expect(clock(new Date(d0.getTime() + 5 * 60_000))).toBe("00:05");
  });

  it("round-trips day keys and clips, merges and sorts segments", () => {
    expect(dayKey(day)).toBe("2026-11-09");
    const ranges = coverage([
      { start: at(10, 30), duration_ms: 60_000 },
      { start: at(10, 31), duration_ms: 60_000 }, // touching → merged
      { start: at(8), duration_ms: 3_600_000 },
      { start: at(23, 50), duration_ms: 3_600_000 }, // runs past midnight → clipped
      { start: new Date(day.getTime() - 60_000).toISOString(), duration_ms: 30_000 }, // yesterday → dropped
    ], day);
    expect(ranges).toEqual([
      [day.getTime() + 8 * 3_600_000, day.getTime() + 9 * 3_600_000],
      [Date.parse(at(10, 30)), Date.parse(at(10, 32))],
      [Date.parse(at(23, 50)), day.getTime() + DAY_MS],
    ]);
    expect(recordedAt(ranges, new Date(at(8, 30)))).toEqual(ranges[0]);
    expect(recordedAt(ranges, new Date(at(9, 30)))).toBeNull();
    expect(nextRecorded(ranges, new Date(at(9, 30)))?.toISOString()).toBe(at(10, 30));
    expect(nextRecorded(ranges, new Date(at(23, 55)))?.toISOString()).toBe(at(23, 55));
    expect(nextRecorded([], new Date(at(1)))).toBeNull();
    expect(timeAt(day, 0.5).toISOString()).toBe(at(12));
    expect(timeAt(day, 2).getTime()).toBe(day.getTime() + DAY_MS);
  });
  it("derives the export cap and player paths", () => {
    expect(exportCapMinutes(-1)).toBe(1440);
    expect(exportCapMinutes(undefined)).toBe(1440);
    expect(exportCapMinutes(10)).toBe(10);
    expect(mimeFor("H264")).toContain("avc1");
    expect(mimeFor("H265")).toContain("hvc1");
    expect(mimeFor(undefined)).toContain("avc1");
    expect(playPath("cam 1", "sub", new Date("2026-11-09T10:00:00Z"), 90.7)).toBe("/v0/cameras/cam%201/archive/play?stream=sub&start=2026-11-09T10%3A00%3A00.000Z&duration=90");
  });
});

describe("timeline gestures", () => {
  const d0 = day.getTime();
  it("zooms around the point under the pointer, keeping it where it was on screen", () => {
    // 6 h window 06:00–12:00, zoom in ×2 around 07:30 (a quarter in): 07:30 stays a quarter in.
    const [span, center] = zoomAround(day, [d0 + 6 * 3_600_000, d0 + 12 * 3_600_000], d0 + 7.5 * 3_600_000, 0.5);
    expect(span).toBe(3 * 3_600_000);
    const [from, to] = zoomWindow(day, span, center);
    expect((d0 + 7.5 * 3_600_000 - from) / (to - from)).toBeCloseTo(0.25);
  });
  it("never zooms past a day or under the minimum span", () => {
    expect(zoomAround(day, [d0, d0 + DAY_MS], d0, 4)[0]).toBe(DAY_MS);
    expect(zoomAround(day, [d0, d0 + MIN_SPAN], d0, 0.1)[0]).toBe(MIN_SPAN);
  });
  it("prints the time with seconds", () => {
    expect(clockSeconds(new Date(d0 + (8 * 3600 + 5 * 60 + 7) * 1000))).toBe("08:05:07");
  });
});

describe("ruler on a narrow bar", () => {
  it("labels fewer major ticks so labels keep apart, on whole clock hours", () => {
    const labels = ticks([day.getTime(), day.getTime() + DAY_MS], 340).filter((m) => m.label).map((m) => m.label);
    expect(labels).toEqual(["00:00", "04:00", "08:00", "12:00", "16:00", "20:00", "00:00"]);
  });
});

describe("day strip", () => {
  it("lists every day from the oldest footage to today, marks those with footage and where a month starts", () => {
    const strip = dayStrip(["2026-10-30", "2026-11-01"], "2026-11-02");
    expect(strip.map((d) => d.key)).toEqual(["2026-10-30", "2026-10-31", "2026-11-01", "2026-11-02"]);
    expect(strip.map((d) => d.footage)).toEqual([true, false, true, false]);
    expect(strip.map((d) => d.monthStart)).toEqual([true, false, true, false]);
    expect(strip[2]!.date.getDate()).toBe(1);
  });
  it("shows just today without footage and stays bounded for a very deep archive", () => {
    expect(dayStrip([], "2026-11-02").map((d) => d.key)).toEqual(["2026-11-02"]);
    expect(dayStrip(["2020-01-01"], "2026-11-02")).toHaveLength(366);
  });
});

describe("snapshots", () => {
  it("names the file after the camera and the moment shown", () => {
    expect(snapshotName("Front door", new Date(day.getTime() + (8 * 3600 + 30 * 60 + 5) * 1000))).toBe("Front-door_2026-11-09_08-30-05.png");
  });
});
