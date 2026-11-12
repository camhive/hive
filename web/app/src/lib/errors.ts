import { CamhiveError } from "@camhive/api";
import type { TFunction } from "i18next";

/** Stable API codes → i18n keys under `errors.`; unknown codes fall back. */
const known = new Set([
  // cloud API
  "bad_json", "invalid", "weak_password", "invalid_credentials", "locked", "email_unverified", "account_disabled",
  "bad_code", "unauthenticated", "mfa_required", "totp_state", "csrf", "not_found", "rate_limited",
  "already_claimed", "unknown_code", "hive_offline", "hive_state", "owner_only", "member_state", "plan_limit",
  "bad_invitation", "wrong_account", "promo_invalid", "promo_redeemed", "hive_unreachable", "hive_not_ready", "internal",
  // hive local API
  "token_missing", "token_invalid", "token_expired", "clock_skew", "wrong_hive", "license_required", "forbidden",
  "not_ready", "engine", "unsupported", "camera", "camera_auth", "no_audio", "audio_unsupported", "export_running", "camera_mismatch",
]);

export function errorKey(err: unknown): string {
  if (err instanceof CamhiveError && known.has(err.code)) return `errors.${err.code}`;
  if (err instanceof TypeError) return "errors.network";
  return "errors.unknown";
}

/**
 * Human text for an error. An API code we have no translation for still
 * shows the code itself, so a screenshot from a user tells us what happened.
 */
export function errorMessage(t: TFunction, err: unknown): string {
  const key = errorKey(err);
  if (key === "errors.unknown" && err instanceof CamhiveError && err.code) return t("errors.unexpected_code", { code: err.code });
  return t(key);
}
