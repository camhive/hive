import { describe, expect, it } from "vitest";
import { GROUPS, connectionHints, footageFacts, localLoginFacts, moveFacts, stripItems, updatesFacts, type StripInput } from "../src/lib/hiveSettings";
import type { StorageOverview } from "../src/lib/storage";
import type { Decision } from "../src/lib/transport";

const GB = 1e9;
const ALL = ["archive.autolock", "archive.delete", "archive.encryption", "disk.health", "local_login", "storage", "updates"];
const hive = { id: "hv_1", version: "0.1.10", capabilities: ALL, lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] } };
const lan: Decision = { path: "lan", reason: "lan_ok", at: 0 };
const relay: Decision = { path: "relay", reason: "lan_failed", at: 0 };

const overview = (patch: Partial<StorageOverview> = {}): StorageOverview => ({
  cameras: [{ id: "cam_a", name: "Door", bytes: 110 * GB, days_held: 13.6 }],
  disk: { total_bytes: 1000 * GB, archive_bytes: 110 * GB, export_bytes: 0, free_bytes: 800 * GB, health: { state: "ok", evidence: [] } },
  settings: { retention_days: 30, min_free_gb: 10, export_max_gb: 10 },
  forecast: { bytes_per_day: 8 * GB, capacity_bytes: 790 * GB, disk_days: 98, fills_in_days: 40, retention_met: false, deleting_for_space: false },
  ...patch,
});

const known = <T>(data: T) => ({ data, loading: false });
const input = (patch: Partial<StripInput> = {}): StripInput => ({
  hive, owner: true, decision: lan, lang: "en",
  storage: known(overview()),
  encryption: known({ state: "locked" as const, sealing: true }),
  updates: known({ updates: "automatic" as const, update_window: "02:00-05:00", update_channel: "stable" as const, running: "0.1.10", supported: true }),
  ...patch,
});
const byId = (items: ReturnType<typeof stripItems>) => Object.fromEntries(items.map((i) => [i.id, i.facts]));

describe("the settings status strip (#174)", () => {
  it("shows the six states of a v0.1.10 hive in order", () => {
    const items = stripItems(input());
    expect(items.map((i) => i.id)).toEqual(["version", "path", "disk", "footage", "encryption", "updates"]);
    const s = byId(items);
    expect(s.version).toEqual([{ key: "settings.fact.version", values: { version: "0.1.10" } }]);
    expect(s.path).toEqual([{ key: "settings.fact.path.direct", tone: "good" }]);
    expect(s.disk).toEqual([{ key: "settings.fact.disk.ok", tone: "good" }]);
    expect(s.footage).toEqual([{ key: "settings.fact.held", values: { size: "110 GB", count: 13 } }, { key: "settings.fact.fillsIn", values: { count: 40 } }]);
    expect(s.encryption).toEqual([{ key: "settings.fact.encryption.locked" }]);
    expect(s.updates).toEqual([{ key: "settings.fact.updates.automatic" }]);
  });

  it("says which version brings what an older hive does not report, and never asks it", () => {
    const old = { ...hive, version: "0.1.6", capabilities: undefined };
    const s = byId(stripItems(input({ hive: old, storage: { loading: false }, encryption: { loading: false }, updates: { loading: false } })));
    expect(s.version).toEqual([{ key: "settings.fact.version", values: { version: "0.1.6" } }]);
    expect(s.disk).toEqual([{ key: "settings.fact.notReported", values: { since: "0.1.10" } }]);
    expect(s.footage).toEqual([{ key: "settings.fact.notReported", values: { since: "0.1.10" } }]);
    expect(s.encryption).toEqual([{ key: "settings.fact.notReported", values: { since: "0.1.7" } }]);
    expect(s.updates).toEqual([{ key: "settings.fact.notReported", values: { since: "0.1.7" } }]);
  });

  it("tells checking from not known: a pending answer, an unreachable hive", () => {
    const pending = byId(stripItems(input({ decision: null, storage: { loading: true }, updates: { loading: true } })));
    expect(pending.path).toEqual([{ key: "transport.probing" }]);
    expect(pending.disk).toEqual([{ key: "settings.fact.checking" }]);
    expect(pending.updates).toEqual([{ key: "settings.fact.checking" }]);
    const gone = byId(stripItems(input({ hive: { ...hive, version: undefined }, decision: { path: "none", reason: "relay_unavailable", at: 0 }, storage: { loading: false }, encryption: { loading: false } })));
    expect(gone.version).toEqual([{ key: "settings.fact.unknownVersion" }]);
    expect(gone.path).toEqual([{ key: "settings.fact.path.none", tone: "bad" }]);
    expect(gone.footage).toEqual([{ key: "settings.fact.unknown" }]);
    expect(gone.encryption).toEqual([{ key: "settings.fact.unknown" }]);
  });

  it("marks a failing disk, an unsealed archive, the relay and a build that never updates", () => {
    const failing = overview({ disk: { ...overview().disk!, health: { state: "failing", evidence: [] } } });
    const s = byId(stripItems(input({ decision: relay, storage: known(failing), encryption: known({ state: "unlocked", sealing: false }),
      updates: known({ updates: "manual", update_window: "02:00-05:00", update_channel: "stable", running: "dev", supported: false }) })));
    expect(s.path).toEqual([{ key: "settings.fact.path.relay" }]);
    expect(s.disk).toEqual([{ key: "settings.fact.disk.failing", tone: "warn" }]);
    expect(s.encryption).toEqual([{ key: "settings.fact.encryption.unlocked" }, { key: "settings.fact.encryption.notSealing", tone: "warn" }]);
    expect(s.updates).toEqual([{ key: "settings.fact.updates.unsupported" }]);
    const failed = overview({ disk: { ...overview().disk!, health: { state: "failed", evidence: [] } } });
    expect(byId(stripItems(input({ storage: known(failed) }))).disk).toEqual([{ key: "settings.fact.disk.failed", tone: "bad" }]);
    const unread = overview({ disk: { ...overview().disk!, health: { state: "unknown", evidence: [] } } });
    expect(byId(stripItems(input({ storage: known(unread) }))).disk).toEqual([{ key: "settings.fact.disk.unknown" }]);
  });

  it("says the hive is offline where that is why nothing is known", () => {
    const off = { loading: false, offline: true };
    const s = byId(stripItems(input({ storage: off, encryption: off, updates: off })));
    expect(s.disk).toEqual([{ key: "settings.fact.offline" }]);
    expect(s.footage).toEqual([{ key: "settings.fact.offline" }]);
    expect(s.encryption).toEqual([{ key: "settings.fact.offline" }]);
    expect(s.updates).toEqual([{ key: "settings.fact.offline" }]);
  });

  it("gives a member the version and the path only: the rest is the owner's", () => {
    expect(stripItems(input({ owner: false })).map((i) => i.id)).toEqual(["version", "path"]);
  });
});

