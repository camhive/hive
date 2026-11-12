import { useState, type FormEvent } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";
import { AuthHeader } from "@/components/AuthHeader";

export function Reset() {
  const { t } = useTranslation();
  const nav = useNavigate();
  const search = useSearch({ from: "/reset" });
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      unwrap(await api.POST("/v1/auth/password/reset", { body: { token: search.token ?? "", password } }));
      await nav({ to: "/login", search: { confirmed: "1" } });
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  return (
    <form className="card sm:!p-8 mt-4" onSubmit={(e) => void submit(e)} aria-label={t("auth.reset")}>
      <AuthHeader title={t("auth.reset")} />
      <Field label={t("auth.newPassword")} name="password" type="password" autoComplete="new-password" required minLength={10} hint={t("auth.passwordHint")} value={password} onChange={(e) => setPassword(e.target.value)} />
      {error && <p role="alert" className="error mb-3">{error}</p>}
      <button className="btn w-full">{t("auth.reset")}</button>
    </form>
  );
}
