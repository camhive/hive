// Last-known copies of small cloud answers (#56): when the cloud cannot be
// reached, the app still knows who is signed in and which hives exist, so
// the LAN paths can be used with a stored offline token.
const PREFIX = "camhive.cache.";

export function cacheGet<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(PREFIX + key);
    return raw ? (JSON.parse(raw) as T) : null;
  } catch {
    return null;
  }
}

export function cacheSet(key: string, value: unknown): void {
  try {
    localStorage.setItem(PREFIX + key, JSON.stringify(value));
  } catch {
    /* storage blocked: the fallback is a convenience */
  }
}

export function cacheClear(): void {
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith(PREFIX)) localStorage.removeItem(k);
  } catch {
    /* ignore */
  }
}

/** True for a fetch that never reached the server (offline, DNS, blocked). */
export function isNetworkError(err: unknown): boolean {
  return err instanceof TypeError;
}
