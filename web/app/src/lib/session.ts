import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, } from "./api";
import { unwrap, CamhiveError } from "@camhive/api";
import { cacheClear, cacheGet, cacheSet, isNetworkError } from "./cache";

export type Me = { id: string; email: string; locale: string; totp_enabled: boolean; created_at: string };

/** The signed-in account, or null when there is no session. */
export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async (): Promise<Me | null> => {
      const res = await api.GET("/v1/me").catch((err: unknown) => {
        // Cloud unreachable: stay signed in with the last-known account for LAN use (#56).
        const cached = cacheGet<Me>("me");
        if (isNetworkError(err) && cached) return cached;
        throw err;
      });
      if (!("response" in res)) return res;
      if (res.response.status === 401) {
        cacheClear();
        return null;
      }
      const me = unwrap(res) as unknown as Me;
      cacheSet("me", me);
      return me;
    },
    staleTime: 60_000,
    retry: (count, err) => !(err instanceof CamhiveError) && count < 2,
  });
}

export function useSignOut() {
  const qc = useQueryClient();
  return async () => {
    await api.POST("/v1/auth/logout");
    cacheClear();
    await qc.invalidateQueries({ queryKey: ["me"] });
  };
}
