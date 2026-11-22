import { describe, expect, it } from "vitest";
import { deletesFootage, fillsLine, forecastLine, formatDays, formatSize, validateStorage, type CameraStorage, type StorageOverview } from "../src/lib/storage";

const GB = 1e9;
const GIB = 2 ** 30;

const cam = (patch: Partial<CameraStorage> = {}): CameraStorage => ({
  id: "cam_a", name: "Door", bytes: 240 * GB, days_held: 7.96, oldest: "", newest: "", oldest_age_days: 7.97,
  bytes_per_day_24h: 30 * GB, bytes_per_day_7d: 28 * GB, retention_days: 30, follows_hive: true, effective_days: 11, ...patch,
});

const overview = (patch: Partial<StorageOverview> = {}): StorageOverview => ({
  cameras: [cam()],
  disk: { total_bytes: 1000 * GB, archive_bytes: 384 * GB, export_bytes: 0, free_bytes: 190 * GB },
  settings: { retention_days: 30, min_free_gb: 10, export_max_gb: 10 },
  forecast: { bytes_per_day: 46 * GB, capacity_bytes: 552 * GB, disk_days: 11, horizon_days: 11, needed_bytes: 1401 * GB, fills_in_days: 3, retention_met: false, deleting_for_space: false },
  ...patch,
});
const met = { bytes_per_day: 10 * GB, capacity_bytes: 1500 * GB, disk_days: 150, retention_met: true, deleting_for_space: false };

describe("storage arithmetic in the app (#150)", () => {
  it("validates like the hive: retention 0-3650 whole days, a floor of at least 1 GiB below the disk", () => {
    const total = 1000 * GB; // 931.3 GiB
    expect(validateStorage({ retention_days: "30", min_free_gb: "10" }, total)).toEqual({ retention_days: 30, min_free_gb: 10 });
    expect(validateStorage({ retention_days: "0", min_free_gb: "931" }, total)).toEqual({ retention_days: 0, min_free_gb: 931 });
    for (const bad of [{ retention_days: "-1", min_free_gb: "10" }, { retention_days: "3651", min_free_gb: "10" }, { retention_days: "1.5", min_free_gb: "10" }, { retention_days: "", min_free_gb: "10" }])
      expect(validateStorage(bad, total)).toEqual({ invalid: "retention_days" });
    for (const bad of ["0", "932", "x"])
      expect(validateStorage({ retention_days: "30", min_free_gb: bad }, total)).toEqual({ invalid: "min_free_gb" });
  });

  it("says what the disk holds and what retention becomes", () => {
    expect(forecastLine(overview(), "en")).toEqual({ key: "storage.forecast.short", values: { rate: "46 GB", days: 11, retention: 30, effective: 11 } });
    expect(forecastLine(overview({ forecast: met }), "en").key).toBe("storage.forecast.fits");
    expect(forecastLine(overview({ settings: { retention_days: 0, min_free_gb: 10, export_max_gb: 10 } }), "en").key).toBe("storage.forecast.unlimited");
    expect(forecastLine(overview({ settings: { retention_days: 0, min_free_gb: 10, export_max_gb: 10 }, forecast: met }), "en").key).toBe("storage.forecast.unlimitedPlain");
    expect(forecastLine(overview({ forecast: { ...met, bytes_per_day: 0 } }), "en").key).toBe("storage.forecast.nothing");
  });

  it("explains a fit the disk alone would not give: a camera keeps only a few days", () => {
    const o = overview({ cameras: [cam({ name: "Yard", retention_days: 7, follows_hive: false, effective_days: 7 }), cam({ id: "cam_b", bytes_per_day_24h: 2 * GB, bytes_per_day_7d: 2 * GB })],
      forecast: { ...met, disk_days: 11 } });
    expect(forecastLine(o, "en")).toEqual({ key: "storage.forecast.fitsOwn", values: { rate: "10 GB", days: 11, retention: 30, effective: 30, camera: "Yard", own: 7 } });
  });

  it("applies the licence cap to the retention it names", () => {
    const o = overview({ settings: { retention_days: 30, min_free_gb: 10, export_max_gb: 10, retention_days_max: 14 } });
    expect(forecastLine(o, "en").values.retention).toBe(14);
  });

  it("tells deleting now from within a day", () => {
    const f = overview().forecast!;
    expect(fillsLine(overview())).toEqual({ key: "storage.fillsIn", values: { count: 3 } });
    expect(fillsLine(overview({ forecast: { ...f, fills_in_days: 0 } }))?.key).toBe("storage.fillsSoon");
    expect(fillsLine(overview({ forecast: { ...f, fills_in_days: 0, deleting_for_space: true } }))).toEqual({ key: "storage.fillsNow", values: { floor: 10 } });
    expect(fillsLine(overview({ forecast: met }))).toBeNull();
  });

  it("knows when a change deletes footage at the next sweep, by the oldest footage's age", () => {
    const o = overview();
    expect(deletesFootage(o, { retention_days: 30, min_free_gb: 10 })).toBe(false);
    expect(deletesFootage(o, { retention_days: 5, min_free_gb: 10 })).toBe(true); // Door's oldest is 7.97 days old
    expect(deletesFootage(o, { retention_days: 0, min_free_gb: 10 })).toBe(false);
    // Stopped 10 days ago, holding 5 days: retention 12 deletes it although it holds less.
    const stopped = overview({ cameras: [cam({ days_held: 5, oldest_age_days: 15 })] });
    expect(deletesFootage(stopped, { retention_days: 12, min_free_gb: 10 })).toBe(true);
    // A camera with its own retention is not touched by the hive-wide value.
    expect(deletesFootage(overview({ cameras: [cam({ retention_days: 60, follows_hive: false, oldest_age_days: 50 })] }), { retention_days: 5, min_free_gb: 10 })).toBe(false);
    // Under a licence cap of 14 the cameras read 14 but follow the hive: lowering to 7 deletes days 8-14.
    const capped = overview({ settings: { retention_days: 30, min_free_gb: 10, export_max_gb: 10, retention_days_max: 14 }, cameras: [cam({ retention_days: 14, oldest_age_days: 13.9 })] });
    expect(deletesFootage(capped, { retention_days: 7, min_free_gb: 10 })).toBe(true);
    expect(deletesFootage(capped, { retention_days: 60, min_free_gb: 10 })).toBe(false);
    // The floor deletes only when raised above the free space.
    expect(deletesFootage(o, { retention_days: 30, min_free_gb: 176 })).toBe(false); // 190 GB free = 176.95 GiB
    expect(deletesFootage(o, { retention_days: 30, min_free_gb: Math.ceil((190 * GB) / GIB) })).toBe(true);
    const tight = overview({ settings: { retention_days: 30, min_free_gb: 500, export_max_gb: 10 } });
    expect(deletesFootage(tight, { retention_days: 30, min_free_gb: 500 })).toBe(false);
  });

  it("rounds rates and needed space up, days down, with the reader's decimal sign", () => {
    expect(formatSize(46.04 * GB, "en", "up")).toBe("46.1 GB");
    expect(formatSize(46.04 * GB, "en")).toBe("46 GB");
    expect(formatSize(1461 * GB, "en", "up")).toBe("1.5 TB");
    expect(formatSize(46.04 * GB, "de", "up")).toBe("46,1 GB");
    expect(formatDays(2.58, "de")).toBe("2,5");
    expect(formatDays(7.96, "en")).toBe("7.9");
  });
});
