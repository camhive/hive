import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CamhiveError, unwrap } from "@camhive/api";
import { api, API_URL } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { useMe } from "@/lib/session";
import { formatClaimCode, installCommands, RELEASES_URL, restartCommand, useHives } from "@/lib/hives";
import { Field } from "@/components/Field";

type Platform = "linux-deb" | "linux-rpm" | "windows";
const platforms: Platform[] = ["linux-deb", "linux-rpm", "windows"];

/**
 * Claim wizard (#49): install the agent, enter the code it prints, name the
 * hive, then watch it come online. Three steps on one page so the browser
 * back button never loses the code.
 */
export function AddHive() {
  const { t } = useTranslation();
  const me = useMe();
  const [platform, setPlatform] = useState<Platform>("linux-deb");
  const [claimed, setClaimed] = useState<{ hive_id: string; name: string } | null>(null);
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  return (
    <div className="space-y-6">
      <h1 className="page-title">{t("hives.add")}</h1>
      <InstallStep platform={platform} onPlatform={setPlatform} />
      {claimed ? <OnlineStep hiveID={claimed.hive_id} name={claimed.name} platform={platform} /> : <CodeStep onClaimed={setClaimed} />}
    </div>
  );
}

function InstallStep({ platform, onPlatform }: { platform: Platform; onPlatform: (p: Platform) => void }) {
  const { t } = useTranslation();
  return (
    <section className="card" aria-labelledby="install-title">
      <h2 id="install-title" className="text-lg font-semibold mb-1">{t("hives.step1")}</h2>
      <p className="muted mb-3">{t("hives.installIntro")}</p>
      <div role="tablist" aria-label={t("hives.platform")} className="seg mb-4">
        {platforms.map((p) => (
          <button key={p} role="tab" aria-selected={p === platform} onClick={() => onPlatform(p)}>
            {t(`hives.platforms.${p}`)}
          </button>
        ))}
      </div>
      <ol className="list-decimal pl-5 space-y-2 text-sm">
        <li>
          <a href={RELEASES_URL} target="_blank" rel="noreferrer" className="link">{t("hives.download", { what: t(`hives.artifacts.${platform}`) })}</a>
        </li>
        {installCommands(platform, API_URL).map((cmd) => (
          <li key={cmd}><Command text={cmd} /></li>
        ))}
        <li>{t("hives.codeShown")}</li>
      </ol>
    </section>
  );
}

/** A command line with a copy button; falls back silently where the clipboard is unavailable. */
export function Command({ text }: { text: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* no clipboard permission: the user selects the text */
    }
  }
  return (
    <span className="flex items-center gap-2">
      <code className="block flex-1 rounded px-2 py-1 text-xs overflow-x-auto" style={{ background: "var(--bg)" }}>{text}</code>
      <button type="button" className="link text-xs" onClick={() => void copy()} aria-label={t("hives.copy", { cmd: text })}>{copied ? t("hives.copied") : t("hives.copyShort")}</button>
    </span>
  );
}

function CodeStep({ onClaimed }: { onClaimed: (r: { hive_id: string; name: string }) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [limit, setLimit] = useState(false);
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLimit(false);
    setBusy(true);
    try {
      const res = unwrap(await api.POST("/v1/hives/claim", { body: { code, name: name || undefined } }));
      await qc.invalidateQueries({ queryKey: ["hives"] });
      onClaimed({ hive_id: res.hive_id ?? "", name: res.name ?? name });
    } catch (err) {
      if (err instanceof CamhiveError && err.code === "plan_limit") setLimit(true);
      else setError(errorMessage(t, err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="card" onSubmit={(e) => void submit(e)} aria-labelledby="code-title">
      <h2 id="code-title" className="text-lg font-semibold mb-1">{t("hives.step2")}</h2>
      <p className="muted mb-3">{t("hives.codeIntro")}</p>
      <Field label={t("hives.code")} name="code" required autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder="K7QM-3XZP" value={code} onChange={(e) => setCode(formatClaimCode(e.target.value))} hint={t("hives.codeHint")} />
      <Field label={t("hives.name")} name="name" maxLength={64} placeholder={t("hives.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
      {error && <p role="alert" className="error mb-3">{error}</p>}
      {limit && <PlanLimit />}
      <button className="btn" disabled={busy || code.length < 9}>{t("hives.claim")}</button>
    </form>
  );
}

/** `plan_limit` from the API: show the cap and where the upgrade path lives. */
function PlanLimit() {
  const { t } = useTranslation();
  const [max, setMax] = useState<number | null>(null);
  useEffect(() => {
    let cancelled = false;
    api.GET("/v1/me/plan").then((r) => {
      const ents = r.data?.entitlements as Record<string, unknown> | undefined;
      if (!cancelled && typeof ents?.["hives.max"] === "number") setMax(ents["hives.max"] as number);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, []);
  return (
    <div role="alert" className="callout mb-3 text-sm" data-testid="plan-limit">
      <p className="font-medium">{max === null ? t("hives.limitTitle") : t("hives.limitTitleN", { count: max })}</p>
      <p className="muted mt-1">{t("hives.limitHelp")}</p>
      <p className="mt-2"><Link to="/plan" className="link">{t("hives.limitAction")}</Link></p>
    </div>
  );
}

function OnlineStep({ hiveID, name, platform }: { hiveID: string; name: string; platform: Platform }) {
  const { t } = useTranslation();
  const nav = useNavigate();
  const hives = useHives({ refetchInterval: 3000 });
  const hive = hives.data?.hives.find((h) => h.id === hiveID);
  const online = hive?.online ?? false;
  return (
    <section className="card" aria-labelledby="online-title" data-testid="online-step">
      <h2 id="online-title" className="text-lg font-semibold mb-1">{t("hives.step3")}</h2>
      <p className="muted mb-3">{t("hives.claimedAs", { name: name || hiveID })}</p>
      {online ? (
        <>
          <p role="status" className="mb-3 font-medium">{t("hives.online")}</p>
          <button className="btn" onClick={() => void nav({ to: "/" })}>{t("hives.toHives")}</button>
        </>
      ) : (
        <>
          <p className="mb-2 text-sm">{t("hives.restartHint")}</p>
          <Command text={restartCommand(platform)} />
          <p role="status" className="muted mt-3 text-sm" aria-live="polite">{t("hives.waiting")}</p>
          <p className="mt-3 text-sm"><Link to="/" className="link">{t("hives.later")}</Link></p>
        </>
      )}
    </section>
  );
}
