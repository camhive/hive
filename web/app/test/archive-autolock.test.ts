import { describe, expect, it } from "vitest";
import { PresenceReporter, PRESENCE_EVERY_MS, locksIn, presenceOf } from "../src/lib/archiveAutolock";

/** A clock and a timer queue the test moves by hand. */
function fakeTime() {
  let now = 0;
  let timers: { at: number; f: () => void }[] = [];
  return {
    now: () => now,
    schedule: (f: () => void, ms: number) => { timers.push({ at: now + ms, f }); },
    pass(ms: number) {
      now += ms;
      const due = timers.filter((t) => t.at <= now);
      timers = timers.filter((t) => t.at > now);
      due.forEach((t) => t.f());
    },
  };
}

function reporter() {
  const time = fakeTime();
  const sent: boolean[] = [];
  const r = new PresenceReporter(time.now, time.schedule);
  const report = (atHome: boolean, opts?: { now?: boolean }) => r.report("hv_1", atHome, async (v) => { sent.push(v); }, opts);
  return { time, sent, report, r };
}

describe("the presence report (#147)", () => {
  it("sends on page load and on every change, never more than once a minute, the latest value last", () => {
    const { time, sent, report } = reporter();
    report(true); // page load
    expect(sent).toEqual([true]);
    time.pass(10_000);
    report(false); // the decision moved to the relay
    report(true);
    report(false);
    expect(sent).toEqual([true]);
    time.pass(PRESENCE_EVERY_MS - 10_000);
    expect(sent).toEqual([true, false]);
    time.pass(PRESENCE_EVERY_MS);
    expect(sent).toEqual([true, false]);
    report(false); // the heartbeat a minute later reports again
    expect(sent).toEqual([true, false, false]);
  });

  it("sends a heartbeat with the same value: the hive may have restarted and forgotten", () => {
    const { time, sent, report } = reporter();
    report(false);
    time.pass(30_000);
    report(false);
    time.pass(PRESENCE_EVERY_MS);
    expect(sent).toEqual([false, false]);
  });

  it("reports an unlock or a restart of the hive at once", () => {
    const { time, sent, report } = reporter();
    report(false);
    time.pass(5_000);
    report(false, { now: true });
    expect(sent).toEqual([false, false]);
  });

  it("keeps hives apart", () => {
    const { sent, r } = reporter();
    const other: boolean[] = [];
    r.report("hv_1", true, async (v) => { sent.push(v); });
    r.report("hv_2", false, async (v) => { other.push(v); });
    expect(sent).toEqual([true]);
    expect(other).toEqual([false]);
  });

  it("survives a failed request and keeps reporting", async () => {
    const time = fakeTime();
    const r = new PresenceReporter(time.now, time.schedule);
    r.report("hv_1", false, () => Promise.reject(new Error("offline")));
    await Promise.resolve();
    const sent: boolean[] = [];
    time.pass(PRESENCE_EVERY_MS);
    r.report("hv_1", false, async (v) => { sent.push(v); }); // the same value: the failed one did not count as told
    expect(sent).toEqual([false]);
  });

  it("is at home on the LAN or in local mode, away through the relay, unknown while probing or unreachable", () => {
    const at = 0;
    expect(presenceOf(false, { path: "relay", reason: "lan_failed", at }, true)).toBeNull();
    expect(presenceOf(false, { path: "lan", reason: "lan_ok", at }, true)).toBeNull();
    expect(presenceOf(false, { path: "lan", reason: "lan_ok", at })).toBe(true);
    expect(presenceOf(false, { path: "address", reason: "address_ok", at, address: "192.168.1.20:16017" })).toBe(true);
    expect(presenceOf(true, { path: "none", reason: "relay_unavailable", at })).toBe(true);
    expect(presenceOf(false, { path: "relay", reason: "lan_failed", at })).toBe(false);
    expect(presenceOf(false, { path: "none", reason: "relay_unavailable", at })).toBeNull();
    expect(presenceOf(false, null)).toBeNull();
  });
});

describe("when the archive locks itself", () => {
  const now = Date.parse("2026-11-22T09:00:00Z");
  it("counts minutes under two hours, then hours, then days, rounding up", () => {
    expect(locksIn("2026-11-22T09:42:00Z", now)).toEqual({ unit: "minutes", count: 42 });
    expect(locksIn("2026-11-22T09:00:20Z", now)).toEqual({ unit: "minutes", count: 1 });
    expect(locksIn("2026-11-22T08:59:00Z", now)).toEqual({ unit: "minutes", count: 1 }); // due: the hive locks within 30 s
    expect(locksIn("2026-11-22T10:59:00Z", now)).toEqual({ unit: "minutes", count: 119 });
    expect(locksIn("2026-11-22T17:00:00Z", now)).toEqual({ unit: "hours", count: 8 });
    expect(locksIn("2026-11-23T09:00:00Z", now)).toEqual({ unit: "hours", count: 24 });
    expect(locksIn("2026-11-29T09:00:00Z", now)).toEqual({ unit: "days", count: 7 });
  });
});
