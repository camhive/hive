import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { CamhiveError } from "@camhive/api";
import { useMe } from "@/lib/session";
import { useMembers, useSharingActions } from "@/lib/sharing";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";
import { PlanLimitBox } from "@/components/PlanLimitBox";

/** People the owner shares with: active members, pending invitations, invite by e-mail (#54). */
export function Members() {
  const { t } = useTranslation();
  const me = useMe();
  const members = useMembers();
  const actions = useSharingActions();
  const [email, setEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [limit, setLimit] = useState(false);
  const [link, setLink] = useState<string | null>(null);
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;

  async function invite(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLimit(false);
    setLink(null);
    try {
      const r = await actions.invite(email);
      setLink(r.link ?? null);
      setEmail("");
    } catch (err) {
      if (err instanceof CamhiveError && err.code === "plan_limit") setLimit(true);
      else setError(errorMessage(t, err));
    }
  }
  return (
    <div className="space-y-6">
      <h1 className="page-title">{t("members.title")}</h1>
      <form className="card" onSubmit={(e) => void invite(e)} aria-label={t("members.invite")}>
        <h2 className="text-lg font-semibold mb-1">{t("members.invite")}</h2>
        <p className="muted text-sm mb-3">{t("members.inviteHelp")}</p>
        <Field label={t("auth.email")} name="invite-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        {error && <p role="alert" className="error mb-3">{error}</p>}
        {limit && <PlanLimitBox entitlement="members.max" titleKey="members.limitTitle" helpKey="members.limitHelp" />}
        {link && <p role="status" className="muted text-sm mb-3 break-all">{t("members.invited")} <code>{link}</code></p>}
        <button className="btn">{t("members.send")}</button>
      </form>

      <section className="card" aria-label={t("members.pending")}>
        <h2 className="text-lg font-semibold mb-2">{t("members.pending")}</h2>
        {members.data?.invitations.length === 0 && <p className="muted text-sm">{t("members.noPending")}</p>}
        <ul className="text-sm divide-y divide-[var(--border)]">
          {members.data?.invitations.map((i) => (
            <li key={i.id} className="flex items-center justify-between gap-3 py-2.5" data-testid="invitation">
              <span>{i.email} <span className="muted">· {t("members.expires", { at: new Date(i.expires_at).toLocaleDateString() })}</span></span>
              <button type="button" className="link" onClick={() => void actions.cancelInvitation(i.id)}>{t("members.cancel")}</button>
            </li>
          ))}
        </ul>
      </section>

      <section className="card" aria-label={t("members.members")}>
        <h2 className="text-lg font-semibold mb-2">{t("members.members")}</h2>
        {members.data?.members.length === 0 && <p className="muted text-sm">{t("members.noMembers")}</p>}
        <ul className="text-sm divide-y divide-[var(--border)]">
          {members.data?.members.map((m) => (
            <li key={m.id} className="flex items-center justify-between gap-3 py-2.5" data-testid="member">
              <span>{m.email} <span className="muted">· {t("members.since", { at: new Date(m.since).toLocaleDateString() })}</span></span>
              <button type="button" className="link link-danger" onClick={() => { if (window.confirm(t("members.confirmRemove", { email: m.email }))) void actions.removeMember(m.id); }}>{t("members.remove")}</button>
            </li>
          ))}
        </ul>
        <p className="muted text-xs mt-3">{t("members.grantsHint")}</p>
      </section>
    </div>
  );
}
