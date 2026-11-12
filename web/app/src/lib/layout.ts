/** How the hive page lays out the cameras that are not opened: tiles in three sizes, or a list without video. */
export type Layout = "large" | "medium" | "small" | "list";

export const LAYOUTS: readonly Layout[] = ["large", "medium", "small", "list"];

const KEY = "camhive.live.layout";

export function readLayout(): Layout {
  try {
    const v = localStorage.getItem(KEY);
    if ((LAYOUTS as readonly string[]).includes(v ?? "")) return v as Layout;
  } catch {
    /* storage may be unavailable */
  }
  return "large";
}

export function writeLayout(l: Layout): void {
  try {
    localStorage.setItem(KEY, l);
  } catch {
    /* ignore */
  }
}

/** Grid columns per tile size, phone first. */
export const COLUMNS: Record<Exclude<Layout, "list">, string> = {
  large: "grid-cols-1 md:grid-cols-2",
  medium: "grid-cols-2 lg:grid-cols-3",
  small: "grid-cols-3 md:grid-cols-4 lg:grid-cols-6",
};