describe("footage held and what the disk does next", () => {
  it("says when the disk is full, fills, or keeps the retention", () => {
    const f = overview().forecast!;
    expect(footageFacts(overview({ forecast: { ...f, deleting_for_space: true } }), "en")[1]).toEqual({ key: "settings.fact.fullNow", tone: "warn" });
    expect(footageFacts(overview({ forecast: { ...f, fills_in_days: 0 } }), "en")[1]).toEqual({ key: "settings.fact.fillsSoon", tone: "warn" });
    expect(footageFacts(overview({ forecast: { ...f, fills_in_days: undefined, retention_met: true } }), "en")[1]).toEqual({ key: "settings.fact.keeps", values: { count: 30 } });
    expect(footageFacts(overview({ forecast: { ...f, bytes_per_day: 0, fills_in_days: undefined } }), "en")).toHaveLength(1);
  });

  it("says the same as the Storage panel: deleting for space even with nothing recorded lately (review of #174)", () => {
    const f = overview().forecast!;
    const idle = overview({ forecast: { ...f, bytes_per_day: 0, fills_in_days: undefined, deleting_for_space: true } });
    expect(footageFacts(idle, "en")[1]).toEqual({ key: "settings.fact.fullNow", tone: "warn" });
  });

  it("follows the panel's forecast when the disk does not fill: own retention, short, until full", () => {
    const f = overview().forecast!;
    const met = { ...f, fills_in_days: undefined, retention_met: true };
    const own = overview({ forecast: { ...met, disk_days: 20 }, cameras: [{ id: "c", name: "Door", bytes: 110 * GB, days_held: 13.6, bytes_per_day_24h: 8 * GB, retention_days: 7, follows_hive: false }] });
    expect(footageFacts(own, "en")[1]).toEqual({ key: "settings.fact.keepsOwn", values: { count: 30, camera: "Door", own: 7 } });
    const short = overview({ forecast: { ...f, fills_in_days: undefined, horizon_days: 21 } });
    expect(footageFacts(short, "en")[1]).toEqual({ key: "settings.fact.keepsShort", values: { count: 21, retention: 30 }, tone: "warn" });
    const unlimited = overview({ settings: { retention_days: 0, min_free_gb: 10, export_max_gb: 10 }, forecast: { ...f, fills_in_days: undefined, horizon_days: 98 } });
    expect(footageFacts(unlimited, "en")[1]).toEqual({ key: "settings.fact.keepsUntilFull", values: { count: 98 } });
  });

  it("holds less than a day, and nothing yet", () => {
    expect(footageFacts(overview({ cameras: [{ id: "c", bytes: 1e8, days_held: 0.4 }] }), "de")[0]).toEqual({ key: "settings.fact.heldToday", values: { size: "110 GB" } });
    expect(footageFacts(overview({ cameras: [], disk: { ...overview().disk!, archive_bytes: 0 } }), "en")).toEqual([{ key: "settings.fact.noFootage" }]);
  });
});

