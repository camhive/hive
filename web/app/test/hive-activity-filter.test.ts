import { describe, expect, it } from "vitest";
import { activitySearch, filterOf, filterRows, groupOf } from "../src/lib/hiveActivity";
import en from "../src/i18n/en.json";

describe("hive activity kinds (#125)", () => {
  it("puts every kind the hive writes in its group; unknown kinds are other", () => {
    const groups: Record<string, string[]> = {
      cameras: ["camera.add", "camera.update", "camera.move", "camera.remove", "camera.address_changed", "camera.identity_changed"],
      recordings: ["archive.delete", "export.done", "export.failed", "export.running", "recording.stopped", "recording.resumed"],
      updates: ["update.available", "update.requested", "update.applied", "update.rolled_back", "update.needs_package", "update.settings_changed"],
      access: ["local_login.enable", "local_login.disable", "local_login.success", "local_login.failure", "local_login.locked", "session.revoke", "diag.bundle"],
      encryption: ["archive.encryption.enable", "archive.encryption.enable_failed", "archive.encryption.unlock", "archive.encryption.unlock_failed",
        "archive.encryption.lock", "archive.encryption.passphrase_change", "archive.encryption.passphrase_change_failed", "archive.encryption.key_file"],
      cloud: ["signal.offer", "live.local", "tunnel.open", "license.set", "cert.set", "ice.set", "keys.set", "grants.set"],
      other: ["something.new", "", "camera", "archive.deleted"],
    };
    for (const [group, kinds] of Object.entries(groups)) for (const k of kinds) expect(groupOf(k), k).toBe(group);
  });

  it("has a group for every kind the app has a label for", () => {
    const labelled = Object.keys(en.hiveActivity.kinds);
    expect(labelled).toContain("update.integrity_failed");
    expect(labelled.filter((k) => groupOf(k) === "other")).toEqual([]);
  });
});

describe("hive activity filter", () => {
  const rows = [
    { action: "update.applied", text: "Update installed Updater" },
    { action: "signal.offer", text: "Live view started Garden Camhive cloud" },
    { action: "tunnel.open", text: "Remote connection opened Camhive cloud" },
    { action: "local_login.failure", text: "Local sign-in failed Local sign-in" },
    { action: "camera.add", text: "Camera added Garden you" },
    { action: "something.new", text: "something.new Hive" },
  ];
  const run = (search: Record<string, unknown>) => {
    const r = filterRows(rows, filterOf(activitySearch(search)), (x) => x.text);
    return { shown: r.shown.map((x) => x.action), cloud: r.cloud };
  };

  it("hides the cloud's sessions by default and counts them", () => {
    expect(run({})).toEqual({ shown: ["update.applied", "local_login.failure", "camera.add", "something.new"], cloud: 2 });
    expect(run({ cloud: true }).shown).toEqual(rows.map((r) => r.action));
  });

  it("shows only the chosen groups", () => {
    expect(run({ kind: "updates,access" }).shown).toEqual(["update.applied", "local_login.failure"]);
    expect(run({ kind: "updates,access", cloud: true }).shown).toEqual(["update.applied", "signal.offer", "tunnel.open", "local_login.failure"]);
  });

  it("matches the free text against label, target and actor, ignoring case, and counts only matching cloud rows", () => {
    expect(run({ q: "garden" })).toEqual({ shown: ["camera.add"], cloud: 1 });
    expect(run({ q: "  UPDATER " }).shown).toEqual(["update.applied"]);
    expect(run({ q: "garden", kind: "updates" })).toEqual({ shown: [], cloud: 1 });
  });

  it("reads the URL once: unknown groups and junk are dropped", () => {
    expect(activitySearch({ kind: "updates,nonsense,,access", q: "door", cloud: true })).toEqual({ kind: "updates,access", q: "door", cloud: true });
    expect(activitySearch({ kind: 3, q: ["x"], cloud: "yes" })).toEqual({});
    expect(activitySearch({ kind: "nonsense", q: "" })).toEqual({});
    expect(activitySearch({ q: 2025 })).toEqual({ q: "2025" }); // ?q=2025 arrives as a number
    expect(filterOf({})).toEqual({ kinds: [], q: "", cloud: false });
  });
});
