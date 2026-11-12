// Hive API access from pages: a typed client whose requests go through the
// transport module (LAN or relay, token attached) and the queries live
// pages need.
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { createHiveClient, unwrapHive, type AccessGrant, type CameraHealth, type HiveCamera, type HiveClient } from "@camhive/api";
import type { Hive } from "./hives";
import { transport, type Transport } from "./transport";

/** Placeholder origin: the transport rewrites every request to the decided base. */
const VIA_TRANSPORT = "https://hive.invalid";

export function hiveClient(hive: Hive, token: () => string, t: Transport = transport): HiveClient {
  return createHiveClient({
    baseUrl: VIA_TRANSPORT,
    token,
    fetch: (input, init) => {
      const req = new Request(input, init);
      const u = new URL(req.url);
      const tok = req.headers.get("Authorization")?.replace(/^Bearer /, "") ?? token();
      return t.fetch(hive, u.pathname + u.search, { method: req.method, headers: req.headers, body: req.body, signal: req.signal, token: tok, ...(req.body ? { duplex: "half" } as RequestInit : {}) });
    },
  });
}

export function useHiveClient(hive: Hive | undefined, grant: AccessGrant | undefined): HiveClient | null {
  const token = grant?.token;
  return useMemo(() => (hive && token ? hiveClient(hive, () => token) : null), [hive?.id, token]); // eslint-disable-line react-hooks/exhaustive-deps
}

export function useCameras(hive: Hive | undefined, client: HiveClient | null) {
  return useQuery({
    queryKey: ["hive", hive?.id, "cameras"],
    queryFn: async (): Promise<HiveCamera[]> => unwrapHive(await client!.GET("/v0/cameras")),
    enabled: !!hive && !!client,
    staleTime: 30_000,
  });
}

/** Per-camera online state and bitrate, polled while a live page is open. */
export function useCameraHealth(hive: Hive | undefined, client: HiveClient | null, intervalMs = 10_000) {
  return useQuery({
    queryKey: ["hive", hive?.id, "health"],
    queryFn: async (): Promise<CameraHealth[]> => unwrapHive(await client!.GET("/v0/health/cameras")),
    enabled: !!hive && !!client,
    refetchInterval: intervalMs,
    retry: false,
  });
}
