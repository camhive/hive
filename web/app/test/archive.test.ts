import { describe, expect, it } from "vitest";
import { DAY_MS, clock, coverage, dayKey, dayStart, exportCapMinutes, nextRecorded, recordedAt, ticks, timeAt, zoomWindow } from "../src/lib/archive";
import { mimeFor, playPath } from "../src/lib/archivePlayer";

const day = dayStart("2026-11-09");
const at = (h: number, m = 0) => new Date(day.getTime() + (h * 60 + m) * 60_000).toISOString();

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
