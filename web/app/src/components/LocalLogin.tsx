import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CamhiveError, unwrapHive } from "@camhive/api";
import type { Hive } from "@/lib/hives";
import { LocalLoginLocked, clearLocal, signInLocally, writeLocal } from "@/lib/localLogin";
import { useGrant } from "@/lib/useGrant";
import { useHiveClient } from "@/lib/hiveApi";
import { errorMessage } from "@/lib/errors";
import { waitMessage } from "@/lib/retryAfter";
import { Field } from "@/components/Field";

/** The local error texts: the local login has a username, not an e-mail, and a lock says how long it lasts. */
export function localError(t: ReturnType<typeof useTranslation>["t"], err: unknown): string {
  if (err instanceof LocalLoginLocked) return waitMessage(t, "localLogin.locked", err.retryAfterS);
  return err instanceof CamhiveError && err.code === "invalid_credentials" ? t("localLogin.wrong") : errorMessage(t, err);
}

/** Sign-in with the hive's local credentials, offered when the cloud cannot reach the hive but its LAN name answers (#57). */
export function LocalSignIn({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      writeLocal(hive.id, await signInLocally(hive, username, password));
    } catch (err) {
      setError(localError(t, err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="card max-w-md" onSubmit={(e) => void submit(e)} aria-labelledby="local-sign-in-title" data-testid="local-sign-in">
      <h2 id="local-sign-in-title" className="text-lg font-semibold mb-1">{t("localLogin.title")}</h2>
      <p className="muted text-sm mb-4">{t("localLogin.intro")}</p>
      <Field label={t("localLogin.username")} name="local-username" autoComplete="username" required value={username} onChange={(e) => setUsername(e.target.value)} />
      <Field label={t("localLogin.password")} name="local-password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      {error && <p role="alert" className="error text-sm mb-3">{error}</p>}
      <button className="btn" disabled={busy}>{t("localLogin.submit")}</button>
    </form>
  );
}

/** Says the session is local and what it cannot do; ends it on request. */
export function LocalSessionBanner({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  return (
    <div role="status" className="card callout text-sm flex flex-wrap items-center justify-between gap-3" data-testid="local-session">
      <p>{t("localLogin.banner")}</p>
      <button type="button" className="btn btn-secondary" onClick={() => clearLocal(hive.id)}>{t("localLogin.signOut")}</button>
    </div>
  );
}

/** Owner settings for the local login: status, set username and password, turn off. Asks the hive for access only once opened. */
export function LocalLoginSettings({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <section className="card max-w-3xl" aria-labelledby="local-login-title" data-testid="local-login-settings">
      <h2 id="local-login-title" className="text-lg font-semibold mb-1">{t("localLogin.settingsTitle")}</h2>
      <p className="muted text-sm mb-3">{t("localLogin.settingsHelp")}</p>
      {open ? <LocalLoginForm hive={hive} /> : <button type="button" className="btn btn-secondary" disabled={!hive.online} onClick={() => setOpen(true)}>{t("localLogin.open")}</button>}
    </section>
  );
}

function LocalLoginForm({ hive }: { hive: Hive }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const status = useQuery({
    queryKey: ["hive", hive.id, "local-login"],
    queryFn: async () => unwrapHive(await client!.GET("/v0/local-login")),
    enabled: !!client,
  });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  async function run(f: () => Promise<unknown>) {
    setError(null);
    try {
      await f();
      setPassword("");
      await qc.invalidateQueries({ queryKey: ["hive", hive.id, "local-login"] });
    } catch (err) {
      setError(localError(t, err));
    }
  }
  const save = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => unwrapHive(await client!.PUT("/v0/local-login", { body: { username, password } })));
  };
  const turnOff = () => void run(async () => unwrapHive(await client!.DELETE("/v0/local-login")));
  if (access.error) return <p role="alert" className="error text-sm">{errorMessage(t, access.error)}</p>;
  if (!client || !status.data) return <p className="muted text-sm">…</p>;
  const s = status.data;
  return (
    <form onSubmit={save}>
      <p className="text-sm mb-4 font-medium" data-testid="local-login-state">
        {s.enabled ? t("localLogin.on", { username: s.username, date: new Date(s.updated_at ?? "").toLocaleDateString(i18n.language) }) : t("localLogin.off")}
      </p>
      <Field label={t("localLogin.username")} name="local-username" autoComplete="off" required value={username} onChange={(e) => setUsername(e.target.value)} />
      <Field label={t("localLogin.password")} name="local-password" type="password" autoComplete="new-password" required hint={t("localLogin.passwordHint")} value={password} onChange={(e) => setPassword(e.target.value)} />
      {error && <p role="alert" className="error text-sm mb-3">{error}</p>}
      <div className="flex flex-wrap gap-3">
        <button className="btn">{t("localLogin.save")}</button>
        {s.enabled && <button type="button" className="btn btn-secondary" onClick={turnOff}>{t("localLogin.turnOff")}</button>}
      </div>
    </form>
  );
}
