import { useQuery } from "@tanstack/react-query";
import { unwrap } from "@camhive/api";
import { api } from "./api";
import { cacheGet, cacheSet, isNetworkError } from "./cache";

export type Hive = {
  id: string;
  name: string;
  status: "active" | "sleeping" | "revoked";
  online: boolean;
  version?: string;
  claimed_at: string;
  last_seen_at?: string;
  lan?: { host?: string; port?: number; addrs?: string[]; cert_fingerprint?: string; cert_issuer?: string };
  relay_host?: string;
};

export type HiveList = { hives: Hive[]; shared: Hive[] };

/**
 * Owned and shared hives with their online state. `refetchInterval` lets the
 * claim wizard poll until a freshly claimed hive connects.
 */
export function useHives(opts: { refetchInterval?: number | false } = {}) {
  return useQuery({
    queryKey: ["hives"],
    queryFn: async (): Promise<HiveList> => {
      try {
        const list = unwrap(await api.GET("/v1/hives")) as unknown as HiveList;
        cacheSet("hives", list);
        return list;
      } catch (err) {
        const cached = cacheGet<HiveList>("hives");
        if (isNetworkError(err) && cached) return cached; // cloud unreachable: last-known hives for the LAN (#56)
        throw err;
      }
    },
    refetchInterval: opts.refetchInterval ?? false,
  });
}

/** Where to download the hive agent; overridable per deployment. */
export const RELEASES_URL: string = import.meta.env.VITE_RELEASES_URL ?? "https://github.com/camhive/hive/releases/latest";

/** Copy-paste install and claim commands per platform. `apiUrl` is the cloud API the hive must talk to. */
export function installCommands(platform: "linux-deb" | "linux-rpm" | "windows", apiUrl: string): string[] {
  const claim = `hive claim --api ${apiUrl}`;
  switch (platform) {
    case "linux-deb":
      return [`sudo dpkg -i camhive-hive_*_linux_amd64.deb`, `sudo ${claim}`];
    case "linux-rpm":
      return [`sudo rpm -i camhive-hive_*_linux_amd64.rpm`, `sudo ${claim}`];
    case "windows":
      return [`hive.exe install`, `hive.exe ${claim.slice("hive ".length)}`];
  }
}

/** The command that makes a claimed hive connect (the agent prints the same after `hive claim`). */
export function restartCommand(platform: "linux-deb" | "linux-rpm" | "windows"): string {
  return platform === "windows" ? "hive.exe restart" : "sudo hive restart";
}

/** Formats a claim code as the agent prints it: upper-case groups of four, e.g. `K7QM-3XZP`. */
export function formatClaimCode(raw: string): string {
  const clean = raw.toUpperCase().replace(/[^0-9A-Z]/g, "").slice(0, 8);
  return clean.length > 4 ? `${clean.slice(0, 4)}-${clean.slice(4)}` : clean;
}
