import { describe, expect, it } from "vitest";
import { FILES_TO_SAVE, filesToSave, moveCommand, moveProgress, moveTargetError, needsAttention } from "../src/lib/diskHealth";

describe("disk health and the guided move (#155)", () => {
  it("asks for attention only when the disk is failing or failed", () => {
    expect(needsAttention(undefined)).toBe(false);
    expect(needsAttention({ state: "ok", evidence: [] })).toBe(false);
    expect(needsAttention({ state: "unknown", evidence: [] })).toBe(false);
    expect(needsAttention({ state: "failing", evidence: [] })).toBe(true);
    expect(needsAttention({ state: "failed", evidence: [] })).toBe(true);
  });

  it("names the files to save first in the order the move copies them, where the hive says they are", () => {
    expect(FILES_TO_SAVE).toEqual(["hive.key", "cloud.json", "hive.yaml", "archive-key/", "hive.db"]);
    expect(filesToSave(undefined)).toEqual([...FILES_TO_SAVE]);
    expect(filesToSave([{ name: "hive.key", path: "/var/lib/camhive/hive.key" }, { name: "hive.yaml", path: "/etc/camhive/hive.yaml" }]))
      .toEqual(["/var/lib/camhive/hive.key", "/etc/camhive/hive.yaml"]);
  });

  it("writes the command for the directory", () => {
    expect(moveCommand("/mnt/newdisk/camhive")).toBe("sudo hive move-data /mnt/newdisk/camhive");
    expect(moveCommand("")).toBe("sudo hive move-data /mnt/newdisk/camhive");
  });

  it("refuses a target the hive would refuse", () => {
    expect(moveTargetError("", "/var/lib/camhive")).toBeUndefined();
    expect(moveTargetError("mnt/new", "/var/lib/camhive")).toBe("absolute");
    expect(moveTargetError("/var/lib/camhive/new", "/var/lib/camhive")).toBe("overlaps");
    expect(moveTargetError("/var/lib", "/var/lib/camhive")).toBe("overlaps");
    expect(moveTargetError("/", "/var/lib/camhive")).toBe("overlaps");
    expect(moveTargetError("/var/lib/camhive2", "/var/lib/camhive")).toBeUndefined();
    expect(moveTargetError("/mnt/new/", "/var/lib/camhive")).toBeUndefined();
    expect(moveTargetError("/mnt/new-disk_2/cam.hive", "/var/lib/camhive")).toBeUndefined();
    expect(moveTargetError("/mnt/cams/new", "/var/lib/camhive", "/mnt/cams")).toBe("overlaps");
    for (const bad of ["/mnt/new disk", "/mnt/50%", "/mnt/a\\b", '/mnt/"x"', "/mnt/$HOME", "/mnt/it's", "/mnt/tab\there",
      "/mnt/a;rm", "/mnt/a&b", "/mnt/a|b", "/mnt/(x)", "/mnt/<x>", "/mnt/*", "/mnt/a?", "/mnt/#x", "/mnt/`x`", "/mnt/a!b", "/mnt/~x", "/mnt/{a,b}", "/mnt/[a]"]) {
      expect(moveTargetError(bad, "/var/lib/camhive")).toBe("characters");
    }
  });

  it("says how far a move is", () => {
    expect(moveProgress({ state: "copying", supported: true, files: [], copied_bytes: 1, planned_bytes: 4 })).toBe(25);
    expect(moveProgress({ state: "copying", supported: true, files: [], copied_bytes: 9, planned_bytes: 0 })).toBe(0);
    expect(moveProgress({ state: "done", supported: true, files: [], copied_bytes: 9, planned_bytes: 9 })).toBe(100);
  });
});
