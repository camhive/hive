import { useState, type FormEvent } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";
import { AuthHeader } from "@/components/AuthHeader";

export function Confirm() {
  const { t } = useTranslation();
  const nav = useNavigate();
  const search = useSearch({ from: "/confirm" });
  const [email, setEmail] = useState(search.email ?? "");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      unwrap(await api.POST("/v1/auth/confirm", { body: { email, code } }));
      await nav({ to: "/login", search: { confirmed: "1" } });
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  return (
    <form className="card sm:!p-8 mt-4" onSubmit={(e) => void submit(e)} aria-label={t("auth.confirm")}>
      <AuthHeader title={t("auth.confirm")} intro={search.email ? t("auth.registerDone", { email: search.email }) : undefined} />
      <Field label={t("auth.email")} name="email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
      <Field label={t("auth.code")} name="code" inputMode="numeric" pattern="[0-9]{6}" required value={code} onChange={(e) => setCode(e.target.value)} />
      {error && <p role="alert" className="error mb-3">{error}</p>}
      <button className="btn w-full">{t("auth.confirm")}</button>
    </form>
  );
}
