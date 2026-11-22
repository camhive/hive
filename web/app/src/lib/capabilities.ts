// Hive capabilities (ADR-0022): the app asks the hive what it can do, not
// its version. The hive reports its list in the heartbeat (the cloud's hive
// list) and in GET /v0/hive.
/**
 * The release in which each capability first shipped. It tells the user
 * which version to update to, and gives a hive that reports no list (v0.1.6
 * or older) what shipped in its version: the only version comparison in
 * the app.
 */
export const SINCE = {
  "archive.autolock": "0.1.10",
  "archive.delete": "0.1.3",
  "archive.encryption": "0.1.7",
  "camera.care": "0.1.10",
  "camera.identity": "0.1.4",
  "camera.retention": "0.1.9",
  "camera.sensitivity": "0.1.10",
  "camera.sub_stream": "0.1.8",
  "disk.health": "0.1.10",
  "local_login": "0.1.6",
  "motion": "0.1.6",
  "storage": "0.1.10",
  "updates": "0.1.7",
} as const;

export type Capability = keyof typeof SINCE;

/** The last release without a capability list (the list ships in v0.1.7). */
const BASELINE = "0.1.6";

/** What the app needs to know about a hive: the cloud's Hive or GET /v0/hive. */
export type HiveInfo = { version?: string; capabilities?: string[] };

export type CapabilityAnswer = { has: true } | { has: false; since: string };

/** Whether the hive has the capability and, if not, the version that brings it. */
export function hiveCapability(hive: HiveInfo, capability: Capability): CapabilityAnswer {
  const since = SINCE[capability];
  const has = hive.capabilities ? hive.capabilities.includes(capability) : older(since, reportedOrBaseline(hive.version));
  return has ? { has } : { has, since };
}

/**
 * The answer for components: the one place they read a capability from.
 * Pass the cloud's Hive (its list comes from the heartbeat, so no LAN or
 * relay connection is needed).
 */
export function useHiveCapability(hive: HiveInfo, capability: Capability): CapabilityAnswer {
  return hiveCapability(hive, capability);
}

/** A list-less hive's version, capped at the baseline; unknown versions are the baseline. */
function reportedOrBaseline(version: string | undefined): string {
  return version && parse(version) && older(version, BASELINE) ? version : BASELINE;
}

/** a <= b for dotted release versions ("0.1.5", "v0.1.5"). */
function older(a: string, b: string): boolean {
  const x = parse(a)!, y = parse(b);
  if (!y) return false;
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i]! < y[i]!;
  return true;
}

function parse(v: string | undefined): number[] | null {
  const m = /^v?(\d+)\.(\d+)\.(\d+)/.exec(v ?? "");
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null;
}
