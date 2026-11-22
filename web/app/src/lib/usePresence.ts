// The owner's app tells a hive with automatic locking whether it is at home
// (#147, ADR-0019 addendum): when a page holding a connection opens, when
// the transport decision changes, at once after an unlock or a restart of
// the hive (it forgets what it was told), and once a minute while open.
import { useEffect, useRef, useSyncExternalStore } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { HiveClient } from "@camhive/api";
import type { Hive } from "./hives";
import { useHiveCapability } from "./capabilities";
import { autolockActions, presenceReporter, PRESENCE_EVERY_MS } from "./archiveAutolock";
import { encryptionKey, type Encryption } from "./archiveEncryption";

export function useReportPresence(hive: Hive | undefined, client: HiveClient | null, atHome: boolean | null, owner: boolean): void {
  const cap = useHiveCapability(hive ?? {}, "archive.autolock");
  const id = hive?.id;
  const state = useCachedState(id);
  const on = !!id && !!client && atHome !== null && owner && cap.has;
  const latest = useRef({ id, client, atHome });
  latest.current = { id, client, atHome };
  const report = (now?: boolean) => {
    const { id, client, atHome } = latest.current;
    if (id && client && atHome !== null) presenceReporter().report(id, atHome, autolockActions(client).presence, { now });
  };

  useEffect(() => {
    if (on) report();
  }, [on, id, client, atHome]); // eslint-disable-line react-hooks/exhaustive-deps

  // An unlock (locked, then unlocked), or another version (an update
  // restarted the hive): at once. The first answer is neither.
  const version = hive?.version;
  const seen = useRef({ state, version });
  useEffect(() => {
    const before = seen.current;
    seen.current = { state, version };
    const unlocked = before.state === "locked" && state === "unlocked";
    const restarted = !!before.version && !!version && before.version !== version;
    if (on && (unlocked || restarted)) report(true);
  }, [state, version]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!on) return;
    const heartbeat = setInterval(() => report(), PRESENCE_EVERY_MS);
    return () => clearInterval(heartbeat);
  }, [on, id]); // eslint-disable-line react-hooks/exhaustive-deps
}

/**
 * The archive's state as the pages that show the lock last fetched it. Read
 * from the cache only: a second observer of that query would replace its
 * fetch options.
 */
function useCachedState(hiveID: string | undefined): Encryption["state"] | undefined {
  const qc = useQueryClient();
  return useSyncExternalStore(
    (changed) => qc.getQueryCache().subscribe(changed),
    () => (hiveID ? qc.getQueryData<Encryption>(encryptionKey(hiveID))?.state : undefined),
  );
}
