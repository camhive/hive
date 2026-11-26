// Kind groups and the filter of a hive's own activity (#125). The API has
// only a limit, so the filter runs over the rows already loaded.

/** The groups a user picks from; cloud and live sessions are behind their own switch. */
export const KIND_GROUPS = ["cameras", "recordings", "updates", "access", "encryption"] as const;
export type KindGroup = (typeof KIND_GROUPS)[number];
export type ActionGroup = KindGroup | "cloud" | "other";

/** Kinds by name; a name ending in "." is a prefix. The first match wins. */
const RULES: [string, ActionGroup][] = [
  ["archive.encryption.", "encryption"],
  ["archive.delete", "recordings"],
  ["export.", "recordings"],
  ["camera.", "cameras"],
  ["motion.", "cameras"], // auto-calm (#157)
  ["recording.", "recordings"], // the hive stopped and resumed recording (#172)
  ["update.", "updates"],
  ["local_login.", "access"],
  ["session.revoke", "access"],
  ["diag.bundle", "access"], // rare and sent on request: who looked into the hive
  // What the cloud and live views write all day long (grants.set: every sharing change and every reconnect).
  ["signal.offer", "cloud"],
  ["live.local", "cloud"],
  ["tunnel.open", "cloud"],
  ["license.set", "cloud"],
  ["cert.set", "cloud"],
  ["ice.set", "cloud"],
  ["keys.set", "cloud"],
  ["grants.set", "cloud"],
];

export function groupOf(action: string): ActionGroup {
  const rule = RULES.find(([name]) => (name.endsWith(".") ? action.startsWith(name) : action === name));
  return rule ? rule[1] : "other";
}

/** The filter as the URL keeps it: ?kind=updates,access&q=door&cloud=true. */
export type ActivitySearch = { kind?: string; q?: string; cloud?: true };

export type ActivityFilter = { kinds: KindGroup[]; q: string; cloud: boolean };

const isGroup = (s: string): s is KindGroup => (KIND_GROUPS as readonly string[]).includes(s);

/** The search text; a hand-typed ?q=2025 arrives as a number. */
const textOf = (q: unknown): string => (typeof q === "string" ? q : typeof q === "number" ? String(q) : "");

/** Parses the URL's search once, where it comes in; anything unknown is dropped. */
export function activitySearch(raw: Record<string, unknown>): ActivitySearch {
  const kinds = typeof raw.kind === "string" ? raw.kind.split(",").filter(isGroup) : [];
  return {
    ...(kinds.length ? { kind: kinds.join(",") } : {}),
    ...(textOf(raw.q) ? { q: textOf(raw.q) } : {}),
    ...(raw.cloud === true ? { cloud: true as const } : {}),
  };
}

export function filterOf(s: ActivitySearch): ActivityFilter {
  return { kinds: (s.kind ?? "").split(",").filter(isGroup), q: s.q ?? "", cloud: s.cloud === true };
}

export function searchOf(f: ActivityFilter): ActivitySearch {
  return activitySearch({ kind: f.kinds.join(","), q: f.q, cloud: f.cloud });
}

/**
 * The rows to show and how many cloud rows match the text: cloud rows only
 * with the switch on, other rows when no group is picked or theirs is.
 */
export function filterRows<T extends { action?: string }>(rows: T[], f: ActivityFilter, text: (row: T) => string): { shown: T[]; cloud: number } {
  const q = f.q.trim().toLowerCase();
  const matching = q ? rows.filter((r) => text(r).toLowerCase().includes(q)) : rows;
  const wanted = (g: ActionGroup) => (g === "cloud" ? f.cloud : f.kinds.length === 0 || (f.kinds as string[]).includes(g));
  return {
    shown: matching.filter((r) => wanted(groupOf(r.action ?? ""))),
    cloud: matching.filter((r) => groupOf(r.action ?? "") === "cloud").length,
  };
}
