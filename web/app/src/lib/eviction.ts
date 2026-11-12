// A viewer's session can be ended from outside: an owner revokes it, or a
// newer session on the same account evicts it (sessions.concurrent). The
// hive refuses the token at once; the page learns by asking the cloud which
// sessions still exist.
import { useQuery } from "@tanstack/react-query";
import { unwrap } from "@camhive/api";
import { api } from "./api";

export type SessionRow = { id: string; account_id: string; kind: string; client: string; issued_at: string; expires_at: string; mine: boolean };

export const EVICTION_POLL_MS = 30_000;

/** True while `sessionId` is listed on the hive; false once it disappeared (revoked or evicted); undefined before the first answer. */
export function useSessionAlive(hiveID: string | undefined, sessionId: string | undefined, intervalMs = EVICTION_POLL_MS) {
  const q = useQuery({
    queryKey: ["hive", hiveID, "sessions", sessionId],
    queryFn: async (): Promise<SessionRow[]> => {
      return unwrap(await api.GET("/v1/hives/{id}/sessions", { params: { path: { id: hiveID! } } })).sessions;
    },
    enabled: !!hiveID && !!sessionId,
    refetchInterval: intervalMs,
    retry: false,
  });
  const alive = q.data === undefined ? undefined : q.data.some((s) => s.id === sessionId);
  return { alive, query: q };
}
