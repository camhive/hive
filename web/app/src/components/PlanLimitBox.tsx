import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api";

/** `plan_limit` from the API: the cap behind it and where the upgrade path lives. */
export function PlanLimitBox({ entitlement, titleKey, helpKey }: { entitlement: string; titleKey: string; helpKey: string }) {
  const { t } = useTranslation();
  const [max, setMax] = useState<number | null>(null);
  useEffect(() => {
    let cancelled = false;
    api.GET("/v1/me/plan").then((r) => {
      const v = (r.data?.entitlements as Record<string, unknown> | undefined)?.[entitlement];
      if (!cancelled && typeof v === "number") setMax(v);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, [entitlement]);
  return (
    <div role="alert" className="callout mb-3 text-sm" data-testid="plan-limit">
      <p className="font-medium">{max === null ? t(titleKey) : t(`${titleKey}N`, { count: max })}</p>
      <p className="muted mt-1">{t(helpKey)}</p>
      <p className="mt-2"><Link to="/plan" className="link">{t("hives.limitAction")}</Link></p>
    </div>
  );
}
