import { useMemo } from "react";
import type { AccessGrant } from "@camhive/api";
import type { Hive } from "./hives";
import { useAccess } from "./access";
import { useCloudReachable } from "./cloud";
import { readOffline, useKeepOfflineGrant } from "./offline";

/**
 * The grant a hive page should use: the cloud's online grant, or, when the
 * cloud cannot be reached, the stored offline grant (owners, LAN only).
 * `local` tells pages to use the hive's own signalling and skip cloud-only
 * features.
 */
export function useGrant(hive: Hive | undefined) {
  const reachable = useCloudReachable();
  const access = useAccess(hive?.id, { enabled: !!hive?.online && reachable });
  const owner = access.data?.role === "owner";
  useKeepOfflineGrant(hive?.id, owner, reachable);
  const offline = useMemo(() => (hive && !reachable ? readOffline(hive.id) : null), [hive, reachable]);
  const grant: AccessGrant | undefined = reachable ? access.data : (offline ?? access.data);
  return { grant, local: !reachable && !!offline, reachable, access };
}
