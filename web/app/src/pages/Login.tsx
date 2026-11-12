import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";
import { AuthHeader } from "@/components/AuthHeader";

export function Login() {
  const { t } = useTranslation();
  const nav = useNavigate();
  const qc = useQueryClient();
  const search = useSearch({ from: "/login" });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [mfa, setMfa] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function done() {
    await qc.invalidateQueries({ queryKey: ["me"] });
    await nav({ to: search.next ?? "/" });
  }
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      if (mfa) {
        unwrap(await api.POST("/v1/auth/mfa", { body: { code } }));
        await done();
        return;
      }
      const res = unwrap(await api.POST("/v1/auth/login", { body: { email, password } }));
      if (res.status === "mfa_required") {
        setMfa(true);
        return;
      }
      await done();
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  return (
    <form className="card sm:!p-8 mt-4" onSubmit={(e) => void submit(e)} aria-label={t("auth.login")}>
      <AuthHeader title={mfa ? t("auth.totp") : t("auth.welcomeBack")} intro={search.confirmed ? <span className="badge badge-success">{t("auth.confirmed")}</span> : t("auth.loginIntro")} />
      {mfa ? (
        <Field label={t("auth.totp")} name="code" inputMode="numeric" autoComplete="one-time-code" required hint={t("auth.totpHint")} value={code} onChange={(e) => setCode(e.target.value)} />
      ) : (
        <>
          <Field label={t("auth.email")} name="email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
          <Field label={t("auth.password")} name="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </>
      )}
      {error && <p role="alert" className="error mb-3">{error}</p>}
      <button className="btn w-full">{mfa ? t("auth.verify") : t("auth.login")}</button>
      {!mfa && (
        <div className="mt-5 flex flex-wrap items-center justify-between gap-2 text-sm">
          <Link to="/forgot" className="link">{t("auth.forgot")}</Link>
          <span className="muted">{t("auth.noAccount")} <Link to="/register" className="link">{t("nav.signUp")}</Link></span>
        </div>
      )}
    </form>
  );
}
