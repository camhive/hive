import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { useMe } from "@/lib/session";
import { freeMonths, useMyPlan, usePlans, price, SHOWN_ENTITLEMENTS } from "@/lib/plan";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";

/** The account's plan, the price list with entitlements, promo codes, and where upgrading will live (#55). */
export function Plan() {
  const { t } = useTranslation();
  const me = useMe();
  const mine = useMyPlan();
  const tiers = usePlans();
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  const currentId = mine.data?.plan.id;
  return (
    <div className="space-y-6 max-w-none">
      <h1 className="page-title">{t("plan.title")}</h1>
      {mine.data && (
        <section className="card" aria-label={t("plan.current")}>
          <p className="text-lg"><span className="muted">{t("plan.current")}:</span> <strong data-testid="current-plan">{mine.data.plan.name}</strong></p>
          {mine.data.addons.length > 0 && <p className="text-sm muted mt-1">{t("plan.addons")}: {mine.data.addons.map((a) => `${a.name ?? a.id} × ${a.quantity ?? 1}`).join(", ")}</p>}
          {mine.data.promos.length > 0 && <p className="text-sm muted mt-1">{t("plan.promos")}: {mine.data.promos.join(", ")}</p>}
          <p className="text-sm muted mt-3">{t("plan.upgradeSoon")}</p>
        </section>
      )}
      {tiers.data && (
        <section aria-label={t("plan.compare")}>
          <ul className="grid grid-cols-1 gap-4 md:grid-cols-3" data-testid="tiers">
            {tiers.data.map((p) => (
              <li key={p.id} className="card" aria-current={p.id === currentId ? "true" : undefined}>
                <h2 className="text-lg font-semibold">{p.name}{p.id === currentId && <span className="ml-1 text-xs font-normal muted">({t("plan.yours")})</span>}</h2>
                <PlanPrice monthCents={p.price_month_cents} yearCents={p.price_year_cents} currency={p.currency} testId={`price-${p.id}`} />
                <dl className="kv text-sm mt-4">
                  {SHOWN_ENTITLEMENTS.map((key) => (
                    <div key={key} className="py-2 border-t" style={{ borderColor: "var(--border)" }}>
                      <dt className="muted">{t(`plan.keys.${key}`)}</dt>
                      <dd data-key={key} data-plan={p.id}>{formatValue(key, p.entitlements[key], t)}</dd>
                    </div>
                  ))}
                </dl>
              </li>
            ))}
          </ul>
          <p className="text-xs muted mt-3">{t("plan.alwaysFree")}</p>
        </section>
      )}
      <PromoCard />
    </div>
  );
}

/** Renders an entitlement value: -1 is unlimited, booleans yes/no, enums translated when known. */
export function formatValue(key: string, v: unknown, t: (k: string, o?: Record<string, unknown>) => string): string {
  if (v === undefined || v === null) return "—";
  if (typeof v === "boolean") return v ? t("plan.yes") : t("plan.no");
  if (typeof v === "number") {
    if (v < 0) return t("plan.unlimited");
    if (key === "members.max") return String(v + 1); // the owner counts as a user of the account
    if (key === "shares.max" && v === 0) return t("plan.no");
    return String(v);
  }
  const s = String(v);
  const known = t(`plan.values.${key}.${s}`, { defaultValue: "" });
  return known || s;
}

/** The monthly price in front; a yearly price only as the cheaper alternative, with what it saves. */
function PlanPrice({ monthCents, yearCents, currency, testId }: { monthCents: number; yearCents: number; currency: string; testId: string }) {
  const { t } = useTranslation();
  if (!monthCents) return <div className="text-sm font-normal" data-testid={testId}>{t("plan.free")}</div>;
  const saved = freeMonths(monthCents, yearCents);
  return (
    <div className="font-normal" data-testid={testId}>
      <div className="text-sm">{t("plan.perMonth", { price: price(monthCents, currency) })}</div>
      {yearCents > 0 && (
        <div className="text-xs muted">
          {saved > 0 ? t("plan.perYearSaving", { price: price(yearCents, currency), count: saved }) : t("plan.perYear", { price: price(yearCents, currency) })}
        </div>
      )}
    </div>
  );
}

function PromoCard() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [code, setCode] = useState("");
  const [msg, setMsg] = useState<{ ok?: string; err?: string }>({});
  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg({});
    try {
      unwrap(await api.POST("/v1/me/promo", { body: { code: code.trim() } }));
      await qc.invalidateQueries({ queryKey: ["me", "plan"] });
      setCode("");
      setMsg({ ok: t("plan.promoRedeemed") });
    } catch (err) {
      setMsg({ err: errorMessage(t, err) });
    }
  }
  return (
    <form className="card" onSubmit={(e) => void submit(e)} aria-label={t("plan.promo")}>
      <h2 className="text-lg font-semibold mb-1">{t("plan.promo")}</h2>
      <p className="muted text-sm mb-3">{t("plan.promoHelp")}</p>
      <Field label={t("plan.promoCode")} name="promo" required autoComplete="off" value={code} onChange={(e) => setCode(e.target.value)} />
      {msg.err && <p role="alert" className="error mb-3">{msg.err}</p>}
      {msg.ok && <p role="status" className="muted mb-3">{msg.ok}</p>}
      <button className="btn">{t("plan.redeem")}</button>
    </form>
  );
}
