/** Exponential backoff for live tiles: 1 s, 2 s, 4 s … capped at 30 s. */
export function backoffMs(attempt: number, base = 1000, cap = 30_000): number {
  return Math.min(cap, base * 2 ** Math.max(0, attempt));
}

/** How large a live picture is drawn: the opened camera, or a tile in one of the grid sizes. */
export type TileSize = "detail" | "large" | "medium" | "small";

/** Which stream a picture should ask for: the plan caps remote quality, and medium or small tiles always use the sub stream. */
export function streamFor(quality: "sub" | "full" | undefined, size: TileSize): "main" | "sub" {
  if (quality === "sub") return "sub";
  return size === "detail" || size === "large" ? "main" : "sub";
}
