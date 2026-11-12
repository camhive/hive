import { useState, type FormEvent } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";
import { AuthHeader } from "@/components/AuthHeader";

export function Register() {
  const { t, i18n } = useTranslation();
  const nav = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      unwrap(await api.POST("/v1/auth/register", { body: { email, password, locale: i18n.resolvedLanguage === "de" ? "de" : "en" } }));
      await nav({ to: "/confirm", search: { email } });
    } catch (err) {
      setError(errorMessage(t, err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="card sm:!p-8 mt-4" onSubmit={(e) => void submit(e)} aria-label={t("auth.register")}>
      <AuthHeader title={t("auth.register")} intro={t("auth.registerIntro")} />
      <Field label={t("auth.email")} name="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
      <Field label={t("auth.password")} name="password" type="password" autoComplete="new-password" required minLength={10} hint={t("auth.passwordHint")} value={password} onChange={(e) => setPassword(e.target.value)} />
      {error && <p role="alert" className="error mb-3">{error}</p>}
      <button className="btn w-full" disabled={busy}>{t("auth.register")}</button>
    </form>
  );
}
