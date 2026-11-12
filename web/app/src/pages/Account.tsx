import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import QRCode from "qrcode";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { useMe } from "@/lib/session";
import { recoveryCodesFile } from "@/lib/qr";
import { Field } from "@/components/Field";

type Session = { id: string; current: boolean; created_at: string; last_seen_at: string; user_agent: string };

export function Account() {
  const { t } = useTranslation();
  const me = useMe();
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  return (
    <div className="space-y-6">
      <div>
        <h1 className="page-title">{t("account.title")}</h1>
        <p className="muted text-sm mt-1">{me.data.email}</p>
      </div>
      <nav className="flex flex-wrap gap-2" aria-label={t("account.more")}>
        <Link to="/plan" className="btn btn-secondary btn-sm">{t("plan.title")}</Link>
        <Link to="/activity" className="btn btn-secondary btn-sm">{t("activity.title")}</Link>
        <Link to="/members" className="btn btn-secondary btn-sm">{t("members.title")}</Link>
      </nav>
      <PasswordCard />
      <TotpCard enabled={me.data.totp_enabled} email={me.data.email} />
      <SessionsCard />
    </div>
  );
}

function PasswordCard() {
  const { t } = useTranslation();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState<{ ok?: string; err?: string }>({});
  async function submit(e: FormEvent) {
    e.preventDefault();
    setMsg({});
    try {
      unwrap(await api.PUT("/v1/me/password", { body: { current, password: next } }));
      setCurrent("");
      setNext("");
      setMsg({ ok: t("account.passwordChanged") });
    } catch (err) {
      setMsg({ err: errorMessage(t, err) });
    }
  }
  return (
    <form className="card" onSubmit={(e) => void submit(e)} aria-label={t("account.password")}>
      <h2 className="text-lg font-semibold mb-3">{t("account.password")}</h2>
      <Field label={t("account.currentPassword")} name="current" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      <Field label={t("auth.newPassword")} name="next" type="password" autoComplete="new-password" required minLength={10} hint={t("auth.passwordHint")} value={next} onChange={(e) => setNext(e.target.value)} />
      {msg.err && <p role="alert" className="error mb-3">{msg.err}</p>}
      {msg.ok && <p role="status" className="muted mb-3">{msg.ok}</p>}
      <button className="btn">{t("account.changePassword")}</button>
    </form>
  );
}

function TotpCard({ enabled, email }: { enabled: boolean; email: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [enrol, setEnrol] = useState<{ secret: string; uri: string } | null>(null);
  const [qr, setQr] = useState<string>("");
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [disable, setDisable] = useState<{ password: string; code: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!enrol) return;
    QRCode.toDataURL(enrol.uri, { margin: 1, width: 192 }).then(setQr).catch(() => setQr(""));
  }, [enrol]);

  async function start() {
    setError(null);
    try {
      const e = unwrap(await api.POST("/v1/me/totp/enrol"));
      setEnrol({ secret: e.secret ?? "", uri: e.uri ?? "" });
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  async function confirm(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = unwrap(await api.POST("/v1/me/totp/confirm", { body: { code } }));
      setRecovery(res.recovery_codes ?? []);
      setEnrol(null);
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  async function doDisable(e: FormEvent) {
    e.preventDefault();
    if (!disable) return;
    setError(null);
    try {
      unwrap(await api.DELETE("/v1/me/totp", { body: { password: disable.password, code: disable.code } }));
      setDisable(null);
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  function download() {
    if (!recovery) return;
    const blob = new Blob([recoveryCodesFile(recovery, email)], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "camhive-recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(a.href);
  }

  return (
    <section className="card" aria-label={t("account.totp")}>
      <h2 className="text-lg font-semibold mb-3">{t("account.totp")}</h2>
      {recovery && (
        <div className="mb-4" data-testid="recovery">
          <p className="mb-2">{t("account.recoveryIntro")}</p>
          <ul className="font-mono text-sm grid grid-cols-2 gap-1 mb-3">{recovery.map((c) => <li key={c}>{c}</li>)}</ul>
          <button type="button" className="btn" onClick={download}>{t("account.downloadCodes")}</button>
        </div>
      )}
      {!enabled && !enrol && (
        <>
          <p className="muted mb-3">{t("account.totpOff")}</p>
          <button type="button" className="btn" onClick={() => void start()}>{t("account.enableTotp")}</button>
        </>
      )}
      {enrol && (
        <form onSubmit={(e) => void confirm(e)} aria-label={t("account.confirmTotp")}>
          <p className="mb-2">{t("account.scan")}</p>
          {qr && <img src={qr} alt={t("account.qrAlt")} width={192} height={192} className="mb-2" />}
          <p className="font-mono text-sm mb-3" data-testid="secret">{enrol.secret}</p>
          <Field label={t("auth.totp")} name="code" inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} />
          <button className="btn">{t("account.confirmTotp")}</button>
        </form>
      )}
      {enabled && !recovery && (
        <>
          <p className="muted mb-3">{t("account.totpOn")}</p>
          {disable ? (
            <form onSubmit={(e) => void doDisable(e)} aria-label={t("account.disableTotp")}>
              <Field label={t("auth.password")} name="dpassword" type="password" required value={disable.password} onChange={(e) => setDisable({ ...disable, password: e.target.value })} />
              <Field label={t("auth.totp")} name="dcode" required hint={t("auth.totpHint")} value={disable.code} onChange={(e) => setDisable({ ...disable, code: e.target.value })} />
              <button className="btn btn-danger">{t("account.disableTotp")}</button>
            </form>
          ) : (
            <button type="button" className="btn btn-secondary" onClick={() => setDisable({ password: "", code: "" })}>{t("account.disableTotp")}</button>
          )}
        </>
      )}
      {error && <p role="alert" className="error mt-3">{error}</p>}
    </section>
  );
}

function SessionsCard() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: async () => (unwrap(await api.GET("/v1/me/sessions")) as { sessions: Session[] }).sessions,
  });
  async function revoke(id: string) {
    await api.DELETE("/v1/me/sessions/{id}", { params: { path: { id } } });
    await qc.invalidateQueries({ queryKey: ["sessions"] });
  }
  return (
    <section className="card" aria-label={t("account.sessions")}>
      <h2 className="text-lg font-semibold mb-3">{t("account.sessions")}</h2>
      <ul>
        {(sessions.data ?? []).map((s) => (
          <li key={s.id} className="flex items-center justify-between gap-3 text-sm py-2.5 border-t first:border-t-0" style={{ borderColor: "var(--border)" }}>
            <span>
              {s.user_agent || "—"} {s.current && <em className="muted">({t("account.thisDevice")})</em>}
              <br />
              <span className="muted">{new Date(s.last_seen_at).toLocaleString()}</span>
            </span>
            {!s.current && <button type="button" className="btn btn-secondary btn-sm" onClick={() => void revoke(s.id)}>{t("account.revoke")}</button>}
          </li>
        ))}
      </ul>
    </section>
  );
}
