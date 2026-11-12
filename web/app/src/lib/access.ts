// Access tokens for a hive (ADR-0004): issued by the cloud for the signed-in
// account, sent to the hive as Bearer. Online tokens live 15 minutes; the
// query refreshes a minute before expiry while a page uses the hive.
import { useQuery } from "@tanstack/react-query";
import { unwrap, type AccessGrant } from "@camhive/api";
import { api } from "./api";

export const REFRESH_MARGIN_MS = 60_000;
export const MIN_REFRESH_MS = 5_000;
/** Timers cannot exceed a signed 32-bit millisecond count; offline tokens (7 days) stay below this anyway. */
export const MAX_REFRESH_MS = 24 * 3_600_000;

/** Milliseconds until the token should be renewed; never sooner than MIN_REFRESH_MS. */
export function msUntilRefresh(expiresAt: string, now = Date.now()): number {
  return Math.min(MAX_REFRESH_MS, Math.max(MIN_REFRESH_MS, Date.parse(expiresAt) - now - REFRESH_MARGIN_MS));
}

export async function issueAccess(hiveID: string, opts: { offline?: boolean; client?: string } = {}): Promise<AccessGrant> {
  const client = opts.client ?? (typeof navigator !== "undefined" ? navigator.userAgent.slice(0, 120) : "web");
  return unwrap(await api.POST("/v1/hives/{id}/access", { params: { path: { id: hiveID } }, body: { client, offline: opts.offline ?? false } }));
}

/** Current grant for the hive, renewed before it expires; `enabled: false` while no page needs it. Falls back to a stored offline grant when the cloud is unreachable (see offline.ts). */
export function useAccess(hiveID: string | undefined, opts: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ["access", hiveID],
    queryFn: () => issueAccess(hiveID!),
    enabled: !!hiveID && (opts.enabled ?? true),
    staleTime: Infinity,
    refetchInterval: (q) => (q.state.data ? msUntilRefresh(q.state.data.expires_at) : false),
    refetchOnWindowFocus: false,
    retry: 1,
  });
}
