// Plans and entitlements for the account pages (#55).
import { useQuery } from "@tanstack/react-query";
import { unwrap, type components } from "@camhive/api";
import { api } from "./api";

export type PlanTier = components["schemas"]["PlanTier"];
export type Entitlements = components["schemas"]["Entitlements"];

export function useMyPlan() {
  return useQuery({ queryKey: ["me", "plan"], queryFn: async () => unwrap(await api.GET("/v1/me/plan")) });
}

export function usePlans() {
  return useQuery({ queryKey: ["plans"], queryFn: async () => unwrap(await api.GET("/v1/plans")).plans, staleTime: 300_000 });
}

/** Entitlement keys in the order the price table shows them (PLAN.md §11.1). */
export const SHOWN_ENTITLEMENTS = [
  "hives.max", "members.max", "sessions.concurrent", "remote.quality", "shares.max",
  "export.remote_per_day", "export.max_minutes", "export.api", "notify.channels", "notify.devices",
  "analytics.level", "ptz.enabled", "updates.early_access",
] as const;

/** Price in whole currency units, e.g. "5 €" or "0". */
export function price(cents: number, currency: string): string {
  if (!cents) return "0";
  const sym = currency === "EUR" ? "€" : currency === "USD" ? "$" : ` ${currency}`;
  const whole = cents / 100;
  return `${Number.isInteger(whole) ? whole : whole.toFixed(2)}${sym}`;
}
