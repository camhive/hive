// Cloud reachability (#56): the app keeps working on the LAN with an
// offline token when api.camhive.app cannot be reached. Every cloud call
// reports here; pages subscribe to show the banner and pick local paths.
import { useSyncExternalStore } from "react";

type Listener = () => void;
let reachable = true;
let since = 0;
const listeners = new Set<Listener>();

export function cloudReachable(): boolean {
  return reachable;
}

/** Called by the API client: a network-level failure marks the cloud down; any answer marks it up. */
export function reportCloud(ok: boolean, now = Date.now()): void {
  if (ok === reachable) return;
  reachable = ok;
  since = now;
  for (const l of listeners) l();
}

export function cloudDownSince(): number {
  return reachable ? 0 : since;
}

export function subscribeCloud(l: Listener): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useCloudReachable(): boolean {
  return useSyncExternalStore(subscribeCloud, cloudReachable, () => true);
}

/** Test hook. */
export function resetCloudForTest(): void {
  reachable = true;
  since = 0;
}