describe("the connection warnings appear only in their conditions", () => {
  const chrome = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36";
  const firefox = "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0";

  it("shows the router hint when the LAN name failed although the hive reports addresses", () => {
    expect(connectionHints(hive, relay, firefox).rebind).toBe(true);
    expect(connectionHints({ ...hive, lan: { ...hive.lan, addrs: [] } }, relay, firefox).rebind).toBe(false);
    expect(connectionHints(hive, lan, firefox).rebind).toBe(false);
    expect(connectionHints(hive, null, firefox).rebind).toBe(false);
  });

  it("shows the Chrome permission note in Chromium browsers on the direct path only", () => {
    expect(connectionHints(hive, lan, chrome).chrome).toBe(true);
    expect(connectionHints(hive, { path: "address", reason: "address_ok", at: 0, address: "192.168.1.20:16017" }, chrome).chrome).toBe(true);
    expect(connectionHints(hive, relay, chrome).chrome).toBe(false);
    expect(connectionHints(hive, lan, firefox).chrome).toBe(false);
  });
});

it("orders the groups as the owner reads them", () => {
  expect(GROUPS).toEqual(["connection", "recordings", "access", "software", "danger"]);
});

describe("the rows' states", () => {
  it("says what runs, how it updates and what waits", () => {
    const u = { updates: "automatic" as const, update_window: "02:00-05:00", update_channel: "stable" as const, running: "0.1.10", supported: true };
    expect(updatesFacts(u)).toEqual([{ key: "settings.fact.version", values: { version: "0.1.10" } }, { key: "settings.fact.updates.automatic" }, { key: "settings.fact.updates.window", values: { from: "02:00", to: "05:00" } }]);
    expect(updatesFacts({ ...u, updates: "manual", available: "0.1.11" }).slice(1)).toEqual([{ key: "settings.fact.updates.manual" }, { key: "settings.fact.updates.available", values: { version: "0.1.11" }, tone: "warn" }]);
    expect(updatesFacts({ ...u, supported: false, available: "0.1.11" }).slice(1)).toEqual([{ key: "settings.fact.updates.unsupported" }]);
  });

  it("says whether local sign-in is on and for whom", () => {
    expect(localLoginFacts({ enabled: false })).toEqual([{ key: "settings.fact.localLogin.off" }]);
    expect(localLoginFacts({ enabled: true, username: "anna" })).toEqual([{ key: "settings.fact.localLogin.on", values: { username: "anna" } }]);
  });

  it("says whether this hive can move, and how far a move has come", () => {
    const m = { state: "none" as const, supported: true, files: [] };
    expect(moveFacts(m)).toEqual([{ key: "settings.fact.move.ready" }]);
    expect(moveFacts({ ...m, supported: false })).toEqual([{ key: "settings.fact.move.unsupported" }]);
    expect(moveFacts({ ...m, state: "copying", copied_bytes: 25, planned_bytes: 100 })).toEqual([{ key: "settings.fact.move.copying", values: { pct: 25 }, tone: "warn" }]);
    expect(moveFacts({ ...m, state: "switching" })).toEqual([{ key: "settings.fact.move.switching", tone: "warn" }]);
    expect(moveFacts({ ...m, state: "done", to: "/mnt/new" })).toEqual([{ key: "settings.fact.move.done", values: { to: "/mnt/new" } }]);
    expect(moveFacts({ ...m, state: "failed" })).toEqual([{ key: "settings.fact.move.ready" }, { key: "settings.fact.move.failed" }]);
  });
});

describe("the strip says a hive does not record (#172)", () => {
  const since = "2026-11-24T10:30:00Z";
  it("puts the stop first in the disk item, with its time and reason, as bad", () => {
    const s = byId(stripItems(input({ stop: { since, reason: "archive_missing", source: "hive" } })));
    expect(s.disk?.[0]).toEqual({ key: "settings.fact.recordingStopped", values: { time: new Date(since).toLocaleString("en"), reason: "archive_missing" }, tone: "bad" });
    expect(s.disk?.[1]).toEqual({ key: "settings.fact.disk.ok", tone: "good" });
  });

  it("says nothing while recording, and a reason it does not know as unknown", () => {
    expect(byId(stripItems(input())).disk).toEqual([{ key: "settings.fact.disk.ok", tone: "good" }]);
    const s = byId(stripItems(input({ stop: { since, reason: "something_new", source: "cloud" } })));
    expect(s.disk?.[0]?.values?.reason).toBe("unknown");
  });

  it("reads as one sentence in both languages", async () => {
    const i18n = (await import("../src/i18n")).default;
    const values = { time: "24.11.2026, 11:30:00", reason: "database" };
    expect(i18n.getFixedT("en")("settings.fact.recordingStopped", values)).toBe("recording stopped since 24.11.2026, 11:30:00: its database reports disk errors");
    expect(i18n.getFixedT("de")("settings.fact.recordingStopped", values)).toBe("Aufnahme gestoppt seit 24.11.2026, 11:30:00: seine Datenbank meldet Festplattenfehler");
  });
});
