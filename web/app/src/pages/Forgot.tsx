import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";
import { AuthHeader } from "@/components/AuthHeader";

export function Forgot() {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      unwrap(await api.POST("/v1/auth/password/forgot", { body: { email } }));
      setSent(true);
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  if (sent) return <p className="card mt-4 text-center">{t("auth.resetSent")}</p>;
  return (
    <form className="card sm:!p-8 mt-4" onSubmit={(e) => void submit(e)} aria-label={t("auth.forgot")}>
      <AuthHeader title={t("auth.forgot")} />
      <Field label={t("auth.email")} name="email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
      {error && <p role="alert" className="error mb-3">{error}</p>}
      <button className="btn w-full">{t("auth.sendReset")}</button>
    </form>
  );
}
