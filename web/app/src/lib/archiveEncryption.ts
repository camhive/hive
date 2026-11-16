// Archive encryption at rest (ADR-0019, #93): the hive seals footage to the
// owner's archive key; after every start it is locked until the owner
// unlocks it with the passphrase. The app reads the state, walks the owner
// through switching it on, and unlocks, locks, changes the passphrase and
// downloads the key file (the owner's backup).
import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { CamhiveError, unwrapHive, type HiveClient, type hiveComponents } from "@camhive/api";
import type { Hive } from "./hives";
import { useHiveCapability } from "./capabilities";
import { errorMessage } from "./errors";
import { retryAfterSeconds, waitMessage } from "./retryAfter";

export type Encryption = hiveComponents["schemas"]["ArchiveEncryption"];

/** The state every member may read; undefined on a hive without encryption or before it answered. */
export function useArchiveEncryption(hive: Hive, client: HiveClient | null) {
  const cap = useHiveCapability(hive, "archive.encryption");
  return useQuery({
    queryKey: encryptionKey(hive.id),
    queryFn: async (): Promise<Encryption> => unwrapHive(await client!.GET("/v0/archive/encryption")),
    enabled: cap.has && !!client,
    staleTime: 30_000,
    refetchInterval: 60_000, // a restart (an update at night) locks it
    retry: false,
  });
}

export const encryptionKey = (hiveID: string) => ["hive", hiveID, "encryption"];

/** The key file's name, as the hive names it in Content-Disposition. */
export const keyFileName = (hiveID: string) => `camhive-archive-key-${hiveID}.age`;

/** The hive refused a passphrase because of too many wrong ones; `retryAfterS` comes from Retry-After, null when hidden. */
export class UnlockRateLimited extends CamhiveError {
  readonly retryAfterS: number | null;
  constructor(retryAfterS: number | null) {
    super(429, { code: "unlock_rate_limited" });
    this.retryAfterS = retryAfterS;
  }
}

type Result<T> = { data?: T; error?: unknown; response: Response };

function checked<T>(r: Result<T>): T {
  if (r.response.status === 429) throw new UnlockRateLimited(retryAfterSeconds(r.response.headers));
  return unwrapHive(r);
}

/** The owner's requests; enable, change and keyFile need a session through Camhive (a local one gets 403). */
export function encryptionActions(client: HiveClient) {
  return {
    enable: async (passphrase: string) => checked(await client.POST("/v0/archive/encryption", { body: { passphrase } })),
    unlock: async (passphrase: string) => checked(await client.POST("/v0/archive/encryption/unlock", { body: { passphrase } })),
    lock: async () => checked(await client.POST("/v0/archive/encryption/lock")),
    change: async (passphrase: string, next: string) => { checked(await client.POST("/v0/archive/encryption/passphrase", { body: { passphrase, new_passphrase: next } })); },
    keyFile: async (): Promise<Blob> => checked(await client.GET("/v0/archive/encryption/key-file", { parseAs: "blob" })) as Blob,
  };
}

export type EncryptionActions = ReturnType<typeof encryptionActions>;

/** Error texts for the passphrase forms: a lock says how long it lasts, a weak passphrase is not a password. */
export function encryptionError(t: TFunction, err: unknown): string {
  if (err instanceof UnlockRateLimited) return waitMessage(t, "encryption.rateLimited", err.retryAfterS);
  if (err instanceof CamhiveError && err.code === "weak_password") return t("encryption.length");
  return errorMessage(t, err);
}

const MIN_PASSPHRASE = 10;
const MAX_PASSPHRASE = 256;

/** What is wrong with a new passphrase typed twice; the hive counts characters (code points). */
export function passphraseProblem(passphrase: string, again: string): "length" | "mismatch" | null {
  const n = [...passphrase].length;
  if (n < MIN_PASSPHRASE || n > MAX_PASSPHRASE) return "length";
  return passphrase === again ? null : "mismatch";
}

/**
 * Switching encryption on (ADR-0019 §4): the warning that nobody, Camhive
 * included, can play the footage without passphrase and key file; the
 * passphrase twice; then, with encryption on, the key-file download before
 * the flow can finish.
 */
export type EnableFlow = { step: "warn" } | { step: "choose" } | { step: "backup"; saved: boolean } | { step: "done" };
export type EnableEvent = "understood" | "enabled" | "saved" | "finish";

export const START: EnableFlow = { step: "warn" };

export function enableStep(flow: EnableFlow, event: EnableEvent): EnableFlow {
  switch (`${flow.step}:${event}`) {
    case "warn:understood": return { step: "choose" };
    case "choose:enabled": return { step: "backup", saved: false };
    case "backup:saved": return { step: "backup", saved: true };
    case "backup:finish": return flow.step === "backup" && flow.saved ? { step: "done" } : flow;
    default: return flow;
  }
}
