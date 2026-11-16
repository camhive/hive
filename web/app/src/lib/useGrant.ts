import { useMemo } from "react";
import type { AccessGrant } from "@camhive/api";
import type { Hive } from "./hives";
import { useAccess } from "./access";
import { useCloudReachable } from "./cloud";
import { readOffline, useKeepOfflineGrant } from "./offline";
import { localGrant, useLocalSession } from "./localLogin";

/**
 * The grant a hive page should use: the cloud's online grant, or, when the
 * cloud cannot be reached, the stored offline grant (owners, LAN only), or,
 * when the hive cannot be reached through the cloud and no offline grant is
 * left, a local session the owner signed in to on the LAN (#57).
 * `local` tells pages to use the hive's own signalling and skip cloud-only
 * features; `localSession` that the grant is a local sign-in; `canSignInLocally`
 * that there is no other way in right now.
 */
export function useGrant(hive: Hive | undefined) {
  const reachable = useCloudReachable();
  const viaCloud = !!hive?.online && reachable;
  const access = useAccess(hive?.id, { enabled: viaCloud });
  const owner = access.data?.role === "owner";
  useKeepOfflineGrant(hive?.id, owner, reachable);
  const offline = useMemo(() => (hive && !reachable ? readOffline(hive.id) : null), [hive, reachable]);
  const cloudGrant: AccessGrant | undefined = reachable ? access.data : (offline ?? access.data);
  const canSignInLocally = !viaCloud && !cloudGrant;
  const session = useLocalSession(hive?.id);
  const fromSession = useMemo(() => (hive && session && canSignInLocally ? localGrant(session, hive) : undefined), [hive, session, canSignInLocally]);
  const grant = fromSession ?? cloudGrant;
  return { grant, local: (!reachable && !!offline) || !!fromSession, localSession: !!fromSession, canSignInLocally, reachable, access };
}
