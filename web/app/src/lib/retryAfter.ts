// Rate limits on the hive (local sign-in, archive unlock) say in Retry-After
// how long they last. The app is always cross-origin to the hive and the
// header is not CORS-safelisted: hives up to v0.1.7 do not expose it, so the
// browser hides it and the app must not pretend to know.
import type { TFunction } from "i18next";

/** Seconds from Retry-After, or null when the browser cannot see it or it is not a number of seconds. */
export function retryAfterSeconds(headers: Headers): number | null {
  const v = headers.get("Retry-After")?.trim();
  return v && /^\d+$/.test(v) ? Number(v) : null;
}

/** `key` with the minutes to wait when the hive said, else `${key}Unknown` (the limit's longest wait). */
export function waitMessage(t: TFunction, key: string, retryAfterS: number | null): string {
  return retryAfterS === null ? t(`${key}Unknown`) : t(key, { count: Math.max(1, Math.ceil(retryAfterS / 60)) });
}
