import { useState } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useMe } from "@/lib/session";
import { useSharingActions } from "@/lib/sharing";
import { errorMessage } from "@/lib/errors";

/** Landing page of the invitation link: accept with the signed-in account, or sign in first (#54). */
export function Invite() {
  const { t } = useTranslation();
  const { token } = useSearch({ from: "/invite" });
  const me = useMe();
  const nav = useNavigate();
  const actions = useSharingActions();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (!token) return <p className="card">{t("errors.bad_invitation")}</p>;
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) {
    return (
      <div className="card">
        <h1 className="text-xl font-semibold mb-2">{t("invite.title")}</h1>
        <p className="muted mb-4">{t("invite.signInFirst")}</p>
        <div className="flex gap-3">
          <Link to="/login" search={{ next: `/invite?token=${encodeURIComponent(token)}` }} className="btn">{t("nav.signIn")}</Link>
          <Link to="/register" search={{ next: `/invite?token=${encodeURIComponent(token)}` }} className="link self-center">{t("nav.signUp")}</Link>
        </div>
      </div>
    );
  }
  async function accept() {
    setBusy(true);
    setError(null);
    try {
      await actions.accept(token!);
      await nav({ to: "/" });
    } catch (err) {
      setError(errorMessage(t, err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="card">
      <h1 className="text-xl font-semibold mb-2">{t("invite.title")}</h1>
      <p className="muted mb-4">{t("invite.acceptAs", { email: me.data.email })}</p>
      {error && <p role="alert" className="error mb-3">{error}</p>}
      <button className="btn" disabled={busy} onClick={() => void accept()}>{t("invite.accept")}</button>
    </div>
  );
}
